# External Content (URL Fetch + Page Archive) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Server-side fetching of user-provided URLs (direct file download; full-page PDF render via headless chromium) stored as ordinary item documents, with hard SSRF protections, an in-memory job system, and an `items.product_url` field — the reusable engine later rounds (part-number search, project URLs, camofox, AI) extend.

**Architecture:** A leaf package `internal/fetcher` holds the SSRF policy (injectable for tests, hard-coded public-only in production), a direct HTTP engine with per-hop redirect checks and IP-pinned dials, and an archive engine that execs chromium `--print-to-pdf` (path from config/env/PATH). The API layer adds a single-flight in-memory job manager (`POST /items/{id}/fetch` → 202 + job id; `GET /jobs/{id}`) that stores results through the existing files+documents pipeline. Frontend adds the product-URL field, an Archive button, and a fetch dialog with polling.

**Tech Stack:** Go stdlib only (net/http, os/exec, crypto/rand); no new Go deps. Vue 3 + TS + Tailwind v4; no new npm deps.

**Spec:** `docs/superpowers/specs/2026-10-22-external-content-design.md`

## Global Constraints

- Every fetch is strictly user-initiated; nothing automatic dials out.
- SSRF (production, hard-coded, no config exposure): http/https only; reject loopback (`127/8`, `::1`), RFC1918 (`10/8`, `172.16/12`, `192.168/16`), link-local (`169.254/16`, `fe80/10`), ULA (`fc00/7`), IPv4-mapped IPv6, unspecified (`0.0.0.0`, `::`); URLs with userinfo rejected. Direct engine pins the connection to the approved IP (custom DialContext) and re-checks every redirect hop (max 5).
- Caps: direct 30s timeout / 25MB body; archive 60s timeout / 25MB output; archive filename `<host> <YYYY-MM-DD>.pdf` UTC.
- Direct content allowlist: `application/pdf`, `image/*`, `text/plain` (sniffed via `http.DetectContentType`; `%PDF-` magic forced to application/pdf). HTML never stored via direct.
- Jobs: in-memory map on Server, mutex-guarded; single-flight (one running fetch server-wide; queued jobs report `running`); owner-scoped (`GET /jobs/{id}` of another user → 404); finished jobs pruned after 1h; no persistence (restart abandons).
- Endpoints: `POST /api/v1/items/{id}/fetch` `{url, mode: direct|archive}` → 202 `{job_id}` (item foreign/absent → 404; URL must parse http(s) ≤2000 chars, mode enum → 400); `GET /api/v1/jobs/{id}` → `{status: running|done|failed, error?, document_id?}`.
- `items.product_url` (migration `00007_items_product_url.sql`, nullable): create/PATCH absent=keep, `""`=clear, non-empty valid http(s) URL ≤2000 chars → 400 `validation_error`; null in JSON when unset; carried through trash snapshot/restore AND item copy.
- Stored mime is always sniffed (client/Content-Type never trusted — R15); documents store through the existing pipeline (`sanitizeFilename`, `AddItemDocument`).
- Chromium resolution: config `chrome_path` (env `STASHER_CHROME_PATH` via the existing override list) → if empty, first existing of `chromium`, `chromium-browser`, `google-chrome` on PATH; missing → archive fails fast with an actionable message, direct unaffected.
- Frontend: fetch mocked at the client boundary; `vue-tsc -b`; dark: variants; no v-html; no silent empty catch; dist rebuilt via `make web` and committed with sources.
- TDD: failing test first, watch it fail, implement, watch it pass. Stdlib `testing` only (Go).

## Review Focus

Five input classes most likely to bite, each pinned by its owning task:

1. **Redirect chain: public first hop → private second hop** must be rejected before dialing (per-hop policy re-check) — Task 1, Step 1 (table case `redirect`).
2. **DNS rebinding: resolver returns public on check, private on connect** — direct engine must dial the approved IP, not re-resolve — Task 1, Step 1 (`flapping resolver` case) — pinned via a custom resolver fake that flips answers.
3. **HTML page fetched in direct mode** → rejected by the content allowlist, nothing stored — Task 1, Step 3.
4. **Chromium absent** → archive job fails fast with the install-hint message; direct unaffected — Task 2, Step 1 + Task 4, Step 3.
5. **Job id belonging to another user / vanished after restart** → 404, client surfaces "fetch interrupted" — Task 4, Steps 1 & 5 (+ frontend 404 path Task 5, Step 1).

