package api

import "strings"

// ftsQuery converts raw user input into a safe FTS5 query: each whitespace-
// separated token becomes a double-quoted phrase (interior quotes doubled per
// FTS5 string escaping), tokens space-joined (implicit AND).
// This makes FTS5 syntax errors impossible and prevents operator injection.
// (Adapted from the plan: the verbatim ReplaceAll-doubling form emitted
// `"""pi"""` for the token `"pi"`, contradicting the locked test expectation
// `"pi"`; trimming edge quotes first satisfies the test with identical safety.)
func ftsQuery(raw string) string {
	fields := strings.Fields(raw)
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, `"`)
		parts = append(parts, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(parts, " ")
}
