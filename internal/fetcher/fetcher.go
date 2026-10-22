// Package fetcher retrieves user-provided URLs and turns them into storable
// files. All fetching is strictly user-initiated; SSRF protections are not
// optional: every dialed address must pass the IP policy, and the connection
// is pinned to the policy-checked IP (never re-resolved).
package fetcher

import "context"

// Result is one fetched, fully-buffered (capped) file.
type Result struct {
	Body        []byte
	ContentType string // sniffed + allowlisted
	Filename    string // suggested (caller sanitizes)
}

// Engine fetches one URL. Implementations: direct HTTP, chromium PDF.
type Engine interface {
	Fetch(ctx context.Context, rawURL string) (Result, error)
}
