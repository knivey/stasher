package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createPlacedItem creates an item (optionally in a location: locID nil = no
// location, int64 = placed) and returns its id and updated_at.
func createPlacedItem(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string, locID any) (int64, string) {
	t.Helper()
	body := map[string]any{"name": name}
	if locID != nil {
		body["location_id"] = locID
	}
	resp := doJSON(t, ts, "POST", "/api/v1/items", body, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create item %q = %d", name, resp.StatusCode)
	}
	var got struct {
		ID        int64  `json:"id"`
		UpdatedAt string `json:"updated_at"`
	}
	decodeBody(t, resp, &got)
	return got.ID, got.UpdatedAt
}

// backdateItem forces an item's updated_at to a fixed past instant so an
// updated_at bump is assertable despite RFC3339's one-second granularity.
func backdateItem(t *testing.T, s *Server, itemID int64) {
	t.Helper()
	mustExec(t, s, "UPDATE items SET updated_at = ? WHERE id = ?",
		"2000-01-01T00:00:00Z", itemID)
}

type bulkMoveResp struct {
	Moved int64 `json:"moved"`
}

// bulkMove POSTs /items/bulk-move and returns the response + decoded body.
func bulkMove(t *testing.T, ts *httptest.Server, cookie *http.Cookie, ids []int64, locID any) (*http.Response, bulkMoveResp) {
	t.Helper()
	body := map[string]any{"item_ids": ids}
	if locID != nil {
		body["location_id"] = locID
	}
	resp := doJSON(t, ts, "POST", "/api/v1/items/bulk-move", body, cookie)
	var out bulkMoveResp
	if resp.StatusCode == http.StatusOK {
		decodeBody(t, resp, &out)
	}
	return resp, out
}

func TestBulkMoveItemsMovesAll(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulkmove@test.dev", "longpassword1")
	shelf := createLocation(t, ts, cookie, "Shelf", nil)
	drawer := createLocation(t, ts, cookie, "Drawer", nil)

	var ids []int64
	for _, name := range []string{"m3 screws", "resistors", "capacitors"} {
		id, _ := createPlacedItem(t, ts, cookie, name, shelf)
		ids = append(ids, id)
		backdateItem(t, s, id)
	}

	resp, out := bulkMove(t, ts, cookie, ids, drawer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk-move = %d, want 200", resp.StatusCode)
	}
	if out.Moved != 3 {
		t.Fatalf("moved = %d, want 3", out.Moved)
	}
	for _, id := range ids {
		var got struct {
			LocationID *int64 `json:"location_id"`
			UpdatedAt  string `json:"updated_at"`
		}
		decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, cookie), &got)
		if got.LocationID == nil || *got.LocationID != drawer {
			t.Fatalf("item %d location = %v, want %d", id, got.LocationID, drawer)
		}
		if got.UpdatedAt == "2000-01-01T00:00:00Z" {
			t.Fatalf("item %d updated_at was not bumped", id)
		}
	}
}

