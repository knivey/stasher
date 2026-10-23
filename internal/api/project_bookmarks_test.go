package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectBookmarksCRUD(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pb1@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "pb2@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "B", "visibility": "shared"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// local page fixture: /page carries the <title>, /docs anything
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs" {
			fmt.Fprint(w, "<html><body>docs</body></html>")
			return
		}
		fmt.Fprint(w, "<html><head><title>  LM358&nbsp;Datasheet </title></head><body>x</body></html>")
	}))
	defer srv.Close()

	// add with explicit title
	r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": srv.URL + "/docs", "title": "Docs"}, owner)
	var bm struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("add bookmark = %d", r.StatusCode)
	}
	decodeBody(t, r, &bm)
	if bm.Title != "Docs" {
		t.Fatalf("title = %q", bm.Title)
	}

	// add without title → auto-title from <title> (engine + LoopbackPolicy)
	r = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": srv.URL + "/page"}, owner)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("add auto-title bookmark = %d", r.StatusCode)
	}
	decodeBody(t, r, &bm)
	if bm.Title != "LM358 Datasheet" { // the fixture page's <title>
		t.Fatalf("auto title = %q", bm.Title)
	}

	// invalid urls → 400
	for _, bad := range []string{"", "javascript:alert(1)", "ftp://x/y", strings.Repeat("https://a/", 500)} {
		if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks", map[string]any{"url": bad}, owner); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad url %q = %d", bad, r.StatusCode)
		}
	}

	// PATCH title + absent-keeps
	r = doJSON(t, ts, "PATCH", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID), map[string]any{"title": "Renamed"}, owner)
	decodeBody(t, r, &bm)
	if bm.Title != "Renamed" {
		t.Fatalf("patched title = %q", bm.Title)
	}

	// sharee mutations 404
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks", map[string]any{"url": "https://x.test/"}, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee add bookmark = %d", r.StatusCode)
	}

	// detail carries bookmarks for sharee too (archived_file null for now)
	var detail map[string]any
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, sharee), &detail)
	if bms := detail["bookmarks"].([]any); len(bms) != 2 {
		t.Fatalf("sharee bookmarks = %d, want 2", len(bms))
	}

	// delete → 204 then 404
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete bookmark = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID), nil, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("double delete = %d", r.StatusCode)
	}
}
