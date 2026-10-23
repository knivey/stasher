package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestItemCreateGetUpdateDelete(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "item@test.dev", "longpassword1")

	// create with tag names (auto-created), qty numeric, shared visibility
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
		"name": "M3 screws", "description": "stainless", "qty": 250.5, "unit": "pcs",
		"visibility": "shared", "tags": []string{"hardware", "fasteners"},
	}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create item = %d", resp.StatusCode)
	}
	var created struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Qty        float64 `json:"qty"`
		Unit       string  `json:"unit"`
		Visibility string  `json:"visibility"`
		Tags       []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Photos    []any `json:"photos"`
		Documents []any `json:"documents"`
		Projects  []any `json:"projects"`
	}
	decodeBody(t, resp, &created)
	if created.Qty != 250.5 || created.Visibility != "shared" || len(created.Tags) != 2 ||
		len(created.Photos) != 0 || len(created.Projects) != 0 {
		t.Fatalf("created = %+v", created)
	}

	// get detail
	var got struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie), &got)
	if len(got.Tags) != 2 {
		t.Fatalf("detail tags = %+v", got.Tags)
	}

	// patch: qty + replace tags
	resp = doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID),
		map[string]any{"qty": 100, "tags": []string{"hardware"}}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch item = %d", resp.StatusCode)
	}
	var patched struct {
		Qty  float64 `json:"qty"`
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	decodeBody(t, resp, &patched)
	if patched.Qty != 100 || len(patched.Tags) != 1 || patched.Tags[0].Name != "hardware" {
		t.Fatalf("patched = %+v", patched)
	}

	// invalid qty rejected
	if resp := doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID),
		map[string]any{"qty": -5}, cookie); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative qty = %d, want 400", resp.StatusCode)
	}
	// foreign location rejected
	if resp := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "X", "location_id": 99999}, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign location = %d, want 404", resp.StatusCode)
	}

	// delete
	if resp := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp := doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted = %d, want 404", resp.StatusCode)
	}
}

func TestItemVisibilityDefaultsToUserSetting(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "vis@test.dev", "longpassword1")
	// flip account default to shared
	doJSON(t, ts, "PATCH", "/api/v1/auth/me",
		map[string]any{"default_item_visibility": "shared"}, cookie)
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "auto-shared"}, cookie)
	var created struct {
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &created)
	if created.Visibility != "shared" {
		t.Fatalf("default visibility = %q, want shared", created.Visibility)
	}
}

func TestItemIsolation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "alice-item@test.dev", "longpassword1")
	bob := registerUser(t, ts, "bob-item@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "private thing"}, alice)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &created)
	// bob cannot see, patch, or delete alice's PRIVATE item — 404 each time
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/items/" + fmtInt64(created.ID)},
		{"PATCH", "/api/v1/items/" + fmtInt64(created.ID)},
		{"DELETE", "/api/v1/items/" + fmtInt64(created.ID)},
	} {
		if resp := doJSON(t, ts, tc.m, tc.p, map[string]any{"name": "stolen"}, bob); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s private item = %d, want 404", tc.m, resp.StatusCode)
		}
	}
}

// TestItemInvalidTagsLeaveNoGhostRow pins final-review Finding 2: tag
// validation must run BEFORE the item row is written, so a 400 for an
// invalid tag list leaves no ghost item behind.
func TestItemInvalidTagsLeaveNoGhostRow(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "ghost@test.dev", "longpassword1")

	if _, total := listItems(t, ts, cookie, ""); total != 0 {
		t.Fatalf("fresh user should have 0 items, got %d", total)
	}
	// empty tag name -> 400, and the item row must NOT persist
	resp := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "ghost test", "tags": []string{""}}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with bad tag = %d, want 400", resp.StatusCode)
	}
	if _, total := listItems(t, ts, cookie, ""); total != 0 {
		t.Fatalf("ghost row persisted after 400: total = %d, want 0", total)
	}
}

