package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"stasher/internal/ai"
)

// aiPNG returns bytes carrying the PNG magic — enough for the endpoint's
// 512-byte sniff (same trick as the client tests in internal/ai; distinct
// from project_files_test.go's 1x1 pngBytes fixture).
func aiPNG(b ...byte) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, b...)
}

// autofillMultipart builds a multipart body whose "files" field carries one
// file part per slice. The endpoint sniffs part types (client part
// Content-Types are ignored), so CreateFormFile's octet-stream is fine.
func autofillMultipart(t *testing.T, parts ...[]byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i, p := range parts {
		fw, err := mw.CreateFormFile("files", fmt.Sprintf("img%d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

// doAutofill POSTs /api/v1/autofill with the parts as "files" file parts.
func doAutofill(t *testing.T, ts *httptest.Server, cookie *http.Cookie, parts ...[]byte) *http.Response {
	t.Helper()
	rd, ct := autofillMultipart(t, parts...)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/autofill", rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", ct)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// streamAutofill posts one "files" part of exactly size bytes, streamed
// through a pipe so multi-80MB oversize payloads never sit in memory.
func streamAutofill(t *testing.T, ts *httptest.Server, cookie *http.Cookie, size int) *http.Response {
	t.Helper()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		fw, err := mw.CreateFormFile("files", "big.png")
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		chunk := bytes.Repeat([]byte{0}, 1<<20)
		for written := 0; written < size; {
			n := min(len(chunk), size-written)
			if _, err := fw.Write(chunk[:n]); err != nil {
				pw.CloseWithError(err)
				return
			}
			written += n
		}
		if err := mw.Close(); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/autofill", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// autofillErr decodes {"error":{"code","message"}} and closes the body.
func autofillErr(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	defer resp.Body.Close()
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return e.Error.Code, e.Error.Message
}

// fakeAIProvider is an OpenAI-compatible chat-completions endpoint: 2xx
// replies with one choice whose content is the given string; any other
// status writes status + body raw (for provider-failure tests).
func fakeAIProvider(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status >= 200 && status < 300 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{"content": body}}},
			})
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wireAI points the server's AI client at the fake provider (mirrors how
// search tests swap s.searcher after newTestServer).
func wireAI(s *Server, fake *httptest.Server) {
	s.aiClient = ai.New("test-key", fake.URL, "test-model")
}

// autofillResp is the 200 body; every key is optional (absent keys decode
// to zero values, qty absent → nil pointer).
type autofillResp struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Qty         *float64 `json:"qty"`
	Unit        string   `json:"unit"`
	Tags        []string `json:"tags"`
}

// 1. Happy: 2 PNG parts → 200 with the fields forwarded; both images
// actually reach the provider.
func TestAutofillHappy(t *testing.T) {
	s, ts := newTestServer(t, nil)
	var imageParts atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("fake provider: read request: %v", err)
		}
		var env struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Errorf("fake provider: request not JSON: %v", err)
		}
		for _, m := range env.Messages {
			if m.Role != "user" {
				continue
			}
			var parts []struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(m.Content, &parts); err != nil {
				t.Errorf("fake provider: user content not a parts array: %v", err)
				continue
			}
			for _, p := range parts {
				if p.Type == "image_url" {
					imageParts.Add(1)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{
				"content": `{"name":"LM358 op-amp","description":"Dual op-amp","qty":5.5,"unit":"pcs","tags":["op-amp","dip-8"]}`,
			}}},
		})
	}))
	t.Cleanup(fake.Close)
	wireAI(s, fake)
	cookie := registerUser(t, ts, "ai-happy@test.dev", "longpassword1")

	resp := doAutofill(t, ts, cookie, aiPNG(1), aiPNG(2))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("autofill = %d, want 200", resp.StatusCode)
	}
	var got autofillResp
	decodeBody(t, resp, &got)
	if got.Name != "LM358 op-amp" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Description != "Dual op-amp" {
		t.Errorf("description = %q", got.Description)
	}
	if got.Qty == nil || *got.Qty != 5.5 {
		t.Errorf("qty = %v, want 5.5", got.Qty)
	}
	if got.Unit != "pcs" {
		t.Errorf("unit = %q", got.Unit)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "op-amp" || got.Tags[1] != "dip-8" {
		t.Errorf("tags = %v", got.Tags)
	}
	if n := imageParts.Load(); n != 2 {
		t.Errorf("provider received %d image parts, want 2 (both parts forwarded whole)", n)
	}
}

