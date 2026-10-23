package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestProjectNotes: the owner-only timestamped notes log. Add is 201 with a
// trimmed body (1-2000 after trim); the owner's project detail carries
// `notes_log` newest-first; sharees never see the key and can neither add
// nor delete notes (owner-only → 404, no existence leak). Delete is
// 204-then-404 via execrows.
func TestProjectNotes(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pnotes@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "pnotes2@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "N", "visibility": "shared"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// add: 201, trim + cap
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/notes", map[string]any{"body": "  first note  "}, owner); r.StatusCode != http.StatusCreated {
		t.Fatalf("add note = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/notes", map[string]any{"body": ""}, owner); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty note = %d, want 400", r.StatusCode)
	}
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/notes", map[string]any{"body": strings.Repeat("x", 2001)}, owner); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("long note = %d, want 400", r.StatusCode)
	}

	// owner detail carries notes_log (trimmed body)
	var detail map[string]any
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, owner), &detail)
	log0 := detail["notes_log"].([]any)[0].(map[string]any)
	if log0["body"] != "first note" {
		t.Fatalf("body = %q, want trimmed", log0["body"])
	}

	// sharee detail OMITS the key entirely (fresh map: json.Decode MERGES
	// into a non-nil map, so reusing the owner's would keep notes_log set)
	var shareeDetail map[string]any
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, sharee), &shareeDetail)
	if _, has := shareeDetail["notes_log"]; has {
		t.Fatal("sharee sees notes_log")
	}

	// sharee cannot add/delete notes even on a shared project (owner-only → 404)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/notes", map[string]any{"body": "x"}, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee add note = %d, want 404", r.StatusCode)
	}

	// delete: 204 then 404 on repeat (execrows)
	id := int64(log0["id"].(float64))
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/notes/"+fmtInt64(id), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete note = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/notes/"+fmtInt64(id), nil, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("double delete = %d, want 404", r.StatusCode)
	}
}
