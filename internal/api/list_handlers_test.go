package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"stasher/internal/db"
)

func listItems(t *testing.T, ts *httptest.Server, cookie *http.Cookie, query string) (names []string, total int64) {
	t.Helper()
	resp := doJSON(t, ts, "GET", "/api/v1/items"+query, nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %s = %d", query, resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, resp, &body)
	for _, d := range body.Data {
		names = append(names, d.Name)
	}
	return names, body.Total
}

func TestItemListSearchAndFilters(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "search@test.dev", "longpassword1")

	// locations: House > Cabinet
	house := createLocation(t, ts, cookie, "House", nil)
	cabinet := createLocation(t, ts, cookie, "Cabinet", house)

	mk := func(name, desc string, loc int64, tags ...string) {
		doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
			"name": name, "description": desc, "location_id": loc, "tags": tags,
		}, cookie)
	}
	mk("Raspberry Pi 5", "8GB single board computer", cabinet, "electronics", "sbcs")
	mk("M3 screws", "stainless machine screws", house, "hardware")
	mk("Filament spool", "PLA 1kg for 3D printing", house, "electronics")

	// full-text: name and description both indexed
	if names, _ := listItems(t, ts, cookie, "?q=raspberry"); len(names) != 1 || names[0] != "Raspberry Pi 5" {
		t.Fatalf("q=raspberry -> %v", names)
	}
	if names, _ := listItems(t, ts, cookie, "?q=stainless"); len(names) != 1 || names[0] != "M3 screws" {
		t.Fatalf("q=stainless (description) -> %v", names)
	}
	// FTS special characters must not 500 (Review Focus #2). The raw input is
	// tokenized into quoted phrases, so this yields 200 with 0 matches, not an
	// error. (Adapted from the plan: the verbatim URL contained unencoded
	// spaces/quotes/parens, which net/http rejects with 400 before any handler
	// runs; the q param is percent-encoded here, and 0 matches is the safe
	// result the plan's Review Focus #2 wording expects.)
	if _, total := listItems(t, ts, cookie, "?q="+url.QueryEscape(`"quoted" AND* (near)`)); total != 0 {
		t.Fatal("special-char query errored")
	}
	// whitespace-only q must be skipped rather than sent to FTS MATCH ''
	// (an fts5 syntax error -> 500): the full unfiltered list comes back
	// (Review Focus #2)
	if _, total := listItems(t, ts, cookie, "?q=%20%20"); total != 3 {
		t.Fatal("whitespace-only q should be skipped, not error")
	}
	// a bare "+" URL-decodes to a single space: same skip path
	if _, total := listItems(t, ts, cookie, "?q=+"); total != 3 {
		t.Fatal("q=+ (decodes to space) should be skipped, not error")
	}
	// location filter includes descendants: House matches Cabinet's item too
	if names, total := listItems(t, ts, cookie, "?location_id="+fmtInt64(house)); total != 3 {
		t.Fatalf("location with descendants -> %+v", names)
	}
	if _, total := listItems(t, ts, cookie, "?location_id="+fmtInt64(cabinet)); total != 1 {
		t.Fatal("cabinet should match exactly the one item")
	}
	// tag filter
	if _, total := listItems(t, ts, cookie, "?tag=electronics"); total != 2 {
		t.Fatal("tag=electronics should match 2")
	}
	// visibility filter
	if _, total := listItems(t, ts, cookie, "?visibility=shared"); total != 0 {
		t.Fatal("nothing shared yet")
	}

	// sort/order round-trip (final-review Finding 1). The three extra items
	// are created in an order (Cherries, Apples, Bananas) whose insertion
	// order - ascending OR descending - matches neither name asc nor qty
	// desc, so passes below cannot be an artifact of timestamp luck.
	attic := createLocation(t, ts, cookie, "Attic", nil)
	mkQ := func(name string, qty float64) {
		doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
			"name": name, "qty": qty, "location_id": attic,
		}, cookie)
	}
	mkQ("Cherries", 2)
	mkQ("Apples", 99)
	mkQ("Bananas", 7)

	// name asc: deterministic full order
	if names, _ := listItems(t, ts, cookie, "?sort=name&order=asc"); strings.Join(names, ",") !=
		"Apples,Bananas,Cherries,Filament spool,M3 screws,Raspberry Pi 5" {
		t.Fatalf("sort=name&order=asc -> %v", names)
	}
	// qty desc: highest qty first (the three default-qty items tie on 1)
	if names, _ := listItems(t, ts, cookie, "?sort=qty&order=desc"); len(names) != 6 ||
		names[0] != "Apples" || names[1] != "Bananas" || names[2] != "Cherries" {
		t.Fatalf("sort=qty&order=desc -> %v", names)
	}
	// created_at asc: same-second ties make positional name assertions
	// nondeterministic, so assert the returned created_at values ascend
	// (they are RFC3339/UTC strings, so lexical order = chronological)
	resp := doJSON(t, ts, "GET", "/api/v1/items?sort=created_at&order=asc", nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("created_at sort = %d", resp.StatusCode)
	}
	var asc struct {
		Data []struct {
			CreatedAt string `json:"created_at"`
		} `json:"data"`
	}
	decodeBody(t, resp, &asc)
	if len(asc.Data) != 6 {
		t.Fatalf("created_at sort returned %d items", len(asc.Data))
	}
	for i := 1; i < len(asc.Data); i++ {
		if asc.Data[i-1].CreatedAt > asc.Data[i].CreatedAt {
			t.Fatalf("sort=created_at&order=asc not ascending: %v", asc.Data)
		}
	}
	// unknown sort/order values (including injection attempts) fall back to
	// the whitelisted defaults: 200 with the full list, SQL unaltered
	if _, total := listItems(t, ts, cookie, "?sort=updated_at;--&order=SLEEP(1)"); total != 6 {
		t.Fatal("bogus sort/order should fall back to defaults, not error")
	}
}