---

### Task 1: `internal/fetcher` — policy + direct engine

**Files:**
- Create: `internal/fetcher/policy.go`, `internal/fetcher/direct.go`, `internal/fetcher/fetcher.go`
- Test: `internal/fetcher/policy_test.go`, `internal/fetcher/direct_test.go`

**Interfaces:**
- Consumes: none (leaf package).
- Produces (Task 2/4 rely on exactly these):
  - `type Result struct { Body []byte; ContentType string; Filename string }`
  - `type Policy interface { Allowed(ip net.IP) bool }`
  - `func PublicPolicy() Policy` (production: all the Global-Constraint ranges rejected)
  - `func LoopbackPolicy() Policy` (tests only: allows loopback/private so httptest targets work)
  - `type DirectEngine struct{...}; func NewDirectEngine(p Policy) *DirectEngine; func (d *DirectEngine) Fetch(ctx context.Context, rawURL string) (Result, error)`

- [ ] **Step 1: Write the failing policy + pinning tests**

`internal/fetcher/policy_test.go`:

```go
package fetcher

import (
	"net"
	"testing"
)

func TestPublicPolicyBlocksPrivateRanges(t *testing.T) {
	p := PublicPolicy()
	blocked := []string{
		"127.0.0.1", "127.8.8.8", "10.0.0.1", "172.16.0.1", "172.31.255.255",
		"192.168.1.1", "169.254.1.1", "0.0.0.0",
		"::1", "fe80::1", "fc00::1", "fd12::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::",
	}
	for _, s := range blocked {
		if p.Allowed(net.ParseIP(s)) {
			t.Errorf("PublicPolicy.Allowed(%s) = true, want false", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if !p.Allowed(net.ParseIP(s)) {
			t.Errorf("PublicPolicy.Allowed(%s) = false, want true", s)
		}
	}
}
```

`internal/fetcher/direct_test.go` (httptest targets are loopback → use LoopbackPolicy except where noted):

```go
package fetcher

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectFetchPDF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 fake"))
	}))
	defer srv.Close()
	res, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), srv.URL+"/datasheet-LM358.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentType != "application/pdf" {
		t.Fatalf("ct = %s", res.ContentType)
	}
	if res.Filename != "datasheet-LM358.pdf" {
		t.Fatalf("filename = %s", res.Filename)
	}
}

func TestDirectFetchRejectsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><script>alert(1)</script></html>"))
	}))
	defer srv.Close()
	if _, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("html accepted by direct engine")
	}
}

func TestDirectFetchRedirectToPrivateRejected(t *testing.T) {
	// First hop is the httptest server (allowed by loopback policy via the
	// engine under test using PublicPolicyForHops? No: use a policy that
	// allows the first hop's IP but blocks the redirect target. Build a
	// set-policy.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer srv2.Close()
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv2.URL+"/x", http.StatusFound)
	}))
	defer srv1.Close()
	firstIP := hostIP(t, srv1)
	p := ipSetPolicy{allowed: map[string]bool{firstIP: true}} // srv2's IP NOT allowed
	if _, err := NewDirectEngine(p).Fetch(context.Background(), srv1.URL); err == nil {
		t.Fatal("redirect to non-approved IP followed")
	}
}

func TestDirectFetchPinsResolvedIP(t *testing.T) {
	// Flapping resolver: first resolution (policy check) returns a public
	// IP, later resolutions (the transport's dial) return a private IP.
	// The engine must dial the PINNED public-approved IP — implemented via
	// a dialFunc seam the test replaces. See fetcher.go's dialFunc field.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("%PDF-1.4 pinned"))
	}))
	defer srv.Close()
	calls := 0
	flap := func(ctx context.Context, network, addr string) (net.Conn, error) {
		calls++
		// Assert the dialed addr uses the pinned (first-resolved) IP,
		// not a re-resolved one: engine passes the pinned IP in addr.
		if !strings.HasPrefix(addr, pinnedPrefix(t)) {
			t.Errorf("dial addr %q does not use pinned IP", addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Addr()) // really connect to the test server
	}
	// wire flap through a test seam: NewDirectEngine keeps resolver+dialer injectable
	eng := NewDirectEngine(LoopbackPolicy())
	eng.dialContext = flap
	res, err := eng.Fetch(context.Background(), "http://example.test/x.pdf")
	if err != nil || res.ContentType != "application/pdf" {
		t.Fatalf("pinned fetch: %v %s", err, res.ContentType)
	}
	if calls == 0 {
		t.Fatal("dial seam not exercised")
	}
}

func TestDirectFetchSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(make([]byte, 26<<20)) // 26MB > 25MB cap
	}))
	defer srv.Close()
	if _, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("26MB body accepted")
	}
}

func hostIP(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	host, _, err := net.SplitHostPort(srv.URL[len("http://"):])
	if err != nil {
		t.Fatal(err)
	}
	return host
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/fetcher/ -v
```

