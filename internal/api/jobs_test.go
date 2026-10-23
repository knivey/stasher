package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"stasher/internal/config"
	"stasher/internal/db"
	"stasher/internal/files"
)

// servePDF is an httptest target handing out a small PDF at any path.
func servePDF(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 test datasheet body"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startFetchJob submits POST /items/{id}/fetch and returns the job id.
func startFetchJob(t *testing.T, ts *httptest.Server, cookie *http.Cookie, itemID int64, rawURL, mode string) string {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(itemID)+"/fetch",
		map[string]any{"url": rawURL, "mode": mode}, cookie)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("fetch submit = %d, want 202", resp.StatusCode)
	}
	var body struct {
		JobID string `json:"job_id"`
	}
	decodeBody(t, resp, &body)
	if body.JobID == "" {
		t.Fatal("empty job_id")
	}
	return body.JobID
}

type jobStatus struct {
	Status     string `json:"status"`
	Error      string `json:"error"`
	DocumentID int64  `json:"document_id"`
}

// waitJob polls GET /jobs/{id} until the job leaves "running" (~5s, 50ms).
func waitJob(t *testing.T, ts *httptest.Server, cookie *http.Cookie, id string) jobStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp := doJSON(t, ts, "GET", "/api/v1/jobs/"+id, nil, cookie)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET job = %d", resp.StatusCode)
		}
		var j jobStatus
		decodeBody(t, resp, &j)
		if j.Status != "running" {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatal("job still running after 5s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestFetchJobDirect(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "fetch-direct@test.dev", "longpassword1")
	target := servePDF(t)

	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "LM358"}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)

	job := startFetchJob(t, ts, cookie, item.ID, target.URL+"/datasheets/lm358.pdf", "direct")
	done := waitJob(t, ts, cookie, job)
	if done.Status != "done" || done.DocumentID == 0 {
		t.Fatalf("job = %+v, want done with document_id", done)
	}

	// a done job carries no "error" key (empty fields are omitted)
	raw := doJSON(t, ts, "GET", "/api/v1/jobs/"+job, nil, cookie)
	var m map[string]any
	decodeBody(t, raw, &m)
	if _, has := m["error"]; has {
		t.Fatalf("done job carries error key: %v", m)
	}

	// the document is an ordinary item document
	var detail struct {
		Documents []struct {
			ID        int64  `json:"id"`
			Filename  string `json:"filename"`
			MimeType  string `json:"mime_type"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"documents"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(item.ID), nil, cookie), &detail)
	if len(detail.Documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(detail.Documents))
	}
	doc := detail.Documents[0]
	if doc.ID != done.DocumentID {
		t.Fatalf("document id = %d, want %d", doc.ID, done.DocumentID)
	}
	if doc.Filename != "lm358.pdf" {
		t.Fatalf("filename = %q, want lm358.pdf", doc.Filename)
	}
	if doc.MimeType != "application/pdf" || doc.SizeBytes <= 0 {
		t.Fatalf("mime/size = %q/%d", doc.MimeType, doc.SizeBytes)
	}
}

func TestFetchJobFailureKeepsItemClean(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "fetch-fail@test.dev", "longpassword1")
	target := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(target.Close)

	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "doomed fetch"}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)

	job := startFetchJob(t, ts, cookie, item.ID, target.URL+"/missing.pdf", "direct")
	done := waitJob(t, ts, cookie, job)
	if done.Status != "failed" {
		t.Fatalf("job status = %q, want failed", done.Status)
	}
	if done.Error == "" {
		t.Fatal("failed job has empty error")
	}
	if done.DocumentID != 0 {
		t.Fatalf("failed job has document_id %d", done.DocumentID)
	}
	if !strings.Contains(done.Error, "404") {
		t.Fatalf("error = %q, want the upstream status", done.Error)
	}

	var detail struct {
		Documents []map[string]any `json:"documents"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(item.ID), nil, cookie), &detail)
	if len(detail.Documents) != 0 {
		t.Fatalf("failed fetch left %d documents behind", len(detail.Documents))
	}
}