// Partial-match search (migration 00005: items_fts trigram tokenizer).
// Trigram matches substrings >= 3 chars, so q=raspb finds "raspberry pi"
// (docs/api-notes.md "Search"). Under the old unicode61 tokenizer these
// queries only matched whole whitespace-delimited tokens.
func TestItemListSearchPartialMatch(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "partial@test.dev", "longpassword1")
	mk := func(name, desc string) {
		doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
			"name": name, "description": desc,
		}, cookie)
	}
	mk("raspberry pi", "model b")
	mk("cable", "braided USB-C lead")

	// partial word in the name
	if names, total := listItems(t, ts, cookie, "?q=raspb"); total != 1 || len(names) != 1 || names[0] != "raspberry pi" {
		t.Fatalf("q=raspb (partial) -> %v (total %d)", names, total)
	}
	// partial word in the description
	if _, total := listItems(t, ts, cookie, "?q=braid"); total != 1 {
		t.Fatalf("q=braid (description partial) -> %d matches, want 1", total)
	}
	// matching stays case-insensitive (trigram folds case by default)
	if names, _ := listItems(t, ts, cookie, "?q=RASP"); len(names) != 1 || names[0] != "raspberry pi" {
		t.Fatalf("q=RASP (case-insensitive) -> %v", names)
	}
	// no substring match anywhere -> 200 with empty result
	if _, total := listItems(t, ts, cookie, "?q=abc"); total != 0 {
		t.Fatalf("q=abc -> %d matches, want 0", total)
	}
}

// Short queries cannot form a trigram (3-char minimum). Standalone they
// match nothing without erroring (driver-verified), but mixed queries
// silently DROP the short token ("ab raspberry" -> behaves as "raspberry"),
// so ftsHasShortToken short-circuits to a uniform empty result instead.
// Pin: any query containing a <3-char term -> 200 + empty data, never 500.
func TestItemListSearchShortQuery(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "shortq@test.dev", "longpassword1")
	doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "raspberry pi"}, cookie)

	for _, q := range []string{"ab", "PI", "a", "ab raspberry"} {
		names, total := listItems(t, ts, cookie, "?q="+url.QueryEscape(q))
		if total != 0 || len(names) != 0 {
			t.Fatalf("q=%q -> %v (total %d), want 200 + empty (no >=3-char trigram)", q, names, total)
		}
	}
}