Expected: build failure — package files undefined.

- [ ] **Step 3: Implement policy + direct engine**

`internal/fetcher/fetcher.go`:

```go
// Package fetcher retrieves user-provided URLs and turns them into storable
// files. All fetching is user-initiated; SSRF protections are not optional.
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
```

`internal/fetcher/policy.go`:

```go
package fetcher

import "net"

// Policy decides whether a resolved IP may be dialed. Production uses
// PublicPolicy (hard-coded, not configurable); tests may inject looser
// policies so httptest targets work.
type Policy interface {
	Allowed(ip net.IP) bool
}

type publicPolicy struct{}

// PublicPolicy rejects every non-routable / private range and allows only
// public unicast addresses (v4 + v6).
func PublicPolicy() Policy { return publicPolicy{} }

func (publicPolicy) Allowed(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"} {
			_, n, _ := net.ParseCIDR(cidr)
			if n.Contains(ip4) {
				return false
			}
		}
		return true
	}
	// v6 (and v4-mapped, which To4 above unwraps): ULA fc00::/7
	for _, cidr := range []string{"fc00::/7"} {
		_, n, _ := net.ParseCIDR(cidr)
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

type loopbackPolicy struct{}

// LoopbackPolicy allows everything (test-only).
func LoopbackPolicy() Policy { return loopbackPolicy{} }
func (loopbackPolicy) Allowed(net.IP) bool { return true }
```

(Note: `net.IP.IsLoopback`, `IsLinkLocalUnicast`, and `IsPrivate` (Go 1.17+) cover 127/8, fe80::/10, and the RFC1918 sets — but keep the explicit CIDRs above so the rejected set is spelled out in code review; To4() unwraps IPv4-mapped IPv6 so `::ffff:10.0.0.1` hits the v4 checks.)

`internal/fetcher/direct.go`:

