package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// createLocation helper returns the new location's id.
func createLocation(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string, parentID any) int64 {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/locations",
		map[string]any{"name": name, "parent_id": parentID}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create location %q = %d", name, resp.StatusCode)
	}
	var body struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &body)
	return body.ID
}

func TestLocationTreeAndBreadcrumbs(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "loc@test.dev", "longpassword1")
	building := createLocation(t, ts, cookie, "House", nil)
	cabinet := createLocation(t, ts, cookie, "Cabinet", building)
	bin := createLocation(t, ts, cookie, "Parts Bin 1", cabinet)

	var got struct {
		Location struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"location"`
		Breadcrumbs []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"breadcrumbs"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations/"+fmtInt64(bin), nil, cookie), &got)
	if got.Location.ID != bin {
		t.Fatalf("location = %+v", got.Location)
	}
	if len(got.Breadcrumbs) != 3 ||
		got.Breadcrumbs[0].ID != building || got.Breadcrumbs[1].ID != cabinet ||
		got.Breadcrumbs[2].ID != bin {
		t.Fatalf("breadcrumbs = %+v", got.Breadcrumbs)
	}

	var list struct {
		Data []struct {
			ID       int64  `json:"id"`
			ParentID *int64 `json:"parent_id"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations", nil, cookie), &list)
	if list.Total != 3 || len(list.Data) != 3 {
		t.Fatalf("list = %+v", list)
	}
}

func TestLocationNameUniquePerParent(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "uniq@test.dev", "longpassword1")
	createLocation(t, ts, cookie, "Shelf", nil)
	// same name under same parent -> 409
	if resp := doJSON(t, ts, "POST", "/api/v1/locations",
		map[string]any{"name": "Shelf"}, cookie); resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup location = %d, want 409", resp.StatusCode)
	}
	// same name under a different parent -> ok
	parent := createLocation(t, ts, cookie, "Cabinet", nil)
	if resp := doJSON(t, ts, "POST", "/api/v1/locations",
		map[string]any{"name": "Shelf", "parent_id": parent}, cookie); resp.StatusCode != http.StatusCreated {
		t.Fatalf("same name other parent = %d, want 201", resp.StatusCode)
	}
}

func TestLocationCycleRejected(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "cyc@test.dev", "longpassword1")
	a := createLocation(t, ts, cookie, "A", nil)
	b := createLocation(t, ts, cookie, "B", a)
	// moving A under B would create a cycle
	resp := doJSON(t, ts, "PATCH", "/api/v1/locations/"+fmtInt64(a),
		map[string]any{"parent_id": b}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cycle move = %d, want 400", resp.StatusCode)
	}
	// moving A under itself also rejected
	resp = doJSON(t, ts, "PATCH", "/api/v1/locations/"+fmtInt64(a),
		map[string]any{"parent_id": a}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self move = %d, want 400", resp.StatusCode)
	}
}

func TestLocationIsolation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "alice-loc@test.dev", "longpassword1")
	bob := registerUser(t, ts, "bob-loc@test.dev", "longpassword1")
	id := createLocation(t, ts, alice, "Alice's shelf", nil)
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/locations/" + fmtInt64(id)},
		{"PATCH", "/api/v1/locations/" + fmtInt64(id)},
		{"DELETE", "/api/v1/locations/" + fmtInt64(id)},
	} {
		if resp := doJSON(t, ts, tc.m, tc.p, map[string]any{"name": "X"}, bob); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s other user's location = %d, want 404", tc.m, resp.StatusCode)
		}
	}
	// bob creating under alice's parent is rejected
	resp := doJSON(t, ts, "POST", "/api/v1/locations",
		map[string]any{"name": "B-child", "parent_id": id}, bob)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("create under foreign parent = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteLocationReparentsAndClearsItems(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "del@test.dev", "longpassword1")
	building := createLocation(t, ts, cookie, "B", nil)
	cabinet := createLocation(t, ts, cookie, "C", building)
	bin := createLocation(t, ts, cookie, "D", cabinet)

	// an item in the cabinet (created via raw SQL; items API comes in Task 13)
	mustExec(t, s, `INSERT INTO items (user_id, name, qty, location_id, visibility, created_at, updated_at)
		SELECT id, 'widget', 1, ?, 'private', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z' FROM users LIMIT 1`, cabinet)

	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(cabinet), nil, cookie)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	var binBody struct {
		Location struct {
			ParentID *int64 `json:"parent_id"`
		} `json:"location"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations/"+fmtInt64(bin), nil, cookie), &binBody)
	if binBody.Location.ParentID == nil || *binBody.Location.ParentID != building {
		t.Fatalf("bin not reparented to building: %+v", binBody.Location)
	}
	var n int64
	mustQueryRow(t, s, `SELECT COUNT(*) FROM items WHERE location_id IS NULL AND name='widget'`).Scan(&n)
	if n != 1 {
		t.Fatalf("item not detached from deleted location: n=%d", n)
	}
}
