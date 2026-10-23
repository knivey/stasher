package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// testPNG returns bytes carrying the PNG magic — enough for the client,
// which takes mimes as given (the endpoint sniffs).
func testPNG(b ...byte) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, b...)
}

// contentReply writes a chat-completions response whose single choice
// carries the given content string.
func contentReply(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{
			map[string]any{"message": map[string]any{"role": "assistant", "content": content}},
		},
	})
}

// reqEnvelope captures the parts of the outgoing request the tests pin.
// Both token-budget fields are pointers so "absent" is distinguishable
// from a value (the ladder sends exactly one per request).
type reqEnvelope struct {
	Model               string          `json:"model"`
	MaxCompletionTokens *int            `json:"max_completion_tokens"`
	MaxTokens           *int            `json:"max_tokens"`
	ResponseFormat      json.RawMessage `json:"response_format"`
	Messages            []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// envelopeLog records every request envelope a fake provider receives
// (the ladder tests assert on the full request sequence).
type envelopeLog struct {
	mu       sync.Mutex
	requests []reqEnvelope
}

// add reads and records r's envelope, returning it for the handler to
// branch on.
func (l *envelopeLog) add(r *http.Request) reqEnvelope {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return reqEnvelope{}
	}
	var env reqEnvelope
	_ = json.Unmarshal(body, &env)
	l.mu.Lock()
	l.requests = append(l.requests, env)
	l.mu.Unlock()
	return env
}

func (l *envelopeLog) snapshot() []reqEnvelope {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]reqEnvelope(nil), l.requests...)
}

type reqContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

func TestNew(t *testing.T) {
	c := New("k", "", "m")
	if c.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q, want OpenAI default", c.BaseURL)
	}
	if c.APIKey != "k" || c.Model != "m" {
		t.Errorf("APIKey/Model = %q/%q, want k/m", c.APIKey, c.Model)
	}
	if c.HTTP == nil {
		t.Fatal("HTTP client is nil")
	}
	if c.HTTP.Timeout != 45*time.Second {
		t.Errorf("HTTP.Timeout = %v, want 45s", c.HTTP.Timeout)
	}
	if !c.Enabled() {
		t.Error("Enabled() = false, want true with key+model")
	}
	if New("", "http://x", "m").Enabled() {
		t.Error("Enabled() = true without API key")
	}
	if New("k", "http://x", "").Enabled() {
		t.Error("Enabled() = true without model")
	}
}

func TestAutofillHappyPath(t *testing.T) {
	img1 := testPNG(1, 2, 3)
	img2 := testPNG(9, 9)

	var (
		got   reqEnvelope
		auth  string
		ctype string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		ctype = r.Header.Get("Content-Type")
		if r.URL.Path != "/chat/completions" {
			t.Errorf("request path = %q, want /chat/completions", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("fake provider: reading request: %v", err)
			return
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("fake provider: request is not valid JSON: %v", err)
		}
		contentReply(w, `{"name":"LM358 op-amp","qty":14,"unit":"pcs","tags":["op-amp","dip-8"]}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	fields, err := c.Autofill(context.Background(), [][]byte{img1, img2}, []string{"image/png", "image/png"})
	if err != nil {
		t.Fatalf("Autofill: %v", err)
	}

	// Fields match.
	if fields.Name != "LM358 op-amp" {
		t.Errorf("Name = %q", fields.Name)
	}
	if fields.Description != "" {
		t.Errorf("Description = %q, want empty", fields.Description)
	}
	if fields.Qty == nil || *fields.Qty != 14 {
		t.Errorf("Qty = %v, want *14", fields.Qty)
	}
	if fields.Unit != "pcs" {
		t.Errorf("Unit = %q", fields.Unit)
	}
	if len(fields.Tags) != 2 || fields.Tags[0] != "op-amp" || fields.Tags[1] != "dip-8" {
		t.Errorf("Tags = %v, want [op-amp dip-8]", fields.Tags)
	}

	// Request shape pinned.
	if auth != "Bearer k" {
		t.Errorf("Authorization = %q, want Bearer k", auth)
	}
	if ctype != "application/json" {
		t.Errorf("Content-Type = %q", ctype)
	}
	if got.Model != "test-model" {
		t.Errorf("model = %q", got.Model)
	}
	if got.MaxCompletionTokens == nil || *got.MaxCompletionTokens != 4000 {
		t.Errorf("max_completion_tokens = %v, want 4000", got.MaxCompletionTokens)
	}
	if got.MaxTokens != nil {
		t.Errorf("max_tokens = %d present — the modern parameter is sent on the first attempt", *got.MaxTokens)
	}
	if len(got.ResponseFormat) != 0 {
		t.Errorf("response_format present (%s) — not universal across providers", got.ResponseFormat)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Errorf("roles = %q/%q, want system/user", got.Messages[0].Role, got.Messages[1].Role)
	}
	var sysText string
	if err := json.Unmarshal(got.Messages[0].Content, &sysText); err != nil || sysText == "" {
		t.Errorf("system content = %s (%v), want non-empty text", got.Messages[0].Content, err)
	}
	var parts []reqContentPart
	if err := json.Unmarshal(got.Messages[1].Content, &parts); err != nil {
		t.Fatalf("user content is not a parts array: %v", err)
	}
	images, texts := 0, 0
	for _, p := range parts {
		switch p.Type {
		case "image_url":
			images++
			if !strings.HasPrefix(p.ImageURL.URL, "data:image/png;base64,") {
				t.Errorf("image url %q lacks data:image/png;base64, prefix", p.ImageURL.URL)
			}
		case "text":
			texts++
		default:
			t.Errorf("unexpected part type %q", p.Type)
		}
	}
	if images != 2 {
		t.Errorf("image parts = %d, want 2 (one per image)", images)
	}
	if texts != 1 {
		t.Errorf("text parts = %d, want 1", texts)
	}
	if len(parts) > 0 {
		want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(img1)
		if parts[0].ImageURL.URL != want {
			t.Errorf("first image url = %q, want exact base64 of original bytes", parts[0].ImageURL.URL)
		}
	}
}

func TestAutofillFencedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentReply(w, "```json\n{\"name\":\"bolt\",\"qty\":3}\n```")
	}))
	defer srv.Close()

	// Trailing slash on the base URL must be tolerated.
	c := New("k", srv.URL+"/", "test-model")
	fields, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err != nil {
		t.Fatalf("Autofill: %v", err)
	}
	if fields.Name != "bolt" {
		t.Errorf("Name = %q, want bolt", fields.Name)
	}
	if fields.Qty == nil || *fields.Qty != 3 {
		t.Errorf("Qty = %v, want *3", fields.Qty)
	}
}

func TestAutofillExtraAndMissingKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentReply(w, `{"description":"x","qty":null,"tags":["a"],"junk":1}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	fields, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err != nil {
		t.Fatalf("Autofill: %v", err)
	}
	if fields.Name != "" {
		t.Errorf("Name = %q, want empty", fields.Name)
	}
	if fields.Description != "x" {
		t.Errorf("Description = %q, want x", fields.Description)
	}
	if fields.Qty != nil {
		t.Errorf("Qty = %v, want nil for explicit null", fields.Qty)
	}
	if fields.Unit != "" {
		t.Errorf("Unit = %q, want empty", fields.Unit)
	}
	if len(fields.Tags) != 1 || fields.Tags[0] != "a" {
		t.Errorf("Tags = %v, want [a]", fields.Tags)
	}
}

func TestAutofillProviderError(t *testing.T) {
	// Juicy body the error must NOT leak.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"sk-live-SUPER-SECRET quota exhausted, billing acct 42"}}`))
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	_, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err == nil {
		t.Fatal("Autofill succeeded, want error on 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention status", err)
	}
	if strings.Contains(err.Error(), "SUPER-SECRET") {
		t.Errorf("error %q leaks provider response body", err)
	}
	// The body IS captured — but only for logs (ErrorDetail), never in
	// the client-facing Error() string.
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error %v is not *HTTPError", err)
	}
	if !strings.Contains(httpErr.ErrorDetail(), "SUPER-SECRET") {
		t.Errorf("ErrorDetail = %q, want the provider body excerpt", httpErr.ErrorDetail())
	}
}

func TestAutofillMalformedContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentReply(w, "the item is a mixed lot of screws and washers")
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	_, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err == nil {
		t.Fatal("Autofill succeeded, want error on unparseable content")
	}
	if !strings.Contains(err.Error(), "unparseable") {
		t.Errorf("error %q does not say unparseable", err)
	}
	// the typed sentinel drives the handler's 400 ai_bad_response mapping
	if !errors.Is(err, ErrUnparseable) {
		t.Errorf("error %q does not satisfy errors.Is(err, ErrUnparseable)", err)
	}
}

func TestAutofillTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		contentReply(w, `{"name":"late"}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	c.HTTP.Timeout = 50 * time.Millisecond // inject short deadline
	_, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err == nil {
		t.Fatal("Autofill succeeded, want timeout error")
	}
}

func TestAutofillContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		contentReply(w, `{"name":"late"}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Autofill(ctx, [][]byte{testPNG(1)}, []string{"image/png"})
	if err == nil {
		t.Fatal("Autofill succeeded, want error on cancelled context")
	}
}

