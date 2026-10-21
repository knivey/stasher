package api

import (
	"net/http"
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
