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
