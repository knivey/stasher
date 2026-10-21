package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func createTag(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string) {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/tags", map[string]any{"name": name}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create tag %q = %d", name, resp.StatusCode)
	}
}

func TestTagCRUDAndDuplication(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "tag@test.dev", "longpassword1")
	createTag(t, ts, cookie, "electronics")

	// duplicate name -> 409
	if resp := doJSON(t, ts, "POST", "/api/v1/tags", map[string]any{"name": "electronics"}, cookie); resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup tag = %d, want 409", resp.StatusCode)
	}
	// list shows it
	var list struct {
		Data []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			ItemCount int64  `json:"item_count"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/tags", nil, cookie), &list)
	if len(list.Data) != 1 || list.Data[0].Name != "electronics" {
		t.Fatalf("tags = %+v", list.Data)
	}
	if list.Data[0].ItemCount != 0 {
		t.Fatalf("item_count = %d, want 0 for a fresh tag", list.Data[0].ItemCount)
	}
	id := list.Data[0].ID

	// rename
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+fmtInt64(id), map[string]any{"name": "electronics-parts"}, cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename = %d, want 200", resp.StatusCode)
	}
	// rename to an existing name -> 409 (create the collision first)
	createTag(t, ts, cookie, "other")
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+fmtInt64(id), map[string]any{"name": "other"}, cookie); resp.StatusCode != http.StatusConflict {
		t.Fatalf("colliding rename = %d, want 409", resp.StatusCode)
	}
	// delete
	if resp := doJSON(t, ts, "DELETE", "/api/v1/tags/"+fmtInt64(id), nil, cookie); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete tag = %d, want 204", resp.StatusCode)
	}
	// unknown id -> 404
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/999999", map[string]any{"name": "nope"}, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch unknown tag = %d, want 404", resp.StatusCode)
	}
	if resp := doJSON(t, ts, "DELETE", "/api/v1/tags/999999", nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete unknown tag = %d, want 404", resp.StatusCode)
	}
}

func TestTagNamesScopedPerUser(t *testing.T) {
	_, ts := newTestServer(t, nil)
	a := registerUser(t, ts, "tag-a@test.dev", "longpassword1")
	b := registerUser(t, ts, "tag-b@test.dev", "longpassword1")
	createTag(t, ts, a, "shared-name")
	createTag(t, ts, b, "shared-name") // same name, different user: 201
	var listB struct {
		Total int64 `json:"total"`
		Data  []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/tags", nil, b), &listB)
	if listB.Total != 1 {
		t.Fatalf("tag bleed between users: %+v", listB)
	}
	// foreign id: B's tag targeted by A -> 404
	bID := listB.Data[0].ID
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+fmtInt64(bID), map[string]any{"name": "steal"}, a); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch foreign tag = %d, want 404", resp.StatusCode)
	}
	if resp := doJSON(t, ts, "DELETE", "/api/v1/tags/"+fmtInt64(bID), nil, a); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete foreign tag = %d, want 404", resp.StatusCode)
	}
}