// 2. Clamps: over-limit values come back clamped or omitted, never 5xx.
func TestAutofillClamps(t *testing.T) {
	s, ts := newTestServer(t, nil)
	content := fmt.Sprintf(`{"name":%s,"description":%s,"qty":2e12,"unit":%s,"tags":["ok","",%s,"Ok","Ok"]}`,
		jsonString(strings.Repeat("n", 250)),
		jsonString(strings.Repeat("d", 10001)),
		jsonString(strings.Repeat("u", 60)),
		jsonString(strings.Repeat("x", 101)))
	wireAI(s, fakeAIProvider(t, http.StatusOK, content))
	cookie := registerUser(t, ts, "ai-clamp@test.dev", "longpassword1")

	resp := doAutofill(t, ts, cookie, aiPNG(1))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("autofill = %d, want 200", resp.StatusCode)
	}
	var got autofillResp
	decodeBody(t, resp, &got)
	if len(got.Name) != 200 {
		t.Errorf("name len = %d, want 200 (truncated)", len(got.Name))
	}
	if got.Name != strings.Repeat("n", 200) {
		t.Errorf("name = %q…", got.Name[:20])
	}
	if len(got.Description) != 10000 {
		t.Errorf("description len = %d, want 10000 (truncated)", len(got.Description))
	}
	if got.Qty != nil {
		t.Errorf("qty = %v, want absent (2e12 out of 0..1e12)", *got.Qty)
	}
	if len(got.Unit) != 50 {
		t.Errorf("unit len = %d, want 50 (truncated)", len(got.Unit))
	}
	// "" and the 101-char tag are dropped; "Ok"/"Ok" fold into "ok"
	// (case-insensitive dedup keeps the first-seen casing).
	if len(got.Tags) != 1 || got.Tags[0] != "ok" {
		t.Errorf("tags = %v, want [ok]", got.Tags)
	}
}

// 3. Part-count guards: zero parts and more than four are 400s that fire
// BEFORE the rate limit (no token burned on invalid requests).
func TestAutofillPartCount(t *testing.T) {
	s, ts := newTestServer(t, nil)
	wireAI(s, fakeAIProvider(t, http.StatusOK, `{}`))
	cookie := registerUser(t, ts, "ai-count@test.dev", "longpassword1")

	resp := doAutofill(t, ts, cookie) // zero parts
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("zero parts = %d, want 400", resp.StatusCode)
	}
	if code, _ := autofillErr(t, resp); code != "validation_error" {
		t.Errorf("zero parts code = %q, want validation_error", code)
	}

	five := [][]byte{aiPNG(1), aiPNG(2), aiPNG(3), aiPNG(4), aiPNG(5)}
	resp = doAutofill(t, ts, cookie, five...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("five parts = %d, want 400", resp.StatusCode)
	}
	if code, _ := autofillErr(t, resp); code != "validation_error" {
		t.Errorf("five parts code = %q, want validation_error", code)
	}

	// "files" sent as a plain form VALUE instead of file parts →
	// missing_file (the field exists but carries no uploads).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("files", "not-a-file"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/autofill", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	vresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if vresp.StatusCode != http.StatusBadRequest {
		t.Fatalf("files-as-value = %d, want 400", vresp.StatusCode)
	}
	if code, _ := autofillErr(t, vresp); code != "missing_file" {
		t.Errorf("files-as-value code = %q, want missing_file", code)
	}
}

// 4. Non-image parts: sniffed type must be an allowed photo type; one bad
// part rejects the whole request (no partial analysis).
func TestAutofillNonImage(t *testing.T) {
	s, ts := newTestServer(t, nil)
	wireAI(s, fakeAIProvider(t, http.StatusOK, `{}`))
	cookie := registerUser(t, ts, "ai-mime@test.dev", "longpassword1")

	for name, parts := range map[string][][]byte{
		"single text part": {[]byte("just some plain text that sniffs as text/plain")},
		"valid plus text":  {aiPNG(1), []byte("also plain text")},
	} {
		resp := doAutofill(t, ts, cookie, parts...)
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%s = %d, want 415", name, resp.StatusCode)
			resp.Body.Close()
			continue
		}
		if code, _ := autofillErr(t, resp); code != "unsupported_type" {
			t.Errorf("%s code = %q, want unsupported_type", name, code)
		}
	}
}

// 5. Oversize: one part over the 20MB per-part cap, and a body over the
// 4×20MB request cap (MaxBytesReader trips mid-parse) — both 400
// invalid_upload, exactly like photo uploads.
func TestAutofillOversize(t *testing.T) {
	s, ts := newTestServer(t, nil)
	wireAI(s, fakeAIProvider(t, http.StatusOK, `{}`))
	cookie := registerUser(t, ts, "ai-big@test.dev", "longpassword1")

	big := make([]byte, maxPhotoBytes+1) // per-part cap; size checked before sniff
	copy(big, aiPNG())
	resp := doAutofill(t, ts, cookie, big)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("21MB part = %d, want 400", resp.StatusCode)
	}
	if code, _ := autofillErr(t, resp); code != "invalid_upload" {
		t.Errorf("21MB part code = %q, want invalid_upload", code)
	}

	// Per-user rate limit is 1/5s and the first (rejected) request never
	// reached it — validation runs first — so a second user is not needed;
	// stream an 80MB+1 body to trip the whole-request reader.
	resp = streamAutofill(t, ts, cookie, 4*maxPhotoBytes+1)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("80MB+1 body = %d, want 400", resp.StatusCode)
	}
	if code, _ := autofillErr(t, resp); code != "invalid_upload" {
		t.Errorf("80MB+1 body code = %q, want invalid_upload", code)
	}
}

