package api

import (
	"testing"
	"time"
)

func TestShouldTouch(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		lastSeen string
		want     bool
	}{
		{"just seen", now.Add(-30 * time.Second).Format(time.RFC3339), false},
		{"exactly 60s ago", now.Add(-60 * time.Second).Format(time.RFC3339), true},
		{"minutes ago", now.Add(-5 * time.Minute).Format(time.RFC3339), true},
		{"garbage timestamp touches", "not-a-timestamp", true},
		{"empty timestamp touches", "", true},
		{"future timestamp (clock skew) skips", now.Add(10 * time.Minute).Format(time.RFC3339), false},
	}
	for _, tc := range cases {
		if got := shouldTouch(tc.lastSeen, now); got != tc.want {
			t.Errorf("%s: shouldTouch(%q) = %v, want %v", tc.name, tc.lastSeen, got, tc.want)
		}
	}
}