// Same trigram semantics on the shared browse path (separate sqlc query, same index).
func TestSharedListSearchPartialMatch(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "shared-q-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "shared-q-b@test.dev", "longpassword1")

	doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "public widget", "visibility": "shared"}, alice)
	doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "secret jig", "visibility": "private"}, alice)

	if _, total := listItems2(t, ts, bob, "/api/v1/shared?q=widg"); total != 1 {
		t.Fatalf("shared q=widg (partial) -> %d matches, want 1", total)
	}
	if _, total := listItems2(t, ts, bob, "/api/v1/shared?q=abc"); total != 0 {
		t.Fatalf("shared q=abc -> %d matches, want 0", total)
	}
}

// listItems2 is listItems for an arbitrary path (shared list has its own route).
func listItems2(t *testing.T, ts *httptest.Server, cookie *http.Cookie, path string) (names []string, total int64) {
	t.Helper()
	resp := doJSON(t, ts, "GET", path, nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, resp, &body)
	for _, d := range body.Data {
		names = append(names, d.Name)
	}
	return names, body.Total
}

func TestItemListPaginationClamps(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "page@test.dev", "longpassword1")
	for i := 0; i < 3; i++ {
		doJSON(t, ts, "POST", "/api/v1/items",
			map[string]any{"name": fmt.Sprintf("item %d", i)}, cookie)
	}
	// page=0 and page_size=99999 clamp to 1 and 200 (Review Focus #4)
	if _, total := listItems(t, ts, cookie, "?page=0&page_size=99999"); total != 3 {
		t.Fatal("clamped pagination lost items")
	}
	if names, _ := listItems(t, ts, cookie, "?page=2&page_size=2"); len(names) != 1 {
		t.Fatalf("page 2 of 3 items -> %v", names)
	}
	if names, _ := listItems(t, ts, cookie, "?page=9&page_size=2"); names != nil {
		t.Fatalf("past-the-end page should be empty, got %v", names)
	}
	// non-numeric params fall back to defaults
	if _, total := listItems(t, ts, cookie, "?page=abc&page_size=-1"); total != 3 {
		t.Fatal("garbage pagination params broke the list")
	}
}

func TestItemListIsolated(t *testing.T) {
	_, ts := newTestServer(t, nil)
	a := registerUser(t, ts, "iso-a@test.dev", "longpassword1")
	b := registerUser(t, ts, "iso-b@test.dev", "longpassword1")
	doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "a's thing"}, a)
	if _, total := listItems(t, ts, b, ""); total != 0 {
		t.Fatal("b sees a's items")
	}
}