// 6. Unconfigured: the message names exactly which keys are missing.
func TestAutofillNotConfigured(t *testing.T) {
	for _, tc := range []struct {
		name          string
		client        *ai.Client
		wantMentions  []string
		notMentioneds []string
	}{
		{"nothing configured", ai.New("", "", ""), []string{"ai_api_key", "ai_model"}, nil},
		{"key without model", ai.New("k", "", ""), []string{"ai_model"}, []string{"ai_api_key"}},
	} {
		s, ts := newTestServer(t, nil) // fresh server: distinct users (rate key)
		s.aiClient = tc.client
		cookie := registerUser(t, ts, "ai-off-"+tc.name+"@test.dev", "longpassword1")
		resp := doAutofill(t, ts, cookie, aiPNG(1))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", tc.name, resp.StatusCode)
		}
		code, msg := autofillErr(t, resp)
		if code != "ai_not_configured" {
			t.Errorf("%s code = %q, want ai_not_configured", tc.name, code)
		}
		for _, want := range tc.wantMentions {
			if !strings.Contains(msg, want) {
				t.Errorf("%s message %q does not name %s", tc.name, msg, want)
			}
		}
		for _, not := range tc.notMentioneds {
			if strings.Contains(msg, not) {
				t.Errorf("%s message %q needlessly names %s", tc.name, msg, not)
			}
		}
	}
}

// 7. Rate limit: 1 per 5s per user — the second immediate call 429s while
// the first succeeded against the fake provider.
func TestAutofillRateLimited(t *testing.T) {
	s, ts := newTestServer(t, nil)
	wireAI(s, fakeAIProvider(t, http.StatusOK, `{"name":"x"}`))
	cookie := registerUser(t, ts, "ai-rate@test.dev", "longpassword1")

	first := doAutofill(t, ts, cookie, aiPNG(1))
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first call = %d, want 200", first.StatusCode)
	}
	first.Body.Close()

	second := doAutofill(t, ts, cookie, aiPNG(2))
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second call = %d, want 429", second.StatusCode)
	}
	if code, _ := autofillErr(t, second); code != "rate_limited" {
		t.Errorf("second call code = %q, want rate_limited", code)
	}
}

// captureSlog swaps the default slog logger for a buffered text handler so
// tests can assert on warn lines, restoring the previous default after.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// 8. Provider 500 → 502 ai_upstream_error with a generic message; the
// provider's body (which may carry account details) is never echoed to the
// client — but its truncated detail IS on the server's warn line.
func TestAutofillUpstreamError(t *testing.T) {
	s, ts := newTestServer(t, nil)
	juicy := `{"error":{"message":"sk-live-SUPER-SECRET quota exhausted, billing acct 42"}}`
	wireAI(s, fakeAIProvider(t, http.StatusInternalServerError, juicy))
	cookie := registerUser(t, ts, "ai-500@test.dev", "longpassword1")

	logs := captureSlog(t)
	resp := doAutofill(t, ts, cookie, aiPNG(1))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("provider 500 = %d, want 502", resp.StatusCode)
	}
	code, msg := autofillErr(t, resp)
	if code != "ai_upstream_error" {
		t.Errorf("code = %q, want ai_upstream_error", code)
	}
	if strings.Contains(msg, "SUPER-SECRET") || strings.Contains(msg, juicy) {
		t.Errorf("message %q leaks the provider response body", msg)
	}

	logStr := logs.String()
	if !strings.Contains(logStr, "ai autofill failed") {
		t.Errorf("log %q lacks the warn line", logStr)
	}
	if !strings.Contains(logStr, "provider_detail") {
		t.Errorf("log %q lacks the provider_detail attribute", logStr)
	}
	if !strings.Contains(logStr, "SUPER-SECRET") {
		t.Errorf("log %q does not carry the provider body detail (server-side only)", logStr)
	}
}

// 9. Provider 200 whose content is not parseable JSON → 400 ai_bad_response.
func TestAutofillBadResponse(t *testing.T) {
	s, ts := newTestServer(t, nil)
	wireAI(s, fakeAIProvider(t, http.StatusOK, "the item is a mixed lot of screws and washers"))
	cookie := registerUser(t, ts, "ai-bad@test.dev", "longpassword1")

	resp := doAutofill(t, ts, cookie, aiPNG(1))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed content = %d, want 400", resp.StatusCode)
	}
	if code, _ := autofillErr(t, resp); code != "ai_bad_response" {
		t.Errorf("code = %q, want ai_bad_response", code)
	}
}

// 10. Unauthenticated → 401 (the route lives inside the authed group).
func TestAutofillUnauthenticated(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doAutofill(t, ts, nil, aiPNG(1))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no cookie = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}
