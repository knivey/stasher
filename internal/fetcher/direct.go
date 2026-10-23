package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	maxHops     = 5
	directLimit = 25 << 20
	directWait  = 30 * time.Second
	// limit+1: an oversized body is detected by length without buffering
	// more than one byte past the cap.
	maxBodyRead = directLimit + 1
)

// dialFunc is the test seam for the actual dial; nil means dial directly.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// pinnedIPKey carries the policy-approved IP from Fetch into the transport's
// dialer, closing the DNS-rebinding window: the dial goes to the CHECKED IP,
// the hostname is never re-resolved.
type pinnedIPKey struct{}

type DirectEngine struct {
	policy Policy
	// test seams (nil = production behavior)
	resolve     func(ctx context.Context, host string) ([]net.IP, error)
	dialContext dialFunc
	client      *http.Client
}

var _ Engine = (*DirectEngine)(nil)

func NewDirectEngine(p Policy) *DirectEngine {
	d := &DirectEngine{
		policy: p,
		resolve: func(ctx context.Context, host string) ([]net.IP, error) {
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			ips := make([]net.IP, 0, len(addrs))
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
			return ips, err
		},
	}
	// Proxy is deliberately nil (a configured proxy would receive the
	// hostname and defeat IP pinning) and keep-alives are off (a pooled
	// connection to a previously pinned IP must never be reused for a later
	// hop whose check approved a different address).
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			pin, ok := ctx.Value(pinnedIPKey{}).(net.IP)
			if !ok {
				return nil, fmt.Errorf("internal: no policy-checked IP for dial")
			}
			pinned := net.JoinHostPort(pin.String(), port)
			if d.dialContext != nil {
				return d.dialContext(ctx, network, pinned)
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, pinned)
		},
	}
	d.client = &http.Client{
		Transport: transport,
		Timeout:   directWait,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// hops are followed manually so each is scheme/userinfo/policy
			// re-checked before dialing
			return http.ErrUseLastResponse
		},
	}
	return d
}

// checkURL validates scheme/userinfo and resolves + policy-checks the host,
// returning the approved IP to pin the dial to.
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
		// EVERY resolved address must pass (spec §2): a mixed
		// public/private answer is a classic rebinding setup.
		if !d.policy.Allowed(ip) {
			return nil, fmt.Errorf("host resolves to a non-public address")
		}
	}
	return ips[0], nil
}

func (d *DirectEngine) Fetch(ctx context.Context, rawURL string) (Result, error) {
	return d.fetchAllow(ctx, rawURL, allowlisted)
}

// fetchAllow is the redirect-following core shared by Fetch and FetchTitle:
// every hop is scheme/userinfo/policy re-checked before dialing, and the
// sniffed content type must satisfy the caller's allow predicate.
func (d *DirectEngine) fetchAllow(ctx context.Context, rawURL string, allow func(string) bool) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("invalid URL")
	}
	for hop := 0; ; hop++ {
		pin, err := d.checkURL(ctx, u)
		if err != nil {
			return Result{}, err
		}
		req, err := http.NewRequestWithContext(
			context.WithValue(ctx, pinnedIPKey{}, pin), http.MethodGet, u.String(), nil)
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
			if hop >= maxHops {
				return Result{}, fmt.Errorf("too many redirects")
			}
			if loc == "" {
				return Result{}, fmt.Errorf("redirect without Location")
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
		if !allow(ct) {
			return Result{}, fmt.Errorf("unsupported content type %q (pdf, image, text only)", ct)
		}
		return Result{
			Body:        body,
			ContentType: ct,
			Filename:    suggestFilename(u, resp.Header.Get("Content-Disposition")),
		}, nil
	}
}

func allowlisted(ct string) bool {
	if ct == "application/pdf" || strings.HasPrefix(ct, "text/plain") {
		return true
	}
	return strings.HasPrefix(ct, "image/")
}

// allowHTML is the bookmark-title predicate: any HTML document qualifies
// (Fetch still rejects HTML via allowlisted — this seam exists for titles).
func allowHTML(ct string) bool {
	return strings.HasPrefix(ct, "text/html")
}

// FetchTitle does a best-effort HTML <title> extraction for bookmark
// labeling: 5s budget, 256KB read cap, policy-checked like every fetch.
// It NEVER returns an error — any failure collapses to the URL hostname.
func (d *DirectEngine) FetchTitle(ctx context.Context, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return truncateTitle(rawURL)
	}
	fallback := truncateTitle(u.Hostname())
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := d.fetchAllow(ctx, rawURL, allowHTML)
	if err != nil {
		return fallback
	}
	b := res.Body
	if len(b) > 256<<10 {
		b = b[:256<<10]
	}
	m := titleRe.FindSubmatch(b)
	if m == nil {
		return fallback
	}
	title := strings.Join(strings.Fields(string(m[1])), " ")
	title = strings.ReplaceAll(title, "&nbsp;", " ")
	title = html.UnescapeString(title)
	return truncateTitle(title)
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// stripControl removes C0/C1 control characters (newline, tab, BEL, DEL,
// ...). Titles flow into archived-PDF filenames via Content-Disposition and
// must stay single-line and filename-safe.
func stripControl(s string) string {
	if !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// truncateTitle caps s at 200 runes — the cap counts RUNES and never splits
// a multi-byte character, so a 200-rune CJK title exceeds 200 bytes (runes
// are the contract, not bytes). Control characters are stripped first so
// neither the stored title nor a derived filename carries them.
func truncateTitle(s string) string {
	s = strings.TrimSpace(stripControl(s))
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	if s == "" {
		return "untitled"
	}
	return s
}

// suggestFilename: Content-Disposition filename, else URL basename, else
// "fetch". Unsanitized suggestion — the API layer applies filename rules.
func suggestFilename(u *url.URL, cd string) string {
	if cd != "" {
		if i := strings.Index(cd, `filename="`); i >= 0 {
			rest := cd[i+len(`filename="`):]
			if j := strings.Index(rest, `"`); j > 0 {
				// same sanity filter as the URL branch: an empty/path-only
				// basename is no filename — fall through to the URL
				if base := path.Base(rest[:j]); base != "" && base != "/" && base != "." {
					return base
				}
			}
		}
	}
	if base := path.Base(u.Path); base != "" && base != "/" && base != "." {
		return base
	}
	return "fetch"
}
