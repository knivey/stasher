package api

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

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

// ftsHasShortToken reports whether raw q contains a token shorter than the
// trigram tokenizer's 3-character minimum (migration 00005). Such a token
// forms no trigram, so it can never match -- and mixed queries silently DROP
// it ("ab raspberry" behaves as "raspberry"), so the short-circuit keeps the
// documented contract uniform: any query with a short term matches nothing.
func ftsHasShortToken(raw string) bool {
	for _, f := range strings.Fields(raw) {
		if utf8.RuneCountInString(f) < 3 {
			return true
		}
	}
	return false
}

// writeEmptyList answers a list/search request with a 200 empty envelope
// (data is a non-nil slice so it marshals as [], never null).
func writeEmptyList(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, listEnvelope{Data: []map[string]any{}, Total: 0})
}