// TestItemPatchInvalidTagsDoesNotPartiallyApply pins the PATCH sibling of
// Finding 2: when the tag list is invalid, no other field in the same
// request may be written either.
func TestItemPatchInvalidTagsDoesNotPartiallyApply(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "partial@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "real item"}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &created)

	resp = doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID),
		map[string]any{"name": "renamed", "tags": []string{"ok", ""}}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("patch with bad tag = %d, want 400", resp.StatusCode)
	}
	var got struct {
		Name string `json:"name"`
		Tags []any  `json:"tags"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie), &got)
	if got.Name != "real item" {
		t.Fatalf("patch partially applied name: %q", got.Name)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("patch partially applied tags: %+v", got.Tags)
	}
}

// noteJSON mirrors one item_notes row as returned by the notes endpoints
// and the item detail's notes array.
type noteJSON struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

func addItemNote(t *testing.T, ts *httptest.Server, cookie *http.Cookie, itemID int64, body string) noteJSON {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(itemID)+"/notes",
		map[string]any{"body": body}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add note %q = %d, want 201", body, resp.StatusCode)
	}
	var n noteJSON
	decodeBody(t, resp, &n)
	return n
}

// TestItemNotesLifecycle: POST creates a trimmed note (201, created_at set),
// the detail JSON lists notes newest-first, invalid bodies 400 with the
// documented error shape, and DELETE removes a note (204; repeat 404).
func TestItemNotesLifecycle(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "notes-life@test.dev", "longpassword1")
	id := createQtyItem(t, ts, cookie, "bench psu", 1)

	// two adds in quick succession share an RFC3339 second, so the id DESC
	// tiebreaker decides which one is "newest"
	n1 := addItemNote(t, ts, cookie, id, "  first note  ")
	if n1.Body != "first note" {
		t.Fatalf("note body = %q, want trimmed %q", n1.Body, "first note")
	}
	if n1.CreatedAt == "" {
		t.Fatal("note created_at empty")
	}
	n2 := addItemNote(t, ts, cookie, id, "second")
	if n2.ID == n1.ID {
		t.Fatal("both notes share one id")
	}

	// detail JSON carries the notes array newest-first
	var detail struct {
		Notes []noteJSON `json:"notes"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, cookie), &detail)
	if len(detail.Notes) != 2 || detail.Notes[0].ID != n2.ID || detail.Notes[1].ID != n1.ID {
		t.Fatalf("detail notes = %+v, want newest-first [%d %d]", detail.Notes, n2.ID, n1.ID)
	}
	if detail.Notes[1].CreatedAt != n1.CreatedAt {
		t.Fatalf("note[1] created_at = %q, want %q", detail.Notes[1].CreatedAt, n1.CreatedAt)
	}

	// validation: empty, whitespace-only, and 2001-char bodies all 400
	for name, body := range map[string]string{
		"empty":      "",
		"whitespace": "   \n\t ",
		"2001chars":  strings.Repeat("x", 2001),
	} {
		resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/notes",
			map[string]any{"body": body}, cookie)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s body = %d, want 400", name, resp.StatusCode)
		}
		var errBody struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeBody(t, resp, &errBody)
		if errBody.Error.Code != "validation_error" || errBody.Error.Message != "note must be 1-2000 characters" {
			t.Fatalf("%s body error = %+v", name, errBody.Error)
		}
	}
	// the failed attempts wrote nothing
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, cookie), &detail)
	if len(detail.Notes) != 2 {
		t.Fatalf("notes after rejected posts = %d, want 2", len(detail.Notes))
	}

	// delete: 204, then 404 on repeat
	if resp := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(id)+"/notes/"+fmtInt64(n1.ID), nil, cookie); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete note = %d, want 204", resp.StatusCode)
	}
	if resp := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(id)+"/notes/"+fmtInt64(n1.ID), nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("re-delete note = %d, want 404", resp.StatusCode)
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, cookie), &detail)
	if len(detail.Notes) != 1 || detail.Notes[0].ID != n2.ID {
		t.Fatalf("notes after delete = %+v, want only %d", detail.Notes, n2.ID)
	}
}