```go
package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	maxHops      = 5
	directLimit  = 25 << 20
	directWait   = 30 * time.Second
	maxBodyRead  = directLimit + 1
)

type DirectEngine struct {
	policy Policy
	// test seams
	resolve   func(ctx context.Context, host string) ([]net.IP, error) // default net.DefaultResolver.LookupIPAddr
	dialContext dialFunc // default: dial pinned IP
	client    *http.Client
}

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func NewDirectEngine(p Policy) *DirectEngine {
	d := &DirectEngine{
		policy:  p,
		resolve: func(ctx context.Context, host string) ([]net.IP, error) {
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			return ipsOf(addrs), err
		},
	}
	transport := &http.Transport{}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		// addr arrives as "host:port" from the transport; we replace the
		// host with the IP pinned at check time.
		_, port, _ := net.SplitHostPort(addr)
		pinAny, ok := ctx.Value(pinnedIPKey{}).(net.IP)
		if !ok {
			return nil, fmt.Errorf("no pinned ip")
		}
		pin := pinAny
		var dialer net.Dialer
		if d.dialContext != nil {
			return d.dialContext(ctx, network, net.JoinHostPort(pin.String(), port))
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(pin.String(), port))
	}
	d.client = &http.Client{
		Transport: transport,
		Timeout:   directWait,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // manual hops so each is policy-checked
		},
	}
	return d
}

type pinnedIPKey struct{}

func ipsOf(addrs []net.IPAddr) []net.IP {
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out
}

// checkURL validates scheme/userinfo and resolves+checks the host, returning
// the approved IP (one address; first public answer).
func (d *DirectEngine) checkURL(ctx context.Context, u *url.URL) (net.IP, error) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q (http/https only)", u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("userinfo in URL not allowed")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if !d.policy.Allowed(ip) {
			return nil, fmt.Errorf("address not allowed")
		}
		return ip, nil
	}
	ips, err := d.resolve(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("cannot resolve host")
	}
	for _, ip := range ips {
		if d.policy.Allowed(ip) {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("host resolves to a non-public address")
}

func (d *DirectEngine) Fetch(ctx context.Context, rawURL string) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("invalid URL")
	}
	hops := 0
	for {
		pin, err := d.checkURL(ctx, u)
		if err != nil {
			return Result{}, err
		}
		req, err := http.NewRequestWithContext(context.WithValue(ctx, pinnedIPKey{}, pin), http.MethodGet, u.String(), nil)
		if err != nil {
			return Result{}, err
		}
		req.Header.Set("User-Agent", "stasher/1")
		resp, err := d.client.Do(req)
		if err != nil {
			return Result{}, fmt.Errorf("fetch failed: %w", err)
		}
		// manual redirect hop
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			hops++
			if hops > maxHops || loc == "" {
				return Result{}, fmt.Errorf("too many redirects")
			}
			next, err := u.Parse(loc)
			if err != nil {
				return Result{}, fmt.Errorf("bad redirect")
			}
			u = next
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return Result{}, fmt.Errorf("status %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))
		resp.Body.Close()
		if err != nil {
			return Result{}, err
		}
		if len(body) > directLimit {
			return Result{}, fmt.Errorf("file exceeds 25MB limit")
		}
		head := body
		if len(head) > 512 {
			head = head[:512]
		}
		ct := http.DetectContentType(head)
		if bytes.HasPrefix(head, []byte("%PDF-")) {
			ct = "application/pdf"
		}
		if !allowlisted(ct) {
			return Result{}, fmt.Errorf("unsupported content type %q (pdf, image, text only)", ct)
		}
		return Result{Body: body, ContentType: ct, Filename: suggestFilename(u, resp.Header.Get("Content-Disposition"))}, nil
	}
}

func allowlisted(ct string) bool {
	if ct == "application/pdf" || ct == "text/plain" || ct == "text/plain; charset=utf-8" {
		return true
	}
	return strings.HasPrefix(ct, "image/")
}

func suggestFilename(u *url.URL, cd string) string {
	if cd != "" {
		// filename="..." from Content-Disposition
		if i := strings.Index(cd, `filename="`); i >= 0 {
			rest := cd[i+len(`filename="`):]
			if j := strings.Index(rest, `"`); j > 0 {
				return path.Base(rest[:j])
			}
		}
	}
	if base := path.Base(u.Path); base != "" && base != "/" && base != "." {
		return base
	}
	return "fetch"
}
```

(Add the `ipSetPolicy` test helper inside `direct_test.go`:

```go
type ipSetPolicy struct{ allowed map[string]bool }
func (p ipSetPolicy) Allowed(ip net.IP) bool { return p.allowed[ip.String()] }
```

and make `NewDirectEngine`'s `dialContext` field assignment work by declaring the field on the struct: `dialContext dialFunc`. The `pinnedPrefix` test helper asserts the addr begins with the srv host IP — adjust the test to capture the expected pinned string from the resolve seam: set `eng.resolve` to return the srv host IP for any host so the pin equals that IP.)

- [ ] **Step 4: Run tests to verify pass**

```bash
go vet ./internal/fetcher/ && go test ./internal/fetcher/ -v
```

Expected: all PASS. Add any missing seams minimally — do not weaken assertions to make things pass; if the pinning test design fights the implementation, reshape BOTH so the property is genuinely pinned (dial uses the checked IP).

- [ ] **Step 5: Commit**

```bash
git add internal/fetcher/
git commit -m "feat: fetcher package with SSRF policy and direct engine"
```

---

### Task 2: Archive engine (chromium) + chrome path config

**Files:**
- Create: `internal/fetcher/archive.go`, `internal/fetcher/testdata/stub-chrome.sh` (executable)
- Modify: `internal/config/config.go` (add `ChromePath string yaml:"chrome_path"` + env override), `stasher.yaml.example` (documented key)
- Test: `internal/fetcher/archive_test.go`

**Interfaces:**
- Consumes: `Policy`, `checkURL` semantics (reuse via a small shared helper or duplicate the scheme/userinfo/resolve check — prefer exporting an internal `checkHost(ctx, policy, host)` from Task 1).
- Produces: `func ChromePath(explicit string) string` (explicit → else PATH lookup), `type ArchiveEngine struct{...}; func NewArchiveEngine(p Policy, chrome string) *ArchiveEngine; func (a *ArchiveEngine) Fetch(ctx, rawURL) (Result, error)` — ContentType always `application/pdf`, Filename `<host> <YYYY-MM-DD>.pdf` (UTC).

- [ ] **Step 1: Write the failing tests**

`internal/fetcher/archive_test.go`:

```go
package fetcher

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestArchiveFetchWithStubChrome(t *testing.T) {
	stub := "./testdata/stub-chrome.sh"
	if _, err := os.Stat(stub); err != nil {
		t.Fatalf("stub chrome missing: %v", err)
	}
	eng := NewArchiveEngine(LoopbackPolicy(), stub)
	res, err := eng.Fetch(context.Background(), "https://example.com/docs/page")
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentType != "application/pdf" {
		t.Fatalf("ct = %s", res.ContentType)
	}
	if !strings.HasPrefix(string(res.Body), "%PDF-") {
		t.Fatalf("body is not a pdf: %q", res.Body[:16])
	}
	if !strings.HasPrefix(res.Filename, "example.com ") || !strings.HasSuffix(res.Filename, ".pdf") {
		t.Fatalf("filename = %s", res.Filename)
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
	eng := NewArchiveEngine(LoopbackPolicy(), "") // policy gate fires before exec
	eng2 := NewArchiveEngine(publicOnlyForHost("10.0.0.5"), "/bin/true")
	if _, err := eng2.Fetch(context.Background(), "http://10.0.0.5/panel"); err == nil {
		t.Fatal("private URL archived")
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
```

(`publicOnlyForHost` is a tiny test policy that denies one IP; reuse `ipSetPolicy` with an empty map.)

- [ ] **Step 2: Run to verify failure** — `go test ./internal/fetcher/ -run Archive -v` → build failure.

- [ ] **Step 3: Implement**

`internal/fetcher/testdata/stub-chrome.sh` (chmod +x, commit with the bit via `git update-index --chmod=+x` after add):

```sh
#!/bin/sh
# Stub headless chrome for tests: prints a minimal PDF to the --print-to-pdf
# target (or stdout with -), ignoring the URL.
out=""
prev=""
for arg in "$@"; do
  case "$prev" in
    --print-to-pdf) out="$arg" ;;
  esac
  prev="$arg"
done
pdf='%PDF-1.4 stub-chrome-output'
if [ -n "$out" ] && [ "$out" != "-" ]; then
  printf '%s' "$pdf" > "$out"
else
  printf '%s' "$pdf"
fi
exit 0
```

`internal/fetcher/archive.go`:

```go
package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"time"
)

const (
	archiveWait    = 60 * time.Second
	archiveLimit   = 25 << 20
	chromeBudgetMs = "10000" // --virtual-time-budget: let JS settle
)

// ChromePath resolves the chromium binary: explicit config/env value wins,
// else the first of chromium, chromium-browser, google-chrome on PATH.
// Empty result means "archive unavailable".
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

func NewArchiveEngine(p Policy, chrome string) *ArchiveEngine {
	return &ArchiveEngine{policy: p, chrome: chrome}
}

func (a *ArchiveEngine) Fetch(ctx context.Context, rawURL string) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Result{}, fmt.Errorf("unsupported scheme %q (http/https only)", u.Scheme)
	}
	if u.User != nil {
		return Result{}, fmt.Errorf("userinfo in URL not allowed")
	}
	// check-then-exec: resolve + policy gate. (IP pinning is not possible
	// across the exec boundary — documented v1 limitation, spec §2.)
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if !a.policy.Allowed(ip) {
			return Result{}, fmt.Errorf("address not allowed")
		}
	} else {
		addrs, err := net.LookupIP(host)
		if err != nil || len(addrs) == 0 {
			return Result{}, fmt.Errorf("cannot resolve host")
		}
		ok := false
		for _, ip := range addrs {
			if a.policy.Allowed(ip) {
				ok = true
				break
			}
		}
		if !ok {
			return Result{}, fmt.Errorf("host resolves to a non-public address")
		}
	}
	if a.chrome == "" {
		return Result{}, fmt.Errorf("archiving needs chromium: install chromium or set chrome_path / STASHER_CHROME_PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, archiveWait)
	defer cancel()
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, a.chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--virtual-time-budget="+chromeBudgetMs,
		"--print-to-pdf=-", u.String())
	cmd.Stdout = &buf
	limit := archiveLimit + 1
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("page render failed: %w", err)
	}
	if buf.Len() > archiveLimit {
		return Result{}, fmt.Errorf("rendered PDF exceeds 25MB")
	}
	body := buf.Bytes()
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		return Result{}, fmt.Errorf("renderer produced no PDF")
	}
	return Result{
		Body: body, ContentType: "application/pdf",
		Filename: fmt.Sprintf("%s %s.pdf", u.Hostname(), time.Now().UTC().Format("2006-01-02")),
	}, nil
}
```

Config: add the field + env wiring to `internal/config/config.go` following the existing pattern (env `STASHER_CHROME_PATH`), and a documented block in `stasher.yaml.example`:

```yaml
# Optional: path to a chromium/chrome binary for the page archiver
# (item product pages → PDF). Empty = auto-detect chromium,
# chromium-browser, or google-chrome on PATH. Without it, direct URL
# fetching still works; archiving returns a helpful error.
chrome_path: ""
```

- [ ] **Step 4: Run tests** — `go vet ./internal/fetcher/ && go test ./internal/fetcher/ -v` green; `go test ./internal/config/` green.

- [ ] **Step 5: Commit**

```bash
git add internal/fetcher/ internal/config/config.go stasher.yaml.example
git commit -m "feat: chromium pdf archive engine + chrome_path config"
```

---

### Task 3: `product_url` end-to-end (migration, API, trash, copy)

**Files:**
- Create: `internal/migrations/00007_items_product_url.sql`
- Modify: `internal/db/queries.sql` (CreateItem, CreateItemWithID, UpdateItem gain product_url) + `make generate`
- Modify: `internal/api/items_handlers.go` (create/PATCH validation + itemDetailJSON), `internal/api/trash_handlers.go` (snapshot + restore), `internal/api/shared_handlers.go` (copy passthrough)
- Test: `internal/api/items_handlers_test.go`, `internal/api/trash_handlers_test.go`, `internal/api/shared_handlers_test.go` (extend)

**Interfaces:**
- Consumes: existing create/PATCH/itemDetailJSON/snapshot/copy structures.
- Produces: item JSON carries `product_url` (string or null); `CreateItemParams`/`UpdateItemParams`/`CreateItemWithIDParams` gain `ProductUrl sql.NullString`; `trashSnapshot` gains `ProductUrl string json:"product_url,omitempty"`.

- [ ] **Step 1: Write the failing tests**

In `items_handlers_test.go` (house style):

```go
func TestItemProductURL(t *testing.T) {
	// create with product_url "https://example.com/x" → 201, detail JSON echoes it
	// create with "ftp://x" and "not a url" and 2001-chars → 400 validation_error
	// PATCH: absent keeps; "" clears (JSON null); valid updates
	// list JSON does NOT include product_url (detail-only field)
}
func TestTrashRestoreCarriesProductURL(t *testing.T) {
	// item with product_url → trash → restore → product_url preserved
}
func TestCopyCarriesProductURL(t *testing.T) {
	// copy duplicates product_url to the new item
}
```

(Flesh each into real assertions exactly like neighboring tests — decode detail JSON, compare `product_url`.)

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement**

Migration `00007_items_product_url.sql`:

```sql
-- +goose Up
ALTER TABLE items ADD COLUMN product_url TEXT;

