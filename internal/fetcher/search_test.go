package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// braveStub answers like api.search.brave.com with the given result rows
// (title, url pairs). status overrides the response code (0 = 200).
func braveStub(t *testing.T, status int, rows ...[2]string) *httptest.Server {
	t.Helper()
	type row struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	results := make([]row, 0, len(rows))
	for _, r := range rows {
		results = append(results, row{Title: r[0], URL: r[1]})
	}
	body := fmt.Sprintf(`{"web":{"results":[`)
	for i, r := range results {
		if i > 0 {
			body += ","
		}
		body += fmt.Sprintf(`{"title":%q,"url":%q,"description":"d"}`, r.Title, r.URL)
	}
	body += `]}}`
	if status == 0 {
		status = http.StatusOK
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func TestBraveSearchParsesResults(t *testing.T) {
	srv := braveStub(t, 0,
		[2]string{"LM358 datasheet", "https://www.ti.com/lit/ds/symlink/lm358.pdf"},
		[2]string{"LM358 page", "https://example.com/lm358"},
	)
	defer srv.Close()
	s := &BraveSearcher{Key: "k", baseURL: srv.URL}
	cands, err := s.Search(context.Background(), "lm358 datasheet")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("len = %d, want 2", len(cands))
	}
	if cands[0].Title != "LM358 datasheet" || cands[0].URL != "https://www.ti.com/lit/ds/symlink/lm358.pdf" {
		t.Errorf("cands[0] = %+v", cands[0])
	}
	if cands[0].Host != "www.ti.com" {
		t.Errorf("host = %q, want www.ti.com", cands[0].Host)
	}
}

func TestBraveSearchSendsKeyAndQuery(t *testing.T) {
	var gotPath, gotKey, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		gotKey = r.Header.Get("X-Subscription-Token")
		gotAccept = r.Header.Get("Accept")
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()
	s := &BraveSearcher{Key: "sekret", baseURL: srv.URL + "/res/v1/web/search"}
	if _, err := s.Search(context.Background(), "lm358 datasheet & more"); err != nil {
		t.Fatal(err)
	}
	if gotKey != "sekret" {
		t.Errorf("X-Subscription-Token = %q", gotKey)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotPath != `/res/v1/web/search?q=lm358+datasheet+%26+more&count=10` {
		t.Errorf("query = %q", gotPath)
	}
}

func TestBraveSearchRanksPDFFirst(t *testing.T) {
	srv := braveStub(t, 0,
		[2]string{"page", "https://a.example.com/product"},
		[2]string{"pdf2", "https://b.example.com/ds.pdf"},
		[2]string{"page2", "https://c.example.com/x"},
		[2]string{"PDF-upper", "https://d.example.com/DS.PDF"},
		[2]string{"pdf-query-only", "https://e.example.com/file?type=.pdf"},
	)
	defer srv.Close()
	s := &BraveSearcher{Key: "k", baseURL: srv.URL}
	cands, err := s.Search(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	// .pdf-SUFFIX urls first (path suffix, before any query), stable within
	// each group; a ".pdf" only in the query string does not count.
	var got []string
	for _, c := range cands {
		got = append(got, c.Title)
	}
	want := []string{"pdf2", "PDF-upper", "page", "page2", "pdf-query-only"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestBraveSearchKeepsHTTPOnlyAndCaps10(t *testing.T) {
	rows := [][2]string{{"ftp", "ftp://x.example.com/a.pdf"}, {"no-scheme", "example.com/b.pdf"}}
	for i := 0; i < 14; i++ {
		rows = append(rows, [2]string{fmt.Sprintf("r%d", i), fmt.Sprintf("https://h%d.example.com/a.pdf", i)})
	}
	srv := braveStub(t, 0, rows...)
	defer srv.Close()
	s := &BraveSearcher{Key: "k", baseURL: srv.URL}
	cands, err := s.Search(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 10 {
		t.Fatalf("len = %d, want 10 (cap)", len(cands))
	}
	for _, c := range cands {
		if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
			t.Errorf("non-http candidate kept: %q", c.URL)
		}
	}
}

func TestBraveSearchKeyRejection(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, 422} {
		srv := braveStub(t, status)
		s := &BraveSearcher{Key: "bad", baseURL: srv.URL}
		_, err := s.Search(context.Background(), "q")
		srv.Close()
		if err == nil {
			t.Errorf("status %d: no error", status)
		} else if !strings.Contains(err.Error(), "Brave API rejected the key") {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
}

func TestBraveSearchTransportError(t *testing.T) {
	srv := braveStub(t, 0)
	base := srv.URL
	srv.Close() // dead port → transport error
	s := &BraveSearcher{Key: "k", baseURL: base}
	_, err := s.Search(context.Background(), "q")
	if err == nil || strings.Contains(err.Error(), "rejected the key") {
		t.Errorf("err = %v, want wrapped transport error", err)
	}
}

func TestBraveSearchEmptyWebKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	s := &BraveSearcher{Key: "k", baseURL: srv.URL}
	cands, err := s.Search(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Errorf("len = %d, want 0", len(cands))
	}
}

// ---- Verify: server-side PDF check through the full policy path ----

func TestVerifyPDFWithContentRange(t *testing.T) {
	var sawRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRange = r.Header.Get("Range")
		if sawRange != "" {
			w.Header().Set("Content-Range", "bytes 0-511/12345")
			w.WriteHeader(http.StatusPartialContent)
		}
		w.Write([]byte("%PDF-1.4 rest of the file..."))
	}))
	defer srv.Close()
	ok, size, err := NewDirectEngine(LoopbackPolicy()).Verify(context.Background(), srv.URL+"/ds.pdf")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if sawRange != "bytes=0-511" {
		t.Errorf("Range header = %q", sawRange)
	}
	if size != 12345 {
		t.Errorf("size = %d, want 12345 from Content-Range total", size)
	}
}

func TestVerifyHTMLOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html><body>not a pdf</body></html>"))
	}))
	defer srv.Close()
	ok, _, err := NewDirectEngine(LoopbackPolicy()).Verify(context.Background(), srv.URL)
	if ok {
		t.Fatal("html verified as PDF")
	}
	if err == nil {
		t.Fatal("expected a reason in err")
	}
}

func TestVerifySizeUnknownWithoutContentRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("%PDF-1.4 no range support"))
	}))
	defer srv.Close()
	ok, size, err := NewDirectEngine(LoopbackPolicy()).Verify(context.Background(), srv.URL)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if size != -1 {
		t.Errorf("size = %d, want -1 (unknown)", size)
	}
}

func TestVerify403RangeRetriesWithoutHeader(t *testing.T) {
	// Some servers 403 the Range request but serve plain GETs: the retry
	// without the header must go through the same pinned dial.
	var ranges, plains int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranges++
			w.WriteHeader(http.StatusForbidden)
			return
		}
		plains++
		w.Write([]byte("%PDF-1.4 served without range"))
	}))
	defer srv.Close()
	ok, size, err := NewDirectEngine(LoopbackPolicy()).Verify(context.Background(), srv.URL)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ranges != 1 || plains != 1 {
		t.Errorf("ranges=%d plains=%d, want 1/1", ranges, plains)
	}
	if size != -1 {
		t.Errorf("size = %d, want -1", size)
	}
}

func TestVerifyRejectsPrivateIPByPolicy(t *testing.T) {
	// PublicPolicy forbids loopback: the literal private IP must be rejected
	// before any dial (no server is even listening there).
	ok, _, err := NewDirectEngine(PublicPolicy()).Verify(context.Background(), "http://127.0.0.1:1/ds.pdf")
	if ok {
		t.Fatal("private address verified")
	}
	if err == nil {
		t.Fatal("expected policy error")
	}
}

func TestVerifyFollowsRedirects(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-511/77")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("%PDF-1.4 after redirect"))
	}))
	defer final.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/real.pdf", http.StatusFound)
	}))
	defer first.Close()
	ok, size, err := NewDirectEngine(LoopbackPolicy()).Verify(context.Background(), first.URL)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if size != 77 {
		t.Errorf("size = %d, want 77", size)
	}
}

func TestVerifyNonPDFStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	ok, _, err := NewDirectEngine(LoopbackPolicy()).Verify(context.Background(), srv.URL)
	if ok {
		t.Error("404 verified")
	}
	if err == nil {
		t.Error("expected reason for non-200/206 status")
	}
}