// TestItemNotesIsolation: notes endpoints are owner-only — a foreign item
// is 404 (never 403), and a non-numeric note id is 400 invalid_id.
func TestItemNotesIsolation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "notes-iso-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "notes-iso-b@test.dev", "longpassword1")
	id := createQtyItem(t, ts, alice, "alice multimeter", 1)

	// bob cannot read or write alice's notes
	if resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/notes",
		map[string]any{"body": "bob was here"}, bob); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign POST note = %d, want 404", resp.StatusCode)
	}
	if resp := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(id)+"/notes/1", nil, bob); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign DELETE note = %d, want 404", resp.StatusCode)
	}
	// non-numeric note id -> 400 invalid_id (after the ownership gate)
	if resp := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(id)+"/notes/nope", nil, alice); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad note id = %d, want 400", resp.StatusCode)
	}
	// none of the failed attempts wrote anything
	var detail struct {
		Notes []any `json:"notes"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, alice), &detail)
	if len(detail.Notes) != 0 {
		t.Fatalf("notes after failed attempts = %+v, want empty", detail.Notes)
	}
}

// TestItemProductURL: product_url is a detail-only item field. Create and
// PATCH validate it as an http(s) URL of at most 2000 chars (400
// validation_error otherwise); PATCH follows the house nullable rules
// (absent keeps, "" clears to an explicit JSON null); the LIST envelope
// never carries it.
func TestItemProductURL(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "purl@test.dev", "longpassword1")

	// create echoes the product_url back on the detail JSON
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
		"name": "Raspberry Pi 5", "product_url": "https://example.com/pi5",
	}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create item = %d", resp.StatusCode)
	}
	var created struct {
		ID         int64   `json:"id"`
		ProductURL *string `json:"product_url"`
	}
	decodeBody(t, resp, &created)
	if created.ProductURL == nil || *created.ProductURL != "https://example.com/pi5" {
		t.Fatalf("created product_url = %v, want https://example.com/pi5", created.ProductURL)
	}
	id := fmtInt64(created.ID)

	// invalid values are all rejected 400 validation_error before any write
	for _, bad := range []string{
		"ftp://example.com/part", // wrong scheme
		"not a url",              // no scheme at all
		"https://example.com/" + strings.Repeat("a", 1981), // 2001 chars
	} {
		if r := doJSON(t, ts, "POST", "/api/v1/items",
			map[string]any{"name": "bad url", "product_url": bad}, cookie); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("create product_url %.30q... = %d, want 400", bad, r.StatusCode)
		}
	}
	// the 2000-char boundary is inclusive: exactly 2000 chars creates fine
	resp = doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
		"name":        "boundary",
		"product_url": "https://example.com/" + strings.Repeat("a", 1980), // 20 + 1980 = 2000
	}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("2000-char product_url = %d, want 201", resp.StatusCode)
	}
	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "bad url", "product_url": "ftp://example.com/part"}, cookie), &errBody)
	if errBody.Error.Code != "validation_error" ||
		errBody.Error.Message != "product_url must be an http(s) URL of at most 2000 chars" {
		t.Fatalf("error = %+v, want validation_error / product_url message", errBody.Error)
	}

	// GET detail carries the field
	var got struct {
		ProductURL *string `json:"product_url"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+id, nil, cookie), &got)
	if got.ProductURL == nil || *got.ProductURL != "https://example.com/pi5" {
		t.Fatalf("detail product_url = %v, want https://example.com/pi5", got.ProductURL)
	}

	// PATCH absent keeps (only qty changes here)
	if resp := doJSON(t, ts, "PATCH", "/api/v1/items/"+id, map[string]any{"qty": 3}, cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("patch qty = %d", resp.StatusCode)
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+id, nil, cookie), &got)
	if got.ProductURL == nil || *got.ProductURL != "https://example.com/pi5" {
		t.Fatalf("product_url after absent PATCH = %v, want kept", got.ProductURL)
	}

	// PATCH a valid value updates
	resp = doJSON(t, ts, "PATCH", "/api/v1/items/"+id, map[string]any{
		"product_url": "https://example.com/pi5-kit",
	}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch product_url = %d", resp.StatusCode)
	}
	decodeBody(t, resp, &got)
	if got.ProductURL == nil || *got.ProductURL != "https://example.com/pi5-kit" {
		t.Fatalf("patched product_url = %v, want https://example.com/pi5-kit", got.ProductURL)
	}

	// a usage (ledger) rewrite of the item row must preserve the field
	if resp := doJSON(t, ts, "POST", "/api/v1/items/"+id+"/usage",
		map[string]any{"kind": "used", "delta": -1}, cookie); resp.StatusCode != http.StatusCreated {
		t.Fatalf("usage = %d", resp.StatusCode)
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+id, nil, cookie), &got)
	if got.ProductURL == nil || *got.ProductURL != "https://example.com/pi5-kit" {
		t.Fatalf("product_url after usage = %v, want preserved", got.ProductURL)
	}

	// PATCH "" clears to an explicit JSON null
	resp = doJSON(t, ts, "PATCH", "/api/v1/items/"+id, map[string]any{"product_url": ""}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear product_url = %d", resp.StatusCode)
	}
	decodeBody(t, resp, &got)
	if got.ProductURL != nil {
		t.Fatalf("cleared product_url = %v, want null", *got.ProductURL)
	}

	// LIST rows never carry the detail-only field
	var list struct {
		Data []map[string]any `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items", nil, cookie), &list)
	if len(list.Data) == 0 {
		t.Fatal("no list rows to inspect")
	}
	if _, has := list.Data[0]["product_url"]; has {
		t.Fatal("list row carries product_url; the field is detail-only")
	}
}
