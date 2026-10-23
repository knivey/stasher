// Package ai is a minimal client for OpenAI-compatible chat-completions
// vision endpoints (OpenAI, OpenRouter, Groq, Together, local
// ollama/vLLM/llama.cpp) backing the "Autofill from photos" feature.
// It is a pure client — stdlib only, no repo dependencies — turning item
// photos into one proposed Fields set for the user to review. Nothing is
// saved here.
package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fields is one AI-proposed set of item values. Every field is optional:
// zero values (and nil Qty) mean "not estimated". Server-side clamping
// happens at the API layer (AI output is clamped, not trusted).
type Fields struct {
	Name        string
	Description string
	Qty         *float64 // nil = not estimated
	Unit        string
	Tags        []string
}

// DefaultBaseURL is the stock OpenAI endpoint, used when no ai_base_url
// is configured.
const DefaultBaseURL = "https://api.openai.com/v1"

const (
	// requestTimeout bounds one autofill call end to end.
	requestTimeout = 45 * time.Second
	// maxTokens bounds the completion (~1000 per spec: the JSON object
	// is tiny; the cap keeps a runaway model cheap).
	maxTokens = 1000
	// maxResponse caps the provider response body read (64KB).
	maxResponse = 64 << 10
)

// systemPrompt and userPrompt are fixed server-side instruction text.
// No response_format is sent — not universal across OpenAI-compatible
// providers — so the prompt itself demands strict JSON with no markdown.
const systemPrompt = `You extract inventory field values from photos of a single item.
Reply with ONLY one JSON object and nothing else: no markdown, no code fences, no commentary.
Every field is optional: include a key only when the photos support an estimate.
Shape: {"name": string, "description": string, "qty": number, "unit": string, "tags": [string]}
name: short human-readable label; include part numbers when markings are legible.
description: one or two sentences.
qty: a plain number (the count), never a string.
unit: a short unit such as "pcs", "m", "kg".
tags: at most 10 short lowercase tags.`

const userPrompt = `Identify the item shown in these photos and reply with the JSON object only.`

// Client talks to one OpenAI-compatible chat-completions endpoint.
//
// TRUSTED ENDPOINT: BaseURL is operator configuration (ai_base_url), not
// user input, so this deliberately uses a plain http.Client with NO SSRF
// address policy — unlike package fetcher (user-supplied URLs, full IP
// policy). Loopback and private ranges MUST remain reachable so local
// ollama/vLLM/llama.cpp servers work. Item photos are only ever sent to
// this operator-configured URL.
type Client struct {
	BaseURL string // default applied by New; trailing "/" tolerated
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// New returns a Client with the standard 45s timeout. An empty baseURL
// falls back to DefaultBaseURL (defensive: config.Load already applies
// the same default).
func New(apiKey, baseURL, model string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: requestTimeout},
	}
}

// Enabled reports whether the operator configured both an API key and a
// model. Without both, the feature is off (the endpoint answers
// ai_not_configured).
func (c *Client) Enabled() bool {
	return c.APIKey != "" && c.Model != ""
}

// Autofill sends the images (full bytes, as-is — no server-side
// re-encode; mimes are sniffed by the caller, one per image) to the
// chat-completions endpoint and returns the model's proposed fields.
// Every field is optional in the response; all errors are wrapped with
// an "ai:" prefix, and provider error bodies are never included.
func (c *Client) Autofill(ctx context.Context, images [][]byte, mimes []string) (Fields, error) {
	if len(images) != len(mimes) {
		return Fields{}, fmt.Errorf("ai: got %d images but %d mimes", len(images), len(mimes))
	}
	if !c.Enabled() {
		return Fields{}, fmt.Errorf("ai: not configured (api key and model required)")
	}

	// One user message: N image parts, then the instruction text part.
	parts := make([]contentPart, 0, len(images)+1)
	for i, img := range images {
		url := "data:" + mimes[i] + ";base64," + base64.StdEncoding.EncodeToString(img)
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: url}})
	}
	parts = append(parts, contentPart{Type: "text", Text: userPrompt})

	body, err := json.Marshal(chatRequest{
		Model: c.Model,
		Messages: []wireMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: parts},
		},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return Fields{}, fmt.Errorf("ai: encoding request: %w", err)
	}

	url := strings.TrimSuffix(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Fields{}, fmt.Errorf("ai: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Fields{}, fmt.Errorf("ai: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Status only — the provider's body may leak account details;
		// the HTTP handler maps this to a generic 502 message.
		return Fields{}, fmt.Errorf("ai: provider returned %s", resp.Status)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return Fields{}, fmt.Errorf("ai: reading response: %w", err)
	}
	if len(respBody) > maxResponse {
		return Fields{}, fmt.Errorf("ai: response exceeds %d bytes", maxResponse)
	}

	var env chatResponse
	if err := json.Unmarshal(respBody, &env); err != nil {
		return Fields{}, fmt.Errorf("ai: decoding response: %w", err)
	}
	if len(env.Choices) == 0 || strings.TrimSpace(env.Choices[0].Message.Content) == "" {
		return Fields{}, fmt.Errorf("ai: unparseable model response: no content")
	}

	// Content is a JSON object possibly wrapped in ``` fences by
	// less-disciplined providers; every field optional, unknown keys
	// ignored, explicit null qty → nil.
	var f fieldsJSON
	if err := json.Unmarshal([]byte(stripFences(env.Choices[0].Message.Content)), &f); err != nil {
		return Fields{}, fmt.Errorf("ai: unparseable model response: %w", err)
	}
	return Fields(f), nil
}

// stripFences trims whitespace and strips one pair of markdown code
// fences (``` or ```json … ```) when present.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return "" // opening fence with nothing after it
	}
	s = s[i+1:]
	if j := strings.LastIndex(s, "```"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// Wire types for the chat-completions protocol.

type imageURL struct {
	URL string `json:"url"`
}

type contentPart struct {
	Type     string    `json:"type"` // "text" | "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string (system) or []contentPart (user)
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []wireMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	// No response_format field: not universal across OpenAI-compatible
	// providers; the prompt demands strict JSON instead.
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// fieldsJSON is the wire shape of Fields; it converts to Fields 1:1.
type fieldsJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Qty         *float64 `json:"qty"`
	Unit        string   `json:"unit"`
	Tags        []string `json:"tags"`
}