// The compatibility ladder: a provider that 400s the modern
// max_completion_tokens parameter (older OpenAI-compatible servers, e.g.
// some ollama/vLLM builds, only know legacy max_tokens) gets ONE retry
// with max_tokens — same request otherwise — and the answer flows through.
func TestAutofillMaxTokensFallback(t *testing.T) {
	var log envelopeLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := log.add(r)
		if env.MaxCompletionTokens != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"max_completion_tokens is not supported"}}`)
			return
		}
		contentReply(w, `{"name":"bolt","qty":3}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	fields, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err != nil {
		t.Fatalf("Autofill: %v", err)
	}
	if fields.Name != "bolt" {
		t.Errorf("Name = %q, want bolt", fields.Name)
	}

	reqs := log.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("provider saw %d requests, want 2 (modern attempt + legacy retry)", len(reqs))
	}
	if reqs[0].MaxCompletionTokens == nil || *reqs[0].MaxCompletionTokens != 4000 {
		t.Errorf("attempt 1 max_completion_tokens = %v, want 4000", reqs[0].MaxCompletionTokens)
	}
	if reqs[0].MaxTokens != nil {
		t.Errorf("attempt 1 also sent max_tokens = %d", *reqs[0].MaxTokens)
	}
	if reqs[1].MaxTokens == nil || *reqs[1].MaxTokens != 4000 {
		t.Errorf("retry max_tokens = %v, want 4000", reqs[1].MaxTokens)
	}
	if reqs[1].MaxCompletionTokens != nil {
		t.Errorf("retry also sent max_completion_tokens = %d", *reqs[1].MaxCompletionTokens)
	}
	// "Same everything else": the retry is the first request with only
	// the parameter name swapped.
	if reqs[1].Model != reqs[0].Model || len(reqs[1].Messages) != len(reqs[0].Messages) {
		t.Errorf("retry changed more than the token parameter: model %q→%q, messages %d→%d",
			reqs[0].Model, reqs[1].Model, len(reqs[0].Messages), len(reqs[1].Messages))
	}
}

// A provider that accepts max_completion_tokens is hit exactly once — no
// fallback request is ever sent.
func TestAutofillNoFallbackWhenModernParamAccepted(t *testing.T) {
	var log envelopeLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := log.add(r)
		if env.MaxCompletionTokens == nil {
			t.Errorf("fake provider: request without max_completion_tokens")
		}
		contentReply(w, `{"name":"x"}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	if _, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"}); err != nil {
		t.Fatalf("Autofill: %v", err)
	}
	if n := len(log.snapshot()); n != 1 {
		t.Errorf("provider saw %d requests, want 1 (no fallback when the modern parameter is accepted)", n)
	}
}

// 401 is not a parameter problem: no retry, the typed error surfaces at
// once.
func TestAutofillNoFallbackOn401(t *testing.T) {
	var log envelopeLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	_, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err == nil {
		t.Fatal("Autofill succeeded, want error on 401")
	}
	if n := len(log.snapshot()); n != 1 {
		t.Fatalf("provider saw %d requests, want 1 (no retry on non-400)", n)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusUnauthorized {
		t.Errorf("error %v is not an *HTTPError with status 401", err)
	}
}

// When both rungs 400, the RETRY's error wins.
func TestAutofillFallbackBothFail(t *testing.T) {
	var log envelopeLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := log.add(r)
		w.WriteHeader(http.StatusBadRequest)
		if env.MaxCompletionTokens != nil {
			_, _ = io.WriteString(w, `{"error":{"message":"modern param rejected"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"error":{"message":"legacy param also rejected"}}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	_, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err == nil {
		t.Fatal("Autofill succeeded, want error when both rungs 400")
	}
	if n := len(log.snapshot()); n != 2 {
		t.Fatalf("provider saw %d requests, want 2", n)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error %v is not *HTTPError", err)
	}
	if d := httpErr.ErrorDetail(); !strings.Contains(d, "legacy param also rejected") {
		t.Errorf("ErrorDetail = %q, want the retry's body (the retry's error wins)", d)
	}
	if s := err.Error(); strings.Contains(s, "legacy param") || strings.Contains(s, "modern param") {
		t.Errorf("Error() %q leaks provider body", s)
	}
}

// HTTPError detail: whitespace-collapsed and capped at 512 bytes; Error()
// stays status-only.
func TestHTTPErrorDetailCollapsedAndTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "line1\n\tline2  "+strings.Repeat("x", 2000)+"\n")
	}))
	defer srv.Close()

	c := New("k", srv.URL, "test-model")
	_, err := c.Autofill(context.Background(), [][]byte{testPNG(1)}, []string{"image/png"})
	if err == nil {
		t.Fatal("Autofill succeeded, want error on 500")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error %v is not *HTTPError", err)
	}
	d := httpErr.ErrorDetail()
	if len(d) > 512 {
		t.Errorf("detail len = %d, want ≤512", len(d))
	}
	// 2KB of body → 2KB read → collapse → 512-byte cut: "line1 line2 "
	// (12 bytes) + exactly 500 x's.
	if want := "line1 line2 " + strings.Repeat("x", 500); d != want {
		t.Errorf("detail = %q…, want collapsed prefix %q…", d[:40], want[:40])
	}
	if s := err.Error(); strings.Contains(s, "line1") || !strings.Contains(s, "500") {
		t.Errorf("Error() = %q, want status-only mentioning 500", s)
	}
}
