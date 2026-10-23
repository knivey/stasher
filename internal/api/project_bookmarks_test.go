package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"stasher/internal/config"
	"stasher/internal/db"
	"stasher/internal/files"
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

	// control-only explicit title clamps to "" → treated as absent →
	// auto-title path (not stored as empty)
	r = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": srv.URL + "/page", "title": "\x07"}, owner)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("add control-only title bookmark = %d", r.StatusCode)
	}
	decodeBody(t, r, &bm)
	if bm.Title != "LM358 Datasheet" {
		t.Fatalf("control-only title → auto title = %q, want LM358 Datasheet", bm.Title)
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
	if bms := detail["bookmarks"].([]any); len(bms) != 3 {
		t.Fatalf("sharee bookmarks = %d, want 3", len(bms))
	}

	// delete → 204 then 404
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete bookmark = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID), nil, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("double delete = %d", r.StatusCode)
	}
}

// --- Opt-in bookmark archiving (generalized fetch jobs) ---

// serveArchiveFailure is an httptest target whose URL makes the (stubbed)
// archive engine fail: the fetcher suite's stub-chrome.sh emits an over-cap
// (26MB) PDF when the URL carries ?emit_big=1, which the engine rejects
// against its 25MB output cap (same fixture mechanism archive_test.go uses).
// The target body itself is irrelevant — the stub never reads it.
func serveArchiveFailure(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	return srv
}

// Titles flow into stored rows AND archived-PDF filenames
// (Content-Disposition): control characters are stripped and the 200 cap
// counts runes, never splitting a multi-byte character.
func TestBookmarkTitleControlStripAndRuneCap(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pb10@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "T"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	bmURL := "/api/v1/projects/" + fmtInt64(proj.ID) + "/bookmarks"

	// explicit title with control characters (newline, tab, BEL)
	r := doJSON(t, ts, "POST", bmURL,
		map[string]any{"url": "https://example.com/a", "title": "Li\nne1\tX\x07Y"}, owner)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("add = %d, want 201", r.StatusCode)
	}
	var bm struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	decodeBody(t, r, &bm)
	if bm.Title != "Line1XY" {
		t.Fatalf("title = %q, want Line1XY", bm.Title)
	}

	// 201-rune CJK title (603 bytes) truncates to 200 runes, no U+FFFD tail
	r = doJSON(t, ts, "POST", bmURL,
		map[string]any{"url": "https://example.com/b", "title": strings.Repeat("漢", 201)}, owner)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("add cjk = %d, want 201", r.StatusCode)
	}
	decodeBody(t, r, &bm)
	if n := utf8.RuneCountInString(bm.Title); n != 200 {
		t.Fatalf("cjk title runes = %d, want 200", n)
	}
	if strings.ContainsRune(bm.Title, '\uFFFD') {
		t.Fatal("cjk title truncation split a rune (U+FFFD present)")
	}

	// PATCH path runs through the same clamp
	r = doJSON(t, ts, "PATCH", bmURL+"/"+fmtInt64(bm.ID),
		map[string]any{"title": "P\x07Q\nR"}, owner)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("patch = %d, want 200", r.StatusCode)
	}
	decodeBody(t, r, &bm)
	if bm.Title != "PQR" {
		t.Fatalf("patched title = %q, want PQR", bm.Title)
	}

	// PATCH with a control-only title clamps to "" → keeps the current
	// title (empty-name sentinel semantics)
	r = doJSON(t, ts, "PATCH", bmURL+"/"+fmtInt64(bm.ID),
		map[string]any{"title": "\x07\n"}, owner)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("patch control-only = %d, want 200", r.StatusCode)
	}
	decodeBody(t, r, &bm)
	if bm.Title != "PQR" {
		t.Fatalf("control-only patch title = %q, want unchanged PQR", bm.Title)
	}
}

