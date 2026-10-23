package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stasher/internal/fetcher"
)

// fakeSearcher returns canned candidates without touching the network.
type fakeSearcher struct {
	cands []fetcher.SearchCandidate
	err   error
}

func (f *fakeSearcher) Search(context.Context, string) ([]fetcher.SearchCandidate, error) {
	return f.cands, f.err
}

// serveRangedPDF hands out a PDF; when the request carries a Range header it
// answers 206 with a Content-Range total, else a plain 200.
func serveRangedPDF(t *testing.T, total int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes 0-511/"+fmtInt64(total))
			w.WriteHeader(http.StatusPartialContent)
		}
		w.Write([]byte("%PDF-1.4 ranged datasheet"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// serveHTML is a candidate that will NOT verify as a PDF.
func serveHTML(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html><body>a product page</body></html>"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// createSearchItem registers a user, creates an item, returns both.
func createSearchItem(t *testing.T, ts *httptest.Server, email string) (*http.Cookie, int64) {
	t.Helper()
	cookie := registerUser(t, ts, email, "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "LM358"}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("item create = %d", resp.StatusCode)
	}
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)
	return cookie, item.ID
}

func TestSearchDocsHappyPath(t *testing.T) {
	s, ts := newTestServer(t, nil)
	pdfA := serveRangedPDF(t, 5000)
	pdfB := servePDF(t)
	html := serveHTML(t)
	s.searcher = &fakeSearcher{cands: []fetcher.SearchCandidate{
		{Title: "LM358 datasheet", URL: pdfA.URL + "/lm358.pdf", Host: hostOf(pdfA.URL)},
		{Title: "second source", URL: pdfB.URL + "/alt.pdf", Host: hostOf(pdfB.URL)},
		{Title: "product page", URL: html.URL + "/lm358", Host: hostOf(html.URL)},
	}}
	cookie, id := createSearchItem(t, ts, "search-happy@test.dev")

	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/search-documents",
		map[string]any{"query": "lm358 datasheet"}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Host  string `json:"host"`
			Size  *int64 `json:"size"`
		} `json:"results"`
		Rejected int `json:"rejected"`
	}
	decodeBody(t, resp, &body)
	if len(body.Results) != 2 {
		t.Fatalf("results = %d, want 2 (only verified PDFs)", len(body.Results))
	}
	if body.Rejected != 1 {
		t.Errorf("rejected = %d, want 1", body.Rejected)
	}
	r0 := body.Results[0]
	if r0.Title != "LM358 datasheet" || r0.Host != hostOf(pdfA.URL) {
		t.Errorf("results[0] = %+v", r0)
	}
	if r0.Size == nil || *r0.Size != 5000 {
		t.Errorf("results[0].size = %v, want 5000 (Content-Range total)", r0.Size)
	}
	if body.Results[1].URL != pdfB.URL+"/alt.pdf" {
		t.Errorf("results[1].url = %q (ranked order kept)", body.Results[1].URL)
	}
}

func TestSearchDocsSizeOmittedWhenUnknown(t *testing.T) {
	s, ts := newTestServer(t, nil)
	pdf := servePDF(t) // plain 200, no Content-Range
	s.searcher = &fakeSearcher{cands: []fetcher.SearchCandidate{
		{Title: "plain pdf", URL: pdf.URL + "/x.pdf", Host: hostOf(pdf.URL)},
	}}
	cookie, id := createSearchItem(t, ts, "search-size@test.dev")
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/search-documents",
		map[string]any{"query": "q"}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search = %d", resp.StatusCode)
	}
	var raw struct {
		Results []map[string]any `json:"results"`
	}
	decodeBody(t, resp, &raw)
	if len(raw.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(raw.Results))
	}
	if _, ok := raw.Results[0]["size"]; ok {
		t.Errorf("size key present for unknown size: %v", raw.Results[0])
	}
}

func TestSearchDocsDisabledWithoutKey(t *testing.T) {
	_, ts := newTestServer(t, nil) // searcher stays nil (no brave_api_key)
	cookie, id := createSearchItem(t, ts, "search-off@test.dev")
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/search-documents",
		map[string]any{"query": "q"}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("search = %d, want 400", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "search_disabled" {
		t.Errorf("code = %q, want search_disabled", body.Error.Code)
	}
	if body.Error.Message != "set brave_api_key in config to enable document search" {
		t.Errorf("message = %q", body.Error.Message)
	}
}

func TestSearchDocsValidatesQuery(t *testing.T) {
	s, ts := newTestServer(t, nil)
	s.searcher = &fakeSearcher{}
	cookie, id := createSearchItem(t, ts, "search-valid@test.dev")
	for name, q := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"over 200":   strings.Repeat("a", 201),
		// 201 runes but 402 bytes: must still 400 under rune counting
		"over 200 multibyte runes": strings.Repeat("é", 201),
	} {
		resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/search-documents",
			map[string]any{"query": q}, cookie)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
			continue
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		decodeBody(t, resp, &body)
		if body.Error.Code != "validation_error" {
			t.Errorf("%s: code = %q, want validation_error", name, body.Error.Code)
		}
	}

	// Exactly 200 runes passes even though it is 400 bytes: the limit
	// counts characters (runes), matching the error message and the
	// client-side maxlength semantics.
	exactly := strings.Repeat("é", 200)
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/search-documents",
		map[string]any{"query": exactly}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("200-rune query: status = %d, want 200", resp.StatusCode)
	}
}

func TestSearchDocsRateLimit(t *testing.T) {
	s, ts := newTestServer(t, nil)
	s.searcher = &fakeSearcher{} // zero candidates: fast responses
	cookie, id := createSearchItem(t, ts, "search-rate@test.dev")
	first := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/search-documents",
		map[string]any{"query": "q"}, cookie)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first search = %d, want 200", first.StatusCode)
	}
	second := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/search-documents",
		map[string]any{"query": "q"}, cookie)
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second search = %d, want 429", second.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, second, &body)
	if body.Error.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", body.Error.Code)
	}
}

func TestSearchDocsNilFetchDirect503(t *testing.T) {
	s, ts := newTestServer(t, nil)
	// Hand-built wiring: a searcher without the direct engine must 503,
	// not nil-panic inside the verification fan-out.
	s.fetchDirect = nil
	s.searcher = &fakeSearcher{cands: []fetcher.SearchCandidate{
		{Title: "x", URL: "https://example.com/x.pdf", Host: "example.com"},
	}}
	cookie, id := createSearchItem(t, ts, "search-nilfd@test.dev")
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/search-documents",
		map[string]any{"query": "q"}, cookie)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("nil fetchDirect = %d, want 503", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "fetching_disabled" {
		t.Errorf("code = %q, want fetching_disabled", body.Error.Code)
	}
}

func TestSearchDocsForeignItem404(t *testing.T) {
	_, ts := newTestServer(t, nil)
	// searcher deliberately stays NIL (search disabled): a foreign item must
	// answer 404 BEFORE the search-disabled check — config state is not
	// probeable through someone else's item.
	_, itemID := createSearchItem(t, ts, "search-owner@test.dev")
	other := registerUser(t, ts, "search-foreign@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(itemID)+"/search-documents",
		map[string]any{"query": "q"}, other)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign item = %d, want 404", resp.StatusCode)
	}
}

// hostOf pulls the host part out of an httptest URL.
func hostOf(u string) string {
	return strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
}
