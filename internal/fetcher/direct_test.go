package fetcher

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestDirectFetchContentDispositionFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="report 2026.pdf"`)
		w.Write([]byte("%PDF-1.4 cd"))
	}))
	defer srv.Close()
	res, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.Filename != "report 2026.pdf" {
		t.Fatalf("filename = %q, want Content-Disposition suggestion", res.Filename)
	}
}

func TestDirectFetchContentTypes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // sniffed content type
	}{
		{"png", "\x89PNG\r\n\x1a\n", "image/png"},
		{"text", "plain text notes", "text/plain; charset=utf-8"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.want)
			w.Write([]byte(tc.body))
		}))
		res, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), srv.URL)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if res.ContentType != tc.want {
			t.Errorf("%s: ct = %q, want %q", tc.name, res.ContentType, tc.want)
		}
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

func TestDirectFetchRejectsNonHTTPScheme(t *testing.T) {
	if _, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), "ftp://example.com/x.pdf"); err == nil {
		t.Fatal("ftp scheme accepted")
	}
}

func TestDirectFetchRejectsUserinfo(t *testing.T) {
	if _, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), "http://user:pass@example.com/x.pdf"); err == nil {
		t.Fatal("userinfo URL accepted")
	}
}

func TestDirectFetchRedirectToPrivateRejected(t *testing.T) {
	// Both httptest servers would share 127.0.0.1, so the redirect target is
	// an IP-literal private address the set-policy does not allow. A correct
	// engine rejects it at the policy layer BEFORE dialing; the dial seam
	// proves it was never dialed (a naive follower would attempt the dial).
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/x", http.StatusFound)
	}))
	defer srv1.Close()
	firstIP := hostIP(t, srv1)
	p := ipSetPolicy{allowed: map[string]bool{firstIP: true}} // 10.0.0.1 NOT allowed
	eng := NewDirectEngine(p)
	var dialed []string
	eng.dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		if host, _, err := net.SplitHostPort(addr); err != nil || host != firstIP {
			return nil, fmt.Errorf("blocked by test seam: %s", addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, srv1.Listener.Addr().String())
	}
	if _, err := eng.Fetch(context.Background(), srv1.URL); err == nil {
		t.Fatal("redirect to non-approved IP followed")
	}
	if len(dialed) == 0 {
		t.Fatal("first hop never dialed")
	}
	for _, a := range dialed {
		if strings.HasPrefix(a, "10.0.0.1:") {
			t.Errorf("non-approved IP was dialed: %s", a)
		}
	}
}

func TestDirectFetchPinsResolvedIP(t *testing.T) {
	// Flapping resolver: the first resolution (the one the policy checks)
	// returns the test server's IP; any later resolution returns a different
	// IP. The engine must dial the PINNED policy-checked IP — never
	// re-resolve the hostname.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 pinned"))
	}))
	defer srv.Close()
	srvIP := hostIP(t, srv)

	resolveCalls := 0
	eng := NewDirectEngine(LoopbackPolicy())
	eng.resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		resolveCalls++
		if resolveCalls == 1 {
			return []net.IP{net.ParseIP(srvIP)}, nil
		}
		// flapped: a second answer would be a private rebinding target
		return []net.IP{net.ParseIP("10.9.9.9")}, nil
	}
	var dialed []string
	eng.dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		h, _, err := net.SplitHostPort(addr)
		if err != nil {
			t.Errorf("dial addr %q: %v", addr, err)
		} else if h != srvIP {
			t.Errorf("dial addr %q does not use pinned IP %s (re-resolution?)", addr, srvIP)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String()) // really connect to the test server
	}

	res, err := eng.Fetch(context.Background(), "http://rebind.test/datasheet.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentType != "application/pdf" {
		t.Fatalf("ct = %s", res.ContentType)
	}
	if resolveCalls != 1 {
		t.Errorf("resolver called %d times, want 1 (transport must not re-resolve)", resolveCalls)
	}
	if len(dialed) == 0 {
		t.Fatal("dial seam not exercised")
	}
}

func TestDirectFetchTooManyRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()
	if _, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("redirect loop not capped")
	}
}

func TestDirectFetchSizeCap(t *testing.T) {
	// %PDF- magic up front so sniffing allowlists the type and the ONLY
	// possible rejection reason is the size cap.
	body := make([]byte, 26<<20) // 26MB > 25MB cap
	copy(body, "%PDF-1.4\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(body)
	}))
	defer srv.Close()
	if _, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("26MB body accepted")
	}
}

func TestDirectFetchRejectsMixedResolution(t *testing.T) {
	// Spec §2: a host with ANY non-public address among its A/AAAA answers
	// is rejected before any dial — even if another answer is public.
	eng := NewDirectEngine(PublicPolicy())
	resolveCalls := 0
	eng.resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		resolveCalls++
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.1")}, nil
	}
	var dialed []string
	eng.dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return nil, fmt.Errorf("blocked by test seam: %s", addr)
	}
	if _, err := eng.Fetch(context.Background(), "http://rebind.test/x.pdf"); err == nil {
		t.Fatal("host with a private address among its answers was accepted")
	}
	if resolveCalls != 1 {
		t.Fatalf("resolver called %d times, want 1", resolveCalls)
	}
	if len(dialed) != 0 {
		t.Errorf("mixed-resolution host was dialed: %v", dialed)
	}
}

func TestDirectFetchRedirectDialsFreshPinPerHop(t *testing.T) {
	// Hop 1 (127.0.0.1) redirects to a DIFFERENT host (public IP literal)
	// whose server really listens on loopback; the dial must use hop 2's
	// freshly checked IP — never hop 1's.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("from B"))
	}))
	defer srv2.Close()
	_, port2, err := net.SplitHostPort(srv2.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://93.184.216.34:"+port2+"/x", http.StatusFound)
	}))
	defer srv1.Close()

	p := ipSetPolicy{allowed: map[string]bool{"127.0.0.1": true, "93.184.216.34": true}}
	eng := NewDirectEngine(p)
	var dialed []string
	eng.dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		target := addr
		if host == "93.184.216.34" {
			// B's server really listens on loopback:port2
			target = net.JoinHostPort("127.0.0.1", port)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, target)
	}
	res, err := eng.Fetch(context.Background(), srv1.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "from B" {
		t.Fatalf("body = %q, want hop 2's response", res.Body)
	}
	if len(dialed) != 2 {
		t.Fatalf("dialed %v, want exactly one dial per hop", dialed)
	}
	if h, _, err := net.SplitHostPort(dialed[1]); err != nil || h != "93.184.216.34" {
		t.Errorf("hop 2 dialed %q, want B's IP (stale pin?)", dialed[1])
	}
}

func TestDirectFetchCDBasenameFallback(t *testing.T) {
	// Useless Content-Disposition filenames fall through to the URL basename.
	cases := []struct{ cd string }{
		{`attachment; filename=""`},
		{`attachment; filename="."`},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", tc.cd)
			w.Write([]byte("%PDF-1.4 fb"))
		}))
		res, err := NewDirectEngine(LoopbackPolicy()).Fetch(context.Background(), srv.URL+"/manual.pdf")
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.cd, err)
		}
		if res.Filename != "manual.pdf" {
			t.Errorf("%s: filename = %q, want URL basename %q", tc.cd, res.Filename, "manual.pdf")
		}
	}
}

func TestFetchTitle(t *testing.T) {
	// /page serves titled HTML; /noTitle serves HTML without a <title>.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/noTitle" {
			fmt.Fprint(w, "<html><head></head><body>no title here</body></html>")
			return
		}
		fmt.Fprint(w, "<html><head><title>  LM358&nbsp;Datasheet </title></head><body>x</body></html>")
	}))
	defer srv.Close()
	d := NewDirectEngine(LoopbackPolicy())
	if got := d.FetchTitle(context.Background(), srv.URL+"/page"); got != "LM358 Datasheet" {
		t.Fatalf("title = %q", got)
	}

	// non-HTML body / network failure / bad URL → hostname fallback
	if got := d.FetchTitle(context.Background(), srv.URL+"/noTitle"); got != "127.0.0.1" {
		t.Fatalf("no-title fallback = %q, want hostname", got) // that path serves HTML without <title>
	}
	if got := d.FetchTitle(context.Background(), "http://127.0.0.1:1/nope"); got != "127.0.0.1" {
		t.Fatalf("dead-host title = %q, want hostname", got)
	}
	if got := d.FetchTitle(context.Background(), "not a url"); got != "not a url" {
		t.Fatalf("garbage url title = %q, want raw string", got)
	}
}

func TestTruncateTitleControlStripAndRuneCap(t *testing.T) {
	// titles feed archived-PDF filenames via Content-Disposition: control
	// characters (newline, tab, BEL, DEL, ...) must never reach one
	if got := truncateTitle("A\x07B\nC\tD"); got != "ABCD" {
		t.Fatalf("control strip = %q, want ABCD", got)
	}
	if got := truncateTitle("\x00\x1f\x7f"); got != "untitled" {
		t.Fatalf("control-only = %q, want untitled", got)
	}
	// 201 CJK runes = 603 bytes: the old s[:200] byte slice split a rune and
	// surfaced U+FFFD. The cap must count runes.
	cjk := strings.Repeat("漢", 201)
	got := truncateTitle(cjk)
	if n := utf8.RuneCountInString(got); n != 200 {
		t.Fatalf("rune count = %d, want 200", n)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatal("truncation split a multi-byte rune (U+FFFD present)")
	}
	if got != strings.Repeat("漢", 200) {
		t.Fatal("truncated title content mismatch")
	}
	// short ASCII passes through untouched
	if got := truncateTitle("plain title"); got != "plain title" {
		t.Fatalf("short ascii = %q", got)
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

type ipSetPolicy struct{ allowed map[string]bool }

func (p ipSetPolicy) Allowed(ip net.IP) bool { return p.allowed[ip.String()] }