func TestBookmarkArchiveJob(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "arch1@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "arch2@test.dev", "longpassword1")
	target := servePDF(t) // content irrelevant: the stub chrome renders the page

	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "A", "visibility": "shared"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)
	resp = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": target.URL, "title": "Build Log"}, owner)
	var bm struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &bm)

	// sharee cannot archive (owner-only even on shared projects)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee archive = %d, want 404", r.StatusCode)
	}

	// owner archives: 202 → poll → done → archived_file populated
	r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, owner)
	if r.StatusCode != http.StatusAccepted {
		t.Fatalf("archive = %d", r.StatusCode)
	}
	var job struct {
		JobID string `json:"job_id"`
	}
	decodeBody(t, r, &job)
	if done := waitJob(t, ts, owner, job.JobID); done.Status != "done" {
		t.Fatalf("archive job = %+v, want done", done)
	}

	var detail struct {
		Bookmarks []struct {
			Title        string `json:"title"`
			ArchivedFile *struct {
				Filename   string `json:"filename"`
				MimeType   string `json:"mime_type"`
				StorageKey string `json:"storage_key"`
			} `json:"archived_file"`
		} `json:"bookmarks"`
		Files []any `json:"files"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, owner), &detail)
	if len(detail.Bookmarks) != 1 || detail.Bookmarks[0].ArchivedFile == nil {
		t.Fatalf("archived_file missing: %+v", detail.Bookmarks)
	}
	af := detail.Bookmarks[0].ArchivedFile
	if af.Filename != "Build Log.pdf" || af.MimeType != "application/pdf" {
		t.Fatalf("archived file = %+v", af)
	}
	// the archive is an ordinary project file — it shows in materials
	if len(detail.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(detail.Files))
	}
	oldKey := af.StorageKey

	// re-archive: a NEW row links, the OLD bytes are gone
	r = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, owner)
	if r.StatusCode != http.StatusAccepted {
		t.Fatalf("re-archive = %d", r.StatusCode)
	}
	var job2 struct {
		JobID string `json:"job_id"`
	}
	decodeBody(t, r, &job2)
	if done := waitJob(t, ts, owner, job2.JobID); done.Status != "done" {
		t.Fatalf("re-archive job = %+v, want done", done)
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, owner), &detail)
	newKey := detail.Bookmarks[0].ArchivedFile.StorageKey
	if newKey == oldKey {
		t.Fatalf("re-archive reused key %q", newKey)
	}
	if len(detail.Files) != 1 {
		t.Fatalf("files after re-archive = %d, want 1 (old row retired)", len(detail.Files))
	}
	if r := getURL(t, ts, oldKey, owner); r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("old archive still served: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
}

func TestBookmarkArchiveFailureLeavesClean(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "arch3@test.dev", "longpassword1")
	// a target the (stubbed) archive engine fails on: ?emit_big=1 trips the
	// stub's over-cap emission (see serveArchiveFailure above)
	target := serveArchiveFailure(t)
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "AF"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)
	resp = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": target.URL + "/page?emit_big=1", "title": "Broken"}, owner)
	var bm struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &bm)

	r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, owner)
	if r.StatusCode != http.StatusAccepted {
		t.Fatalf("archive = %d", r.StatusCode)
	}
	var job struct {
		JobID string `json:"job_id"`
	}
	decodeBody(t, r, &job)
	jobStatus := waitJob(t, ts, owner, job.JobID)
	if jobStatus.Status != "failed" {
		t.Fatalf("job = %+v, want failed", jobStatus)
	}

	// bookmark untouched: archived_file stays null, files list stays empty
	var detail struct {
		Bookmarks []struct {
			ArchivedFile any `json:"archived_file"`
		} `json:"bookmarks"`
		Files []any `json:"files"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, owner), &detail)
	if len(detail.Bookmarks) != 1 || detail.Bookmarks[0].ArchivedFile != nil {
		t.Fatalf("bookmark after failed archive: %+v", detail.Bookmarks)
	}
	if len(detail.Files) != 0 {
		t.Fatalf("orphan files after failed archive: %d", len(detail.Files))
	}
}

// TestBookmarkArchiveDisabledWhenUnwired: without the archive engine the
// endpoint answers 503 fetching_disabled instead of queueing a job that can
// never run (mirrors TestFetchDisabledWhenUnwired for the item route).
func TestBookmarkArchiveDisabledWhenUnwired(t *testing.T) {
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}
	st, err := files.New(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(&config.Config{DataDir: dir}, sqlDB, st, nil, nil, nil, nil)
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	cookie := registerUser(t, ts, "unwired-arch@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "U"}, cookie)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)
	resp = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": "https://example.com/x"}, cookie)
	var bm struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &bm)

	r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, cookie)
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unwired archive = %d, want 503", r.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, r, &body)
	if body.Error.Code != "fetching_disabled" {
		t.Fatalf("code = %q, want fetching_disabled", body.Error.Code)
	}
}
