package api

import (
	"net/http"
	"testing"
)

// TestProjectComments: the public comments thread on shared projects. Owner
// can always comment (solo thread even on a private project); any user can
// comment on a shared project; deletion is author-or-project-owner, everyone
// else 404s (no existence leak). Thread reads oldest-first with display-name
// authors and a per-viewer `mine` flag.
func TestProjectComments(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pc1@test.dev", "longpassword1")
	alice := registerUser(t, ts, "pc2@test.dev", "longpassword1")
	bob := registerUser(t, ts, "pc3@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "C", "visibility": "shared"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// alice + owner comment; bob reads
	mk := func(user *http.Cookie, body string) int64 {
		r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/comments", map[string]any{"body": body}, user)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("comment by = %d", r.StatusCode)
		}
		var c struct {
			ID int64 `json:"id"`
		}
		decodeBody(t, r, &c)
		return c.ID
	}
	ca := mk(alice, "nice build")
	mk(owner, "thanks")

	// thread visible to sharee, oldest first, author display names, mine flag
	var detail struct {
		Comments []struct {
			Author string `json:"author"`
			Mine   bool   `json:"mine"`
			Body   string `json:"body"`
		} `json:"comments"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, bob), &detail)
	if len(detail.Comments) != 2 || detail.Comments[0].Body != "nice build" {
		t.Fatalf("comments = %+v", detail.Comments)
	}

	// deletion matrix: bob cannot delete alice's; alice deletes own; owner deletes any
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/comments/"+fmtInt64(ca), nil, bob); r.StatusCode != http.StatusNotFound {
		t.Fatalf("bob delete = %d, want 404", r.StatusCode)
	}
	ca2 := mk(alice, "again")
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/comments/"+fmtInt64(ca2), nil, alice); r.StatusCode != http.StatusNoContent {
		t.Fatalf("author delete = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/comments/"+fmtInt64(ca), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("owner delete = %d", r.StatusCode)
	}

	// private project: sharee cannot even POST (404 via requireViewableProject)
	resp = doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Solo"}, owner)
	decodeBody(t, resp, &proj)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/comments", map[string]any{"body": "hi"}, alice); r.StatusCode != http.StatusNotFound {
		t.Fatalf("private comment = %d, want 404", r.StatusCode)
	}
	// owner CAN comment on own private project (solo thread)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/comments", map[string]any{"body": "note to self"}, owner); r.StatusCode != http.StatusCreated {
		t.Fatalf("owner private comment = %d", r.StatusCode)
	}
}
