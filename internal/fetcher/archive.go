package fetcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"time"
)

const (
	archiveWait  = 60 * time.Second
	archiveLimit = 25 << 20
	// --virtual-time-budget: let JS settle before printing.
	chromeBudgetMs = "10000"
)

var errOverCap = errors.New("rendered PDF exceeds 25MB")

var errNoChromium = errors.New("archiving needs chromium: install chromium or set chrome_path / STASHER_CHROME_PATH")

// ChromePath resolves the chromium binary: explicit config/env value wins,
// else the first of chromium, chromium-browser, google-chrome on PATH.
// Empty result means "archive unavailable" (direct fetch is unaffected).
func ChromePath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

type ArchiveEngine struct {
	policy Policy
	chrome string
}

var _ Engine = (*ArchiveEngine)(nil)

func NewArchiveEngine(p Policy, chrome string) *ArchiveEngine {
	return &ArchiveEngine{policy: p, chrome: chrome}
}

// checkURL mirrors DirectEngine.checkURL's scheme/userinfo/policy semantics
// minus IP pinning: chromium re-resolves the host itself, so the approved IP
// cannot be pinned across the exec boundary — a check-then-exec gap the spec
// documents as a v1 limitation (mitigated by strictly user-initiated jobs).
func (a *ArchiveEngine) checkURL(ctx context.Context, u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q (http/https only)", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("userinfo in URL not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !a.policy.Allowed(ip) {
			return fmt.Errorf("address not allowed")
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("cannot resolve host")
	}
	for _, ip := range addrs {
		// EVERY resolved address must pass (spec §2) — same strict rule as
		// the direct engine: a mixed public/private answer is a classic
		// rebinding setup.
		if !a.policy.Allowed(ip.IP) {
			return fmt.Errorf("host resolves to a non-public address")
		}
	}
	return nil
}

func (a *ArchiveEngine) Fetch(ctx context.Context, rawURL string) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("invalid URL")
	}
	// fail fast before any resolution work (spec §6): empty chrome OR a
	// configured path that does not exist is the same actionable error
	bin := a.chrome
	if bin == "" {
		return Result{}, errNoChromium
	}
	if _, err := exec.LookPath(bin); err != nil {
		return Result{}, errNoChromium
	}
	if err := a.checkURL(ctx, u); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, archiveWait)
	defer cancel()
	// out caps itself at archiveLimit: a hostile page must not buffer
	// unbounded output before the size check.
	var out cappedWriter
	cmd := exec.CommandContext(ctx, bin,
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--virtual-time-budget="+chromeBudgetMs,
		"--print-to-pdf=-", u.String())
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if out.over {
			return Result{}, errOverCap
		}
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("page render failed: timed out after %s", archiveWait)
		}
		return Result{}, fmt.Errorf("page render failed: %w", err)
	}
	if out.over {
		return Result{}, errOverCap
	}
	body := out.buf.Bytes()
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		return Result{}, errors.New("renderer produced no PDF")
	}
	return Result{
		Body:        body,
		ContentType: "application/pdf",
		// UTC everywhere (house rule)
		Filename: fmt.Sprintf("%s %s.pdf", u.Hostname(), time.Now().UTC().Format("2006-01-02")),
	}, nil
}

// cappedWriter buffers up to archiveLimit bytes; the first write that would
// exceed the cap sets over and fails, which tears down the exec copy
// goroutine (the child then dies on EPIPE instead of running unbounded).
type cappedWriter struct {
	buf  bytes.Buffer
	over bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > archiveLimit {
		w.over = true
		return 0, errors.New("output over archive cap")
	}
	return w.buf.Write(p)
}