func TestBulkMoveIgnoresForeignIDs(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "bulk-alice2@test.dev", "longpassword1")
	bob := registerUser(t, ts, "bulk-bob2@test.dev", "longpassword1")

	loc := createLocation(t, ts, alice, "Alice Drawer", nil)
	bobLoc := createLocation(t, ts, bob, "Bob Drawer", nil)
	a1, _ := createPlacedItem(t, ts, alice, "alice item 1", nil)
	a2, _ := createPlacedItem(t, ts, alice, "alice item 2", nil)
	b1, _ := createPlacedItem(t, ts, bob, "bob item", bobLoc)

	resp, out := bulkMove(t, ts, alice, []int64{a1, a2, b1}, loc)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk-move = %d, want 200", resp.StatusCode)
	}
	if out.Moved != 2 {
		t.Fatalf("moved = %d, want 2 (bob's id must not count)", out.Moved)
	}
	for _, id := range []int64{a1, a2} {
		var got struct {
			LocationID *int64 `json:"location_id"`
		}
		decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, alice), &got)
		if got.LocationID == nil || *got.LocationID != loc {
			t.Fatalf("item %d location = %v, want %d", id, got.LocationID, loc)
		}
	}
	// bob's item is untouched, still in bob's own location
	var got struct {
		LocationID *int64 `json:"location_id"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(b1), nil, bob), &got)
	if got.LocationID == nil || *got.LocationID != bobLoc {
		t.Fatalf("foreign item %d location = %v, want unchanged %d", b1, got.LocationID, bobLoc)
	}
}

func TestBulkMoveValidation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-valid@test.dev", "longpassword1")
	loc := createLocation(t, ts, cookie, "Somewhere", nil)

	ids201 := make([]int64, 201)
	for i := range ids201 {
		ids201[i] = int64(i + 1)
	}
	for _, tc := range []struct {
		name string
		ids  []int64
		loc  any
		msg  string
	}{
		{"zero ids", []int64{}, loc, "1-200"},
		{"201 ids", ids201, loc, "1-200"},
		{"zero id in list", []int64{1, 0}, loc, "positive"},
		{"negative id in list", []int64{5, -2}, loc, "positive"},
		{"missing location", []int64{1}, nil, "location_id"},
		{"negative location", []int64{1}, -1, "location_id"},
	} {
		resp, _ := bulkMove(t, ts, cookie, tc.ids, tc.loc)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, resp.StatusCode)
			continue
		}
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeBody(t, resp, &e)
		if e.Error.Code != "validation_error" {
			t.Errorf("%s: code = %q, want validation_error", tc.name, e.Error.Code)
		}
		if !strings.Contains(e.Error.Message, tc.msg) {
			t.Errorf("%s: message = %q, want it to contain %q", tc.name, e.Error.Message, tc.msg)
		}
	}
}

func TestBulkMoveZeroClearsLocations(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-clear@test.dev", "longpassword1")
	shelf := createLocation(t, ts, cookie, "Shelf", nil)
	i1, _ := createPlacedItem(t, ts, cookie, "one", shelf)
	i2, _ := createPlacedItem(t, ts, cookie, "two", shelf)

	resp, out := bulkMove(t, ts, cookie, []int64{i1, i2}, 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk-move = %d, want 200", resp.StatusCode)
	}
	if out.Moved != 2 {
		t.Fatalf("moved = %d, want 2", out.Moved)
	}
	for _, id := range []int64{i1, i2} {
		var got struct {
			LocationID *int64 `json:"location_id"`
		}
		decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, cookie), &got)
		if got.LocationID != nil {
			t.Fatalf("item %d location = %d, want null (cleared)", id, *got.LocationID)
		}
	}
}

func TestBulkMoveForeignLocationNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "bulk-alice3@test.dev", "longpassword1")
	bob := registerUser(t, ts, "bulk-bob3@test.dev", "longpassword1")

	foreign := createLocation(t, ts, bob, "Bob's shelf", nil)
	i1, _ := createPlacedItem(t, ts, alice, "one", nil)

	resp, _ := bulkMove(t, ts, alice, []int64{i1}, foreign)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bulk-move to foreign location = %d, want 404", resp.StatusCode)
	}
	// the item's (absent) location is unchanged after the 404
	var got struct {
		LocationID *int64 `json:"location_id"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(i1), nil, alice), &got)
	if got.LocationID != nil {
		t.Fatalf("item %d location = %d after 404, want still null", i1, *got.LocationID)
	}
}

func TestBulkMoveDisposedItemMovable(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-disposed@test.dev", "longpassword1")
	shelf := createLocation(t, ts, cookie, "Shelf", nil)
	drawer := createLocation(t, ts, cookie, "Drawer", nil)

	id, _ := createPlacedItem(t, ts, cookie, "dead PSU", shelf)
	if resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/usage",
		map[string]any{"kind": "disposed"}, cookie); resp.StatusCode != http.StatusCreated {
		t.Fatalf("dispose = %d, want 201", resp.StatusCode)
	}

	resp, out := bulkMove(t, ts, cookie, []int64{id}, drawer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk-move = %d, want 200", resp.StatusCode)
	}
	if out.Moved != 1 {
		t.Fatalf("moved = %d, want 1 (disposed items have no special case)", out.Moved)
	}
	var got struct {
		LocationID *int64 `json:"location_id"`
		Disposed   bool   `json:"disposed"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, cookie), &got)
	if got.LocationID == nil || *got.LocationID != drawer {
		t.Fatalf("disposed item location = %v, want %d", got.LocationID, drawer)
	}
	if !got.Disposed {
		t.Fatal("disposed flag must survive a location move")
	}
}