func TestItemListThumbKey(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "thumb@test.dev", "longpassword1")

	// item A: two photos. The FIRST-uploaded gets sort_order 5, the second
	// sort_order 1 — the thumbnail must come from the sort_order-1 photo,
	// proving the ORDER BY sort_order rule (not insertion order).
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "A"}, cookie)
	var itemA struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &itemA)

	upload := func() struct {
		ID         int64  `json:"id"`
		StorageKey string `json:"storage_key"`
	} {
		pr := postFile(t, ts, "/api/v1/items/"+fmtInt64(itemA.ID)+"/photos",
			"file", "pic.png", png1x1, cookie)
		if pr.StatusCode != http.StatusCreated {
			t.Fatalf("photo upload = %d", pr.StatusCode)
		}
		var p struct {
			ID         int64  `json:"id"`
			StorageKey string `json:"storage_key"`
		}
		decodeBody(t, pr, &p)
		return p
	}
	setSort := func(photoID, sort int64) {
		pr := doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(itemA.ID)+"/photos/"+fmtInt64(photoID),
			map[string]any{"sort_order": sort}, cookie)
		if pr.StatusCode != http.StatusOK {
			t.Fatalf("patch photo sort = %d", pr.StatusCode)
		}
	}
	high := upload() // uploaded first, sorted later
	low := upload()
	setSort(high.ID, 5)
	setSort(low.ID, 1)

	// uploads derive real thumbs (Task 3): the expected list value is the
	// lowest-sort photo's DERIVED thumb_key, read straight from its row
	rows, err := s.Queries().ListItemPhotos(context.Background(), itemA.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("photos: %v %d", err, len(rows))
	}
	derived := map[int64]string{}
	for _, ph := range rows {
		if !ph.ThumbKey.Valid || ph.ThumbKey.String == "" || ph.ThumbKey.String == ph.StorageKey {
			t.Fatalf("photo %d lacks a derived thumb: %+v", ph.ID, ph.ThumbKey)
		}
		derived[ph.ID] = ph.ThumbKey.String
	}

	// item B: no photos at all
	doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "B"}, cookie)

	var list struct {
		Data []struct {
			Name     string          `json:"name"`
			ThumbKey json.RawMessage `json:"thumb_key"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items", nil, cookie), &list)
	byName := map[string]json.RawMessage{}
	for _, d := range list.Data {
		byName[d.Name] = d.ThumbKey
	}
	// thumb_key is the lowest-sort photo's DERIVED key (COALESCE picks the
	// generated thumb over the original), raw JSON string
	if got := string(byName["A"]); got != jsonString(derived[low.ID]) {
		t.Fatalf("A thumb_key = %s, want %s (derived thumb of sort_order-1 photo, not %q)",
			got, jsonString(derived[low.ID]), high.StorageKey)
	}
	// no photos -> explicit null (not absent, not "")
	if got := string(byName["B"]); got != "null" {
		t.Fatalf("B thumb_key = %s, want null", got)
	}

	// detail JSON photos[] carry the same non-null derived thumb_key,
	// ordered (sort_order, id) like the list's pick
	detail := doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(itemA.ID), nil, cookie)
	var d struct {
		Photos []struct {
			ID       int64           `json:"id"`
			ThumbKey json.RawMessage `json:"thumb_key"`
		} `json:"photos"`
	}
	decodeBody(t, detail, &d)
	if len(d.Photos) != 2 || d.Photos[0].ID != low.ID {
		t.Fatalf("detail photos = %+v, want low-sort photo first", d.Photos)
	}
	if got := string(d.Photos[0].ThumbKey); got != jsonString(derived[low.ID]) {
		t.Fatalf("detail photos[0] thumb_key = %s, want %s", got, jsonString(derived[low.ID]))
	}
	if got := string(d.Photos[1].ThumbKey); got != jsonString(derived[high.ID]) {
		t.Fatalf("detail photos[1] thumb_key = %s, want %s", got, jsonString(derived[high.ID]))
	}
}

// A thumb key from a shared item must serve to the sharee (200), and a
// private item's thumb key must 404 for a stranger — the /files gate
// applies to derived files exactly as it does to originals.
func TestThumbKeyGate(t *testing.T) {
	s, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "gate-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "gate-b@test.dev", "longpassword1")
	mallory := registerUser(t, ts, "gate-m@test.dev", "longpassword1")

	mkItem := func(name, visibility string) int64 {
		resp := doJSON(t, ts, "POST", "/api/v1/items",
			map[string]any{"name": name, "visibility": visibility}, alice)
		var item struct {
			ID int64 `json:"id"`
		}
		decodeBody(t, resp, &item)
		return item.ID
	}
	sharedID := mkItem("gated widget", "shared")
	mkItem("bare widget", "shared") // shared, no photos: NULL thumb path
	privID := mkItem("secret widget", "private")
	for _, id := range []int64{sharedID, privID} {
		pr := postFile(t, ts, "/api/v1/items/"+fmtInt64(id)+"/photos",
			"file", "widget.jpg", jpegBytes(t, 900, 600), alice)
		if pr.StatusCode != http.StatusCreated {
			t.Fatalf("photo upload = %d", pr.StatusCode)
		}
	}
	thumbOf := func(itemID int64) string {
		rows, err := s.Queries().ListItemPhotos(context.Background(), itemID)
		if err != nil || len(rows) != 1 || !rows[0].ThumbKey.Valid || rows[0].ThumbKey.String == rows[0].StorageKey {
			t.Fatalf("photos for item %d: %v %+v", itemID, err, rows)
		}
		return rows[0].ThumbKey.String
	}
	sharedThumb := thumbOf(sharedID)
	privThumb := thumbOf(privID)

	// the owner's detail JSON photos[] carry the non-null derived thumb_key
	var detail struct {
		Photos []struct {
			ThumbKey json.RawMessage `json:"thumb_key"`
		} `json:"photos"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(sharedID), nil, alice), &detail)
	if len(detail.Photos) != 1 || string(detail.Photos[0].ThumbKey) != jsonString(sharedThumb) {
		t.Fatalf("detail photos thumb_key = %+v, want %s", detail.Photos, jsonString(sharedThumb))
	}

	// bob (sharee) sees the shared items in /shared: the photo-bearing row
	// with the derived thumb, the photoless row with an explicit null (the
	// NULL-scan path the query's NULLIF wrapper protects)
	var list struct {
		Data []struct {
			Name     string          `json:"name"`
			ThumbKey json.RawMessage `json:"thumb_key"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/shared", nil, bob), &list)
	byName := map[string]json.RawMessage{}
	for _, d := range list.Data {
		byName[d.Name] = d.ThumbKey
	}
	if len(byName) != 2 {
		t.Fatalf("shared list = %+v, want the two shared items", list)
	}
	if got := string(byName["gated widget"]); got != jsonString(sharedThumb) {
		t.Fatalf("shared thumb_key = %s, want %s", got, jsonString(sharedThumb))
	}
	if got := string(byName["bare widget"]); got != "null" {
		t.Fatalf("photoless shared thumb_key = %s, want null", got)
	}

	getFile := func(key string, cookie *http.Cookie) int {
		req, _ := http.NewRequest("GET", ts.URL+"/files/"+key, nil)
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// the sharee can fetch the derived thumb (gate matches thumb_key)
	if c := getFile(sharedThumb, bob); c != http.StatusOK {
		t.Fatalf("sharee thumb fetch = %d, want 200", c)
	}
	// strangers get 404 (not 403) for a private item's derived thumb
	if c := getFile(privThumb, mallory); c != http.StatusNotFound {
		t.Fatalf("stranger private thumb fetch = %d, want 404", c)
	}
	if c := getFile(privThumb, bob); c != http.StatusNotFound {
		t.Fatalf("sharee private-item thumb fetch = %d, want 404", c)
	}
}

// A photo with no derived thumb (legacy row, corrupt original) surfaces its
// ORIGINAL storage_key as thumb_key on lists — the COALESCE fallback
// documented in docs/api-notes.md.
func TestItemListThumbKeyFallsBackToOriginal(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "thumb-null@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "legacy"}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)

	// real file on disk, row inserted with a NULL thumb (pre-thumb-era
	// shape, as left behind by failed generation or a legacy photo)
	key, _, err := s.Storage().Save(bytes.NewReader(png1x1), "png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Queries().AddItemPhoto(context.Background(), db.AddItemPhotoParams{
		ItemID: item.ID, StorageKey: key, ThumbKey: sql.NullString{},
		Caption: "", SortOrder: 0, CreatedAt: timeNow(t),
	}); err != nil {
		t.Fatal(err)
	}

	var list struct {
		Data []struct {
			Name     string          `json:"name"`
			ThumbKey json.RawMessage `json:"thumb_key"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items", nil, cookie), &list)
	if len(list.Data) != 1 || list.Data[0].Name != "legacy" {
		t.Fatalf("list = %+v", list)
	}
	if got := string(list.Data[0].ThumbKey); got != jsonString(key) {
		t.Fatalf("thumb_key = %s, want original storage_key %s (COALESCE fallback)", got, jsonString(key))
	}
}
