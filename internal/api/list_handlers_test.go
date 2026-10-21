package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
	_, ts := newTestServer(t, nil)
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
	// thumb_key is the lowest-sort photo's storage key (raw JSON string)
	if got := string(byName["A"]); got != jsonString(low.StorageKey) {
		t.Fatalf("A thumb_key = %s, want %s (sort_order-1 photo, not %q)",
			got, jsonString(low.StorageKey), high.StorageKey)
	}
	// no photos -> explicit null (not absent, not "")
	if got := string(byName["B"]); got != "null" {
		t.Fatalf("B thumb_key = %s, want null", got)
	}
}
