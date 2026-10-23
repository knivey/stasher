package fetcher

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestArchiveFetchWithStubChrome drives the archive engine through the stub
// chrome fixture and pins the exact chromium flag contract (spec §2).
func TestArchiveFetchWithStubChrome(t *testing.T) {
	argsFile := t.TempDir() + "/args"
	t.Setenv("STUB_ARGS_FILE", argsFile)

	eng := NewArchiveEngine(LoopbackPolicy(), "./testdata/stub-chrome.sh")
	res, err := eng.Fetch(context.Background(), "https://example.com/docs/page")
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentType != "application/pdf" {
		t.Fatalf("ct = %s", res.ContentType)
	}
	if !strings.HasPrefix(string(res.Body), "%PDF-") {
		t.Fatalf("body is not a pdf: %q", string(res.Body[:16]))
	}
	// <host> <YYYY-MM-DD>.pdf, UTC
	name := regexp.MustCompile(`^example\.com \d{4}-\d{2}-\d{2}\.pdf$`)
	if !name.MatchString(res.Filename) {
		t.Fatalf("filename = %q, want \"example.com <YYYY-MM-DD>.pdf\"", res.Filename)
	}

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("stub recorded no args (engine did not exec chrome?): %v", err)
	}
	want := strings.Join([]string{
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--virtual-time-budget=10000", "--print-to-pdf=-",
		"https://example.com/docs/page",
	}, "\n") + "\n"
	if string(got) != want {
		t.Fatalf("chrome args:\n got %q\nwant %q", string(got), want)
	}
}

// TestArchiveFilenamePinnedUTC: the archived filename's date is the UTC
// date (house rule: UTC everywhere), not the server's local date. The
// injected instant is 2026-01-15 23:30 UTC and TZ is pinned to UTC+14 —
// locally that is already the 16th, so this passes only if the engine
// formats the injected time in UTC.
func TestArchiveFilenamePinnedUTC(t *testing.T) {
	t.Setenv("TZ", "Pacific/Kiritimati") // UTC+14: local date != UTC date
	eng := NewArchiveEngine(LoopbackPolicy(), "./testdata/stub-chrome.sh")
	eng.now = func() time.Time {
		return time.Date(2026, 1, 15, 23, 30, 0, 0, time.UTC)
	}
	res, err := eng.Fetch(context.Background(), "https://example.com/docs/page")
	if err != nil {
		t.Fatal(err)
	}
	if res.Filename != "example.com 2026-01-15.pdf" {
		t.Fatalf("filename = %q, want %q (UTC date, not local)", res.Filename, "example.com 2026-01-15.pdf")
	}
}

// TestArchiveRejectsUserinfo: credentials embedded in the URL are rejected
// before any exec — same rule as the direct engine. The stub chrome WOULD
// produce a valid PDF, so the only way this fetch fails is the userinfo gate.
func TestArchiveRejectsUserinfo(t *testing.T) {
	eng := NewArchiveEngine(LoopbackPolicy(), "./testdata/stub-chrome.sh")
	_, err := eng.Fetch(context.Background(), "http://user:pass@example.com/x")
	if err == nil || !strings.Contains(err.Error(), "userinfo") {
		t.Fatalf("want userinfo rejection, got %v", err)
	}
}

func TestArchiveMissingChrome(t *testing.T) {
	eng := NewArchiveEngine(LoopbackPolicy(), "/nonexistent/chrome-xyz")
	_, err := eng.Fetch(context.Background(), "https://example.com/x")
	if err == nil || !strings.Contains(err.Error(), "chromium") {
		t.Fatalf("want actionable missing-chrome error, got %v", err)
	}
}

func TestArchiveRejectsPrivateURL(t *testing.T) {
	// ipSetPolicy with an empty allow-set denies every address; chrome is
	// /bin/true so the ONLY way this fetch fails is the policy gate firing
	// before exec.
	eng := NewArchiveEngine(ipSetPolicy{allowed: map[string]bool{}}, "/bin/true")
	_, err := eng.Fetch(context.Background(), "http://10.0.0.5/panel")
	if err == nil {
		t.Fatal("private URL archived")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("want policy rejection, got %v", err)
	}
}

func TestArchiveRejectsNonHTTPScheme(t *testing.T) {
	eng := NewArchiveEngine(LoopbackPolicy(), "/bin/true")
	if _, err := eng.Fetch(context.Background(), "ftp://example.com/x"); err == nil {
		t.Fatal("non-http scheme accepted")
	}
}

func TestArchiveOutputCap(t *testing.T) {
	// Stub emits >25MB on stdout; the engine must abort at the cap, not
	// buffer it all.
	eng := NewArchiveEngine(LoopbackPolicy(), "./testdata/stub-chrome.sh")
	_, err := eng.Fetch(context.Background(), "http://127.0.0.1/page?emit_big=1")
	if err == nil || !strings.Contains(err.Error(), "25MB") {
		t.Fatalf("want 25MB cap error, got %v", err)
	}
}

func TestChromePathResolution(t *testing.T) {
	if p := ChromePath("/explicit/chrome"); p != "/explicit/chrome" {
		t.Fatalf("explicit ignored: %s", p)
	}
	// empty → PATH lookup; on this dev box at least one of the trio exists
	p := ChromePath("")
	if p == "" {
		t.Log("no chrome on PATH in this environment — acceptable")
	}
}