-- +goose Down
ALTER TABLE items DROP COLUMN product_url;
```

queries.sql: add `product_url` to the three item write queries' column lists + params (CreateItem, CreateItemWithID, UpdateItem), `make generate`. Update every `CreateItemParams` call site (create handler, copy, trash restore) with `ProductUrl: sql.NullString{String: ..., Valid: ...}`.

items_handlers.go:
- create request struct gains `ProductURL *string`; validate when non-nil non-empty: `url.Parse`, scheme http/https, len ≤2000 → else 400 `validation_error "product_url must be an http(s) URL of at most 2000 chars"`.
- PATCH: `ProductURL *string`; nil = keep; non-nil = set ("" clears to NULL). Same validation for non-empty values.
- `itemDetailJSON` adds `"product_url": nullString(i.ProductUrl)` (add the tiny `nullString(sql.NullString) any` helper near `thumbJSON` — nil when invalid or empty).
- trash snapshot: `ProductUrl: item.ProductUrl.String` (empty when unset); restore passes `sql.NullString{String: snap.ProductUrl, Valid: snap.ProductUrl != ""}`.
- copy: pass source's ProductUrl through to `CreateItem`.

- [ ] **Step 4: Full suite green** — `go vet ./... && go test -count=1 ./...` (existing tests that pin item JSON keys may need `product_url` added where they compare whole objects — update, don't weaken).

- [ ] **Step 5: Commit** — `feat: items product_url end-to-end (api, trash, copy)`

---

### Task 4: Job manager + fetch/jobs endpoints

**Files:**
- Create: `internal/api/jobs.go`
- Modify: `internal/api/server.go` (routes + engine wiring on Server: `fetchDirect fetcher.Engine`, `fetchArchive fetcher.Engine`, jobs map + mutex + single-flight chan), `internal/api/items_handlers.go` or `jobs.go` (handler), `cmd/stasher/main.go` (construct engines from config), `docs/api-notes.md`
- Test: `internal/api/jobs_test.go`

**Interfaces:**
- Consumes: `fetcher.Engine` (both), `PublicPolicy()`, `ChromePath(cfg.ChromePath)`, existing `s.st.Save`, `AddItemDocument`, `sanitizeFilename`, `requireOwnedItem`.
- Produces: routes `POST /api/v1/items/{id}/fetch` and `GET /api/v1/jobs/{id}`; job JSON shapes (Global Constraints).

- [ ] **Step 1: Write the failing tests** (`internal/api/jobs_test.go`; server wiring in `newTestServer` must construct engines with `LoopbackPolicy` + stub chrome so tests can fetch real httptest URLs):

```go
func TestFetchJobDirect(t *testing.T) {
	// httptest server serving a PDF named lm358.pdf
	// POST /items/{id}/fetch {url, mode:"direct"} → 202 job_id
	// poll GET /jobs/{id} until done (test helper: up to ~5s, 50ms interval)
	// → status done + document_id; item detail documents[] contains the doc
	//   with filename "lm358.pdf", mime application/pdf, size > 0
}
func TestFetchJobFailureKeepsItemClean(t *testing.T) {
	// URL 404s → job failed with a one-line error; no document added
}
func TestFetchJobArchiveStub(t *testing.T) {
	// mode "archive" with stub chrome → done; doc filename matches
	// "<host> <date>.pdf"; mime application/pdf
}
func TestFetchJobValidation(t *testing.T) {
	// table: bad URL, ftp scheme, 2001 chars, bad mode, foreign item → 404
}
func TestFetchJobOwnerScoped(t *testing.T) {
	// bob's client GETs alice's job id → 404
}
func TestFetchJobsSingleFlight(t *testing.T) {
	// stub chrome that sleeps 300ms; start two archive jobs;
	// the second stays running until the first finishes (assert via
	// observing job statuses while the first runs, or total wall time > 2x sleep)
}
```

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement**

`internal/api/jobs.go` sketch (full implementation required, following server patterns):

```go
type jobState struct {
	id      string
	userID  int64
	status  string // running|done|failed
	err     string
	docID   int64
	created time.Time
}

