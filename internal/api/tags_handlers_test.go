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

// Ownership/existence must be checked BEFORE name-collision: a foreign or
// absent id is 404 not_found even when the requested name collides with one
// of the caller's own tags (the old check-then-act order answered 409 first).
func TestTagRenameOwnershipBeforeCollision(t *testing.T) {
	_, ts := newTestServer(t, nil)
	a := registerUser(t, ts, "tag-own@test.dev", "longpassword1")
	b := registerUser(t, ts, "tag-for@test.dev", "longpassword1")
	createTag(t, ts, a, "alpha")
	createTag(t, ts, a, "beta")
	createTag(t, ts, b, "gamma")
	var listB struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/tags", nil, b), &listB)
	if len(listB.Data) != 1 {
		t.Fatalf("b tags = %d, want 1", len(listB.Data))
	}
	foreign := fmtInt64(listB.Data[0].ID)

	// foreign id + colliding name -> 404 not_found (never 409)
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+foreign, map[string]any{"name": "alpha"}, a); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign id + colliding name = %d, want 404", resp.StatusCode)
	}
	// foreign id + free name -> 404
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+foreign, map[string]any{"name": "free"}, a); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign id + free name = %d, want 404", resp.StatusCode)
	}

	var listA struct {
		Data []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/tags", nil, a), &listA)
	if len(listA.Data) != 2 {
		t.Fatalf("a tags = %d, want 2", len(listA.Data))
	}
	var alphaID int64
	for _, tg := range listA.Data {
		if tg.Name == "alpha" {
			alphaID = tg.ID
		}
	}
	// own id + colliding name -> 409 tag_exists
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+fmtInt64(alphaID), map[string]any{"name": "beta"}, a)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("own id + colliding name = %d, want 409", resp.StatusCode)
	}
	decodeBody(t, resp, &errBody)
	if errBody.Error.Code != "tag_exists" {
		t.Fatalf("collision code = %q, want tag_exists", errBody.Error.Code)
	}
	// rename to the tag's own current name stays a no-op 200 (UNIQUE does
	// not flag the row against itself)
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+fmtInt64(alphaID), map[string]any{"name": "alpha"}, a); resp.StatusCode != http.StatusOK {
		t.Fatalf("same-name rename = %d, want 200", resp.StatusCode)
	}
}

// Create has no check-then-insert window: the UNIQUE(user_id, name)
// constraint answers 409 duplicate_tag directly, so a duplicate create can
// never surface as 500 under a concurrent insert.
func TestTagCreateDuplicateIs409DuplicateTag(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "tag-race@test.dev", "longpassword1")
	createTag(t, ts, cookie, "dup")
	resp := doJSON(t, ts, "POST", "/api/v1/tags", map[string]any{"name": "dup"}, cookie)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "duplicate_tag" {
		t.Fatalf("error code = %q, want duplicate_tag", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Fatal("empty error message")
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