func TestFetchJobArchiveStub(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "fetch-archive@test.dev", "longpassword1")
	target := servePDF(t) // content irrelevant: chrome renders the page

	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "archived page"}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)

	job := startFetchJob(t, ts, cookie, item.ID, target.URL+"/product/page", "archive")
	done := waitJob(t, ts, cookie, job)
	if done.Status != "done" {
		t.Fatalf("archive job = %+v, want done", done)
	}
	var detail struct {
		Documents []struct {
			Filename string `json:"filename"`
			MimeType string `json:"mime_type"`
		} `json:"documents"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(item.ID), nil, cookie), &detail)
	if len(detail.Documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(detail.Documents))
	}
	doc := detail.Documents[0]
	// httptest host is 127.0.0.1 (no port in Hostname()); "<host> <UTC date>.pdf"
	name := regexp.MustCompile(`^127\.0\.0\.1 \d{4}-\d{2}-\d{2}\.pdf$`)
	if !name.MatchString(doc.Filename) {
		t.Fatalf("filename = %q, want \"127.0.0.1 <YYYY-MM-DD>.pdf\"", doc.Filename)
	}
	if doc.MimeType != "application/pdf" {
		t.Fatalf("mime = %q, want application/pdf", doc.MimeType)
	}
}

func TestFetchJobValidation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "fetch-valid@test.dev", "longpassword1")
	bob := registerUser(t, ts, "fetch-valid2@test.dev", "longpassword1")
	target := servePDF(t)

	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "victim"}, alice)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)
	path := "/api/v1/items/" + fmtInt64(item.ID) + "/fetch"

	cases := []struct {
		name   string
		cookie *http.Cookie
		url    string
		mode   string
		want   int
	}{
		{"bad url", alice, "not a url", "direct", http.StatusBadRequest},
		{"ftp scheme", alice, "ftp://example.com/ds.pdf", "direct", http.StatusBadRequest},
		{"2001 chars", alice, "https://example.com/" + strings.Repeat("a", 1981), "direct", http.StatusBadRequest},
		{"empty url", alice, "", "direct", http.StatusBadRequest},
		{"bad mode", alice, target.URL + "/ds.pdf", "wget", http.StatusBadRequest},
		{"empty mode", alice, target.URL + "/ds.pdf", "", http.StatusBadRequest},
		{"foreign item", bob, target.URL + "/ds.pdf", "direct", http.StatusNotFound},
	}
	for _, tc := range cases {
		r := doJSON(t, ts, "POST", path, map[string]any{"url": tc.url, "mode": tc.mode}, tc.cookie)
		if r.StatusCode != tc.want {
			t.Fatalf("%s: = %d, want %d", tc.name, r.StatusCode, tc.want)
		}
	}
	// absent item 404s like every other owned-item route
	if r := doJSON(t, ts, "POST", "/api/v1/items/999999/fetch",
		map[string]any{"url": target.URL + "/x.pdf", "mode": "direct"}, alice); r.StatusCode != http.StatusNotFound {
		t.Fatalf("absent item = %d, want 404", r.StatusCode)
	}
}

func TestFetchJobOwnerScoped(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "job-alice@test.dev", "longpassword1")
	bob := registerUser(t, ts, "job-bob@test.dev", "longpassword1")
	target := servePDF(t)

	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "alice item"}, alice)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)

	job := startFetchJob(t, ts, alice, item.ID, target.URL+"/secret.pdf", "direct")
	// bob must not see (or even confirm the existence of) alice's job
	if r := doJSON(t, ts, "GET", "/api/v1/jobs/"+job, nil, bob); r.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign job GET = %d, want 404", r.StatusCode)
	}
	if done := waitJob(t, ts, alice, job); done.Status != "done" {
		t.Fatalf("alice's own job = %+v", done)
	}
	// unknown / garbage ids 404 too
	for _, id := range []string{"nope", "AAAA"} {
		if r := doJSON(t, ts, "GET", "/api/v1/jobs/"+id, nil, alice); r.StatusCode != http.StatusNotFound {
			t.Fatalf("job id %q = %d, want 404", id, r.StatusCode)
		}
	}
}

// TestFetchDisabledWhenUnwired: a Server constructed without engines (both
// nil) answers 503 fetching_disabled instead of queueing a job that can
// never run. main always wires both; this pins the nil-safety.
func TestFetchDisabledWhenUnwired(t *testing.T) {
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
	s := New(&config.Config{DataDir: dir}, sqlDB, st, nil, nil)
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	cookie := registerUser(t, ts, "unwired@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "no fetcher"}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)

	r := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(item.ID)+"/fetch",
		map[string]any{"url": "https://example.com/x.pdf", "mode": "direct"}, cookie)
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unwired fetch = %d, want 503", r.StatusCode)
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

// TestFetchJobsSingleFlight: only one fetch runs at a time server-wide. The
// slow target's handler proves job1 holds the semaphore (it signals when
// entered and records its completion time); job2's target must not be dialed
// until job1's target has FINISHED.
func TestFetchJobsSingleFlight(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "single-flight@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "queued"}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)

	started := make(chan struct{})
	release := make(chan struct{})
	var slowFinishedAt, fastDialedAt atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started) // entered: the semaphore is now held
		<-release
		slowFinishedAt.Store(time.Now().UnixNano())
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("slow body"))
	}))
	t.Cleanup(slow.Close)
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fastDialedAt.Store(time.Now().UnixNano())
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("fast body"))
	}))
	t.Cleanup(fast.Close)

	job1 := startFetchJob(t, ts, cookie, item.ID, slow.URL+"/slow.txt", "direct")
	select {
	case <-started: // job1 is mid-fetch, holding the semaphore
	case <-time.After(5 * time.Second):
		t.Fatal("job1 never started fetching")
	}

	job2 := startFetchJob(t, ts, cookie, item.ID, fast.URL+"/fast.txt", "direct")
	close(release) // let job1 finish; job2 may proceed only after that
	if j := waitJob(t, ts, cookie, job1); j.Status != "done" {
		t.Fatalf("job1 = %+v", j)
	}
	if j := waitJob(t, ts, cookie, job2); j.Status != "done" {
		t.Fatalf("job2 = %+v", j)
	}
	if fastDialedAt.Load() < slowFinishedAt.Load() {
		t.Fatal("second job fetched before the first finished — no single-flight")
	}
}
