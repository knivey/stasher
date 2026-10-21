package api

import "testing"

func TestFTSQueryQuotesTokens(t *testing.T) {
	got := ftsQuery(`raspberry "pi" 5 AND* OR`)
	want := `"raspberry" "pi" "5" "AND*" "OR"`
	if got != want {
		t.Fatalf("ftsQuery = %q, want %q", got, want)
	}
	if ftsQuery("") != "" {
		t.Fatal("empty query should stay empty")
	}
}

func TestFTSQueryWhitespaceAndSymbols(t *testing.T) {
	if got := ftsQuery("   "); got != "" {
		t.Fatalf(`ftsQuery("   ") = %q, want "" (zero tokens -> filter skipped)`, got)
	}
	// "+" is not whitespace: it survives Fields as a token and is wrapped as the
	// phrase "+". A bare "+" in a URL query string decodes to a space, so a
	// literal plus only reaches the handler percent-encoded as %2B; MATCH '"+"'
	// is valid FTS5 and matches nothing (driver-verified), so this stays safe.
	if got := ftsQuery("+"); got != `"+"` {
		t.Fatalf(`ftsQuery("+") = %q, want "+"`, got)
	}
}