// Server fields: jobs map[string]*jobState; jobsMu sync.Mutex; fetchSem chan struct{} (cap 1)
// api.New gains: fetchDirect, fetchArchive fetcher.Engine (nil-safe: POST /fetch returns 503 "fetching disabled" if both nil — wire always in practice).
```

POST handler: `requireOwnedItem` → decode `{URL string `json:"url"`, Mode string `json:"mode"`}` → validate (Global Constraints) → job id = base64.RawURLEncoding of 16 crypto/rand bytes → store running → `go runJob(...)` → 202 `{"job_id": id}`.

`runJob`: acquire `s.fetchSem` (block — single-flight); pick engine by mode; `eng.Fetch(ctx, rawURL)`; on success: `key, size, err := s.st.Save(bytes.NewReader(res.Body), extForContent(res.ContentType))` (reuse/extract `extForMime` — pdf→pdf, image/png→png, jpeg→jpg, gif, webp, text/plain→txt); `s.q.AddItemDocument(... Filename: sanitizeFilename(res.Filename), MimeType: res.ContentType, SizeBytes: size, CreatedAt: now)`; mark done with docID; on error mark failed with `err.Error()`; release sem. Prune finished jobs older than 1h under the mutex on each submission.

GET handler: parse id, mutex-lock lookup, owner check (`j.userID != u.ID` → 404 `not_found`), respond `{"status":..., "error":..., "document_id":...}` (omit empty fields).

`cmd/stasher/main.go`: resolve `chrome := fetcher.ChromePath(cfg.ChromePath)` once, construct `fetcher.NewDirectEngine(fetcher.PublicPolicy())` and `fetcher.NewArchiveEngine(fetcher.PublicPolicy(), chrome)`, pass into `api.New` (extend its signature — update the one call site + test server).

`docs/api-notes.md`: new "External fetch & jobs" section — both endpoints, modes, caps, allowlist, single-flight queueing, in-memory jobs (404 after restart = interrupted), product_url PATCH semantics pointer.

- [ ] **Step 4: Full suite green** + manual smoke: `make check`.

- [ ] **Step 5: Commit** — `feat: fetch jobs, endpoints, engine wiring`

---

### Task 5: Frontend — product URL field, fetch dialog, polling

**Files:**
- Modify: `web/src/api/types.ts` (Item gains `product_url?: string | null`; job response types), `web/src/views/ItemFormView.vue`, `web/src/views/ItemDetailView.vue` (+ both test files)
- Build output: `internal/webfs/dist/**` via `make web`

**Interfaces:**
- Consumes: Task 3 (`product_url` on item JSON), Task 4 endpoints.
- Produces: no interface changes beyond types.

- [ ] **Step 1: Write failing tests** (both files, house fetch-mock style):

```ts
// ItemFormView: field renders, saves product_url in POST/PATCH body; "" clears
// ItemDetailView:
//  - product_url renders as external <a target=_blank rel=noopener> + Archive button
//  - "Fetch document from URL…" opens dialog; .pdf URL preselects mode direct,
//    other URL preselects archive; user can override
//  - submit → POST /api/v1/items/1/fetch with chosen body → toast "Fetching…"
//    → polls GET /api/v1/jobs/{id} (mock sequence running → done)
//    → toast "Added document" + item refetch + dialog closes
//  - job failed → error toast with job error
//  - job 404 (interrupted) → "fetch interrupted" toast
//  - Archive button → POST with mode "archive" and the field's URL
```

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement**

- types: `product_url?: string | null`; `interface FetchJob { status: 'running' | 'done' | 'failed'; error?: string; document_id?: number }`.
- ItemFormView: optional "Product page URL" input (`type=url` advisory; server validates) in `form`, included in POST body when non-empty; PATCH sends only when changed (buildPatch), `""` clears.
- ItemDetailView:
  - product URL line under the meta section when set: link + small "Archive" button (same flow, mode `archive`, URL from the item).
  - Documents section header action "Fetch from URL…" → dialog (URL input, mode select direct/archive with auto-guess on URL input: `url.includes('.pdf')` heuristics — `.pdf` before query/hash), submit disabled while URL empty or job starting.
  - Polling: `async function runFetchJob(url, mode)` — POST, toast `Fetching…`, loop `await new Promise(r => setTimeout(r, 2000))` + GET, up to 45 iterations (90s); clear timer on unmount (`onUnmounted` sets a cancelled flag); outcomes per tests; success → `fetchItem()`.
- Dark variants; no v-html; ApiError → toast.

- [ ] **Step 4: GREEN + typecheck + build** — `cd web && npx vitest run && npx vue-tsc -b && cd .. && make web`.

- [ ] **Step 5: Commit** — `feat(web): product url field, fetch dialog, job polling` (sources + dist).

---

### Task 6: Final verification + INSTALL docs

**Files:**
- Modify: `docs/INSTALL.md` ("Optional: headless chromium" — alpine/debian/arch, what works without it)

- [ ] **Step 1: INSTALL section** (exact content):

```markdown
## Optional: headless chromium (page archiving)

Direct URL fetching (datasheet/manual PDFs, images) needs nothing extra.
Rendering a web page to a PDF archive uses headless chromium:

- Alpine: `apk add chromium`
- Debian: `apt install chromium`
- Arch: `pacman -S chromium`

Stasher auto-detects `chromium`, `chromium-browser`, or `google-chrome` on
PATH; set `chrome_path` (or `STASHER_CHROME_PATH`) to point at a specific
binary. Without chromium, archive requests fail with a hint — everything
else works.
```

- [ ] **Step 2: Full gate** — `make check && go build -o stasher ./cmd/stasher && ./scripts/smoke-web.sh && (cd web && node scripts/smoke-authed.mjs)`.

- [ ] **Step 3: Commit** — `docs: chromium install notes for the archiver`
