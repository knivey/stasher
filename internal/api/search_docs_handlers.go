package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"stasher/internal/fetcher"
)

// searchDocDeadline bounds one whole search+verify round trip. The response
// is synchronous — the client waits for it — so this, not the 90s job
// deadline, is what the frontend waits on.
const searchDocDeadline = 15 * time.Second

const (
	// searchVerifyWorkers verify candidates in parallel.
	searchVerifyWorkers = 3
	// searchTopCandidates is how many ranked Brave hits get verified.
	searchTopCandidates = 6
)

type searchDocsReq struct {
	Query string `json:"query"`
}

// searchDocsResult is one VERIFIED downloadable PDF. Size omits itself when
// the candidate server did not report a total (unknown length).
type searchDocsResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Host  string `json:"host"`
	Size  *int64 `json:"size,omitempty"`
}

// handleItemSearchDocuments searches the web for candidate documents and
// answers synchronously with only those that verify as downloadable PDFs
// (POST /items/{id}/search-documents). Owner-only like every owned-item
// route (foreign/absent → 404, before the search-disabled check — config
// state must not be probeable through someone else's item). Picking a
// result downloads it through the ordinary POST /items/{id}/fetch flow.
func (s *Server) handleItemSearchDocuments(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	if _, ok := s.requireOwnedItem(w, r, u); !ok {
		return
	}
	if s.searcher == nil {
		writeErr(w, http.StatusBadRequest, "search_disabled",
			"set brave_api_key in config to enable document search")
		return
	}
	var req searchDocsReq
	if !decodeJSON(w, r, &req) {
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" || len(query) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error",
			"query must be 1-200 characters")
		return
	}
	if !s.searchRate.Allow("user:" + strconv.FormatInt(u.ID, 10)) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited",
			"too many searches; try again in a few seconds")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), searchDocDeadline)
	defer cancel()
	cands, err := s.searcher.Search(ctx, query)
	if err != nil {
		slog.Warn("document search failed", "err", err)
		writeErr(w, http.StatusBadGateway, "search_failed", err.Error())
		return
	}
	if len(cands) > searchTopCandidates {
		cands = cands[:searchTopCandidates]
	}

	// Verify in parallel; results keep the ranked candidate order. A
	// candidate that fails verification counts toward rejected and is
	// dropped — err is diagnostic only (never surfaced to the client).
	oks := make([]bool, len(cands))
	sizes := make([]int64, len(cands))
	var wg sync.WaitGroup
	sem := make(chan struct{}, searchVerifyWorkers)
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c fetcher.SearchCandidate) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ok, size, err := s.fetchDirect.Verify(ctx, c.URL)
			if err != nil {
				slog.Warn("candidate verification failed", "url", c.URL, "err", err)
			}
			oks[i], sizes[i] = ok, size
		}(i, c)
	}
	wg.Wait()

	results := make([]searchDocsResult, 0, len(cands))
	rejected := 0
	for i, c := range cands {
		if !oks[i] {
			rejected++
			continue
		}
		res := searchDocsResult{Title: c.Title, URL: c.URL, Host: c.Host}
		if sizes[i] >= 0 {
			size := sizes[i]
			res.Size = &size
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "rejected": rejected})
}
