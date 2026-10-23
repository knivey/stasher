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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
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

// ErrUnparseable is wrapped (via %w) by every error raised because the
// provider's ANSWER could not be turned into Fields — both a model reply
// that isn't JSON and a response with no content at all. The HTTP handler
// discriminates on it with errors.Is to answer 400 ai_bad_response;
// every other client error (transport, non-2xx, oversized response) is a
// 502 ai_upstream_error. Typed sentinel, not a text match: callers must
// never string-compare error text.
var ErrUnparseable = errors.New("ai: unparseable model response")

// HTTPError is a non-2xx answer from the provider. Error() stays generic
// (status only — no body) because the string is client-safe; the
// whitespace-collapsed, size-capped provider body excerpt in Detail is
// FOR LOGGING ONLY (via ErrorDetail): provider bodies may leak account
// details and must never reach a client response.
type HTTPError struct {
	Status int
	Detail string
}

func (e *HTTPError) Error() string {
	if text := http.StatusText(e.Status); text != "" {
		return fmt.Sprintf("ai: provider returned %d %s", e.Status, text)
	}
	return fmt.Sprintf("ai: provider returned status %d", e.Status)
}

// ErrorDetail exposes the truncated provider body for server-side logs.
func (e *HTTPError) ErrorDetail() string { return e.Detail }

const (
	// requestTimeout bounds one autofill call end to end.
	requestTimeout = 45 * time.Second
	// maxCompletionTokens bounds the completion budget. Reasoning models
	// spend hidden reasoning tokens against the same budget, so the old
	// ~1000 cap risked burning the whole budget before any content came
	// out (empty/truncated JSON); 4000 leaves headroom while staying
	// bounded.
	maxCompletionTokens = 4000
	// maxResponse caps the provider response body read (64KB).
	maxResponse = 64 << 10
	// maxErrorBody caps how much of a non-2xx provider body is read for
	// the log-only HTTPError detail (2KB).
	maxErrorBody = 2 << 10
	// maxErrorDetail caps the collapsed detail string (bytes).
	maxErrorDetail = 512
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
// an "ai:" prefix. Non-2xx answers come back as *HTTPError whose Error()
// is status-only; the provider body excerpt is exposed solely through
// ErrorDetail for server-side logging.
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
	msgs := []wireMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: parts},
	}

	// Compatibility ladder, rung 1: the modern parameter. OpenAI's newer
	// reasoning models REJECT max_tokens outright (400 "unsupported
	// parameter") — max_completion_tokens is the Chat Completions
	// parameter and OpenAI maps it across all current models — so it
	// goes first.
	budget := maxCompletionTokens
	body, err := json.Marshal(chatRequest{
		Model:               c.Model,
		Messages:            msgs,
		MaxCompletionTokens: &budget,
	})
	if err != nil {
		return Fields{}, fmt.Errorf("ai: encoding request: %w", err)
	}
	fields, err := c.complete(ctx, body)
	if err == nil {
		return fields, nil
	}

	// Rung 2, only on 400: some older OpenAI-compatible servers (certain
	// ollama/vLLM builds) know ONLY legacy max_tokens and 400 on the new
	// name. A 400 is the one status that can mean "unrecognized
	// parameter", so retry ONCE with max_tokens — same request otherwise.
	// Any other status means something unrelated to the parameter name
	// and fails immediately. If the retry also fails, its error wins.
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.Status == http.StatusBadRequest {
		body, err := json.Marshal(chatRequest{
			Model:     c.Model,
			Messages:  msgs,
			MaxTokens: &budget,
		})
		if err != nil {
			return Fields{}, fmt.Errorf("ai: encoding request: %w", err)
		}
		return c.complete(ctx, body)
	}
	return Fields{}, err
}

// complete performs one chat-completions round trip with the given
// serialized request body and parses the answer into Fields.
func (c *Client) complete(ctx context.Context, body []byte) (Fields, error) {
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
		// Bounded read of the provider body for the LOG-ONLY detail
		// (captured even on the pre-fallback attempt, so a first-rung
		// 400 is diagnosable when the retry succeeds); the returned
		// error's client-facing string stays status-only because the
		// body may leak account details — the HTTP handler maps it to a
		// generic 502 message.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return Fields{}, &HTTPError{
			Status: resp.StatusCode,
			Detail: collapseDetail(string(detail)),
		}
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
		return Fields{}, fmt.Errorf("%w: no content", ErrUnparseable)
	}

	// Content is a JSON object possibly wrapped in ``` fences by
	// less-disciplined providers; every field optional, unknown keys
	// ignored, explicit null qty → nil.
	var f fieldsJSON
	if err := json.Unmarshal([]byte(stripFences(env.Choices[0].Message.Content)), &f); err != nil {
		// double %w: the sentinel drives the handler's 400 mapping while
		// the json error stays unwrappable for diagnostics
		return Fields{}, fmt.Errorf("%w: %w", ErrUnparseable, err)
	}
	return Fields(f), nil
}

// collapseDetail squeezes whitespace runs to single spaces and caps the
// result at maxErrorDetail bytes (never splitting a UTF-8 rune) so
// provider error bodies stay log-friendly.
func collapseDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxErrorDetail {
		return s
	}
	cut := maxErrorDetail
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
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
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	// Exactly one token-budget field is set per request: the modern
	// max_completion_tokens on the first attempt, legacy max_tokens on
	// the one-shot 400 fallback for older OpenAI-compatible servers.
	MaxCompletionTokens *int `json:"max_completion_tokens,omitempty"`
	MaxTokens           *int `json:"max_tokens,omitempty"`
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
