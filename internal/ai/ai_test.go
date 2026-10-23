package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
type reqEnvelope struct {
	Model          string          `json:"model"`
	MaxTokens      int             `json:"max_tokens"`
	ResponseFormat json.RawMessage `json:"response_format"`
	Messages       []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
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
	if got.MaxTokens <= 0 || got.MaxTokens > 1000 {
		t.Errorf("max_tokens = %d, want 1..1000", got.MaxTokens)
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
