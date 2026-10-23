package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SearchCandidate is one web-search hit offered to the user.
type SearchCandidate struct {
	Title string
	URL   string
	Host  string
}

// Searcher runs a web query. Implemented by BraveSearcher; a nil Searcher
// means document search is disabled (no API key configured).
type Searcher interface {
	Search(ctx context.Context, query string) ([]SearchCandidate, error)
}

// braveEndpoint is a constant host operated by Brave. The search call needs
// no SSRF policy — it never fetches user-controlled URLs; the RESULT urls
// are only ever touched by Verify/Fetch, which enforce the full policy.
const braveEndpoint = "https://api.search.brave.com/res/v1/web/search"

const (
	braveTimeout  = 10 * time.Second
	braveRespCap  = 1 << 20 // 1MB response cap
	braveMaxCount = 10
)

// BraveSearcher queries the Brave web-search API.
type BraveSearcher struct {
	// Key is the X-Subscription-Token (brave_api_key config).
	Key string
	// baseURL overrides braveEndpoint (test seam; empty = production).
	baseURL string
}

var _ Searcher = (*BraveSearcher)(nil)

// braveResp mirrors the only part of the response we use.
type braveResp struct {
	Web struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
	} `json:"web"`
}

// Search queries Brave and returns up to 10 http(s) candidates, `.pdf`
// URLs ranked first (stable within the two groups).
func (b *BraveSearcher) Search(ctx context.Context, query string) ([]SearchCandidate, error) {
	base := b.baseURL
	if base == "" {
		base = braveEndpoint
	}
	ctx, cancel := context.WithTimeout(ctx, braveTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"?q="+url.QueryEscape(query)+"&count="+strconv.Itoa(braveMaxCount), nil)
	if err != nil {
		return nil, fmt.Errorf("brave request: %w", err)
	}
	req.Header.Set("X-Subscription-Token", b.Key)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == 422 {
		return nil, fmt.Errorf("Brave API rejected the key")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave search status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, braveRespCap+1))
	if err != nil {
		return nil, fmt.Errorf("brave search body: %w", err)
	}
	var parsed braveResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("brave search response: %w", err)
	}
	var cands []SearchCandidate
	for _, r := range parsed.Web.Results {
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		cands = append(cands, SearchCandidate{Title: r.Title, URL: r.URL, Host: u.Hostname()})
		if len(cands) >= braveMaxCount {
			break
		}
	}
	return rankPDFsFirst(cands), nil
}

// rankPDFsFirst stably partitions candidates: URLs whose PATH ends in
// ".pdf" (case-insensitive, before any query) come first.
func rankPDFsFirst(cands []SearchCandidate) []SearchCandidate {
	isPDF := func(u string) bool {
		p, err := url.Parse(u)
		if err != nil {
			return false
		}
		return strings.HasSuffix(strings.ToLower(p.Path), ".pdf")
	}
	out := make([]SearchCandidate, 0, len(cands))
	for _, c := range cands {
		if isPDF(c.URL) {
			out = append(out, c)
		}
	}
	for _, c := range cands {
		if !isPDF(c.URL) {
			out = append(out, c)
		}
	}
	return out
}

// verifyBytes is how much of the body Verify reads to sniff the magic.
const verifyBytes = 512

// Verify checks a candidate URL server-side through the SAME policy/pinning
// path as Fetch: every hop is scheme/userinfo/policy-checked and the dial is
// pinned to the approved IP. A Range request reads only the first 512 bytes;
// ok is true only when the status is 200/206 AND the body starts with the
// `%PDF-` magic. size is the Content-Range total when present, else -1
// (unknown). err carries the rejection reason (callers log it, never show
// it). Servers that 403 the Range request are retried once without the
// header.
func (d *DirectEngine) Verify(ctx context.Context, rawURL string) (bool, int64, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false, -1, fmt.Errorf("invalid URL")
	}
	for hop := 0; ; hop++ {
		if hop > maxHops {
			return false, -1, fmt.Errorf("too many redirects")
		}
		pin, err := d.checkURL(ctx, u)
		if err != nil {
			return false, -1, err
		}
		do := func(withRange bool) (*http.Response, error) {
			req, err := http.NewRequestWithContext(
				context.WithValue(ctx, pinnedIPKey{}, pin), http.MethodGet, u.String(), nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("User-Agent", "stasher/1")
			if withRange {
				req.Header.Set("Range", "bytes=0-511")
			}
			return d.client.Do(req)
		}
		resp, err := do(true)
		if err != nil {
			return false, -1, fmt.Errorf("verify failed: %w", err)
		}
		if resp.StatusCode == http.StatusForbidden {
			// Retry once without Range against the same pinned IP.
			resp.Body.Close()
			resp, err = do(false)
			if err != nil {
				return false, -1, fmt.Errorf("verify failed: %w", err)
			}
		}
		// manual redirect hop (same as Fetch: every hop re-checked)
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if loc == "" {
				return false, -1, fmt.Errorf("redirect without Location")
			}
			next, err := u.Parse(loc)
			if err != nil {
				return false, -1, fmt.Errorf("bad redirect")
			}
			u = next
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			return false, -1, fmt.Errorf("status %d", resp.StatusCode)
		}
		head := make([]byte, verifyBytes)
		n, _ := io.ReadFull(io.LimitReader(resp.Body, verifyBytes), head)
		resp.Body.Close()
		head = head[:n]
		if !bytes.HasPrefix(head, []byte("%PDF-")) {
			return false, -1, fmt.Errorf("not a PDF (magic check failed)")
		}
		return true, contentRangeTotal(resp.Header.Get("Content-Range")), nil
	}
}

// contentRangeTotal extracts the total from "bytes 0-511/12345" (or
// "bytes * /12345"); anything malformed or unspecified → -1.
func contentRangeTotal(cr string) int64 {
	i := strings.IndexByte(cr, '/')
	if i < 0 {
		return -1
	}
	total := strings.TrimSpace(cr[i+1:])
	if total == "*" {
		return -1
	}
	n, err := strconv.ParseInt(total, 10, 64)
	if err != nil || n < 0 {
		return -1
	}
	return n
}
