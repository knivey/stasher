# AI Autofill: Photo → Pre-filled Item Fields

**Date:** 2026-10-23
**Status:** Approved design (pre-implementation)

## 1. Purpose

Adding items by hand is the biggest friction in a home inventory. This
feature lets the user photograph an item — often several photos: a closeup
of an IC's markings, a wider shot of the whole lot — and have a
vision-capable AI propose field values that prefill the add/edit form for
review. Nothing is ever saved by the AI itself; every field stays
user-editable before the normal save flow.

The AI service is operator-configured, off by default, and speaks the
OpenAI-compatible chat-completions protocol (works with OpenAI, OpenRouter,
Groq, Together, and local ollama/vLLM/llama.cpp servers).

Success criteria:

- Operator sets `ai_api_key` + `ai_model` (optionally `ai_base_url`) →
  an "Autofill from photos" button appears on the add form once photos are
  queued and on the edit form over the item's existing photos.
- Picking a closeup + lot photo returns one reconciled field set — name
  from the markings, qty from the lot shot — prefilled into the form.
- With no key configured, the feature is simply absent/disabled (clear
  error, no button or a disabled state with hint).
- Item photos leave the server only toward the configured AI endpoint;
  the base URL is operator config, not user input.

## 2. Configuration

Flat keys following the `brave_api_key` pattern (`internal/config`), env
overrides `STASHER_AI_API_KEY` / `STASHER_AI_BASE_URL` / `STASHER_AI_MODEL`:

- `ai_api_key` — empty (default) disables the feature entirely.
- `ai_base_url` — default `https://api.openai.com/v1`.
- `ai_model` — REQUIRED when `ai_api_key` is set; no vendor-specific
  default (a wrong-vendor default model name errors confusingly). Key
  without model logs a startup warning; the endpoint returns
  `400 ai_not_configured` with a message naming the missing key.

**Trusted-endpoint distinction (binding):** `ai_base_url` is operator
configuration. The AI client uses a plain HTTP client with NO SSRF
PublicPolicy — loopback and private ranges MUST be reachable so local
ollama/vLLM work. This is deliberately unlike bookmark/fetch URLs (user
input, full policy); the code comments must say so.

## 3. `internal/ai` package

```go
type Fields struct {
    Name        string
    Description string
    Qty         *float64 // nil = not estimated
    Unit        string
    Tags        []string
}
type Client struct { /* BaseURL, APIKey, Model, HTTP *http.Client */ }
func (c *Client) Enabled() bool
func (c *Client) Autofill(ctx context.Context, images [][]byte) (Fields, error)
```

- Request: `POST {base}/chat/completions`, `Authorization: Bearer <key>`,
  one user message with fixed server-side instruction text + N image
  parts (`image_url` with base64 `data:image/jpeg;base64,…` URLs —
  sniffed mime per image, png allowed too), `max_tokens` bounded (~1000).
  **No `response_format` assumption** — not universal across compat
  providers; the prompt demands strict JSON instead.
- Response handling: 64KB body cap, JSON envelope → `choices[0].message.content`
  → robust content parse: trim, strip ```json fences if present, decode
  `{"name","description","qty","unit","tags"}` with every field OPTIONAL.
  Malformed content → error (`ai_bad_response` at the handler).
- Errors: non-2xx from provider → wrapped error carrying the status; the
  handler maps to `502 ai_upstream_error` with a GENERIC message — the
  provider's response body is never echoed to the client (it may contain
  account details). Network/timeout → same 502 mapping. 45s overall
  timeout.
- Images: `[][]byte` full bytes; the CLIENT-side downscale (add form) or
  the stored original (edit form) are sent as-is; the API does no
  server-side re-encode.

## 4. Endpoint

`POST /api/v1/autofill` — authenticated, not item-associated:

- Multipart with **1–4** `files` parts, each an image: sniffed via
  `http.DetectContentType` (client Content-Type ignored), `image/*` only
  (415 otherwise), each part ≤ `maxPhotoBytes` (20MB), total request
  bounded by MaxBytesReader at 4× that. Zero valid parts → 400
  `validation_error`.
- Per-user rate limit **1 per 5s** (reuse the auth.Limiter pattern;
  key "ai:"+userID) → 429 `rate_limited`.
- Unconfigured (no key, or key without model) → `400 ai_not_configured`
  with a message naming what's missing.
- **Server-side clamping** (AI output is not user input to re-submit —
  clamp, don't reject): name trimmed to 1–200 chars (empty → omitted);
  description truncated at 10,000 (matching the client maxlength); qty
  only accepted when finite and 0..1e12; unit ≤50 chars; tags filtered to
  1–100 chars each, deduplicated (case-insensitive), capped at 10, else
  omitted.
- Response `200 {name?, description?, qty?, unit?, tags?}` — only fields
  the model returned (post-clamp); every key may be absent.

## 5. Frontend

One affordance, two sources (ItemFormView serves both add and edit):

- **Add form**: once photos are queued, show "✨ Autofill from photos"
  (uses the first ≤4 queued blobs — they're all photos of this item; a
  muted hint states the count, and "first 4" when more are queued).
  Disabled while analyzing; spinner + "Analyzing…" text.
- **Edit form**: the same button opens a **multi-select thumbnail picker**
  of the item's existing photos (`thumb_key ?? storage_key` as thumbs;
  click to toggle; badge shows selected count; max 4; "Analyze" enabled
  with ≥1 selected). The chosen photos' ORIGINAL bytes are fetched from
  `/files/{storage_key}` (same-origin, session cookie) and posted.
- On success, returned fields are written into the form (marking each
  touched field with a subtle "✨" suffix hint in its label until
  manually edited); a toast says what was filled; the user saves via the
  normal flow. **No auto-save, no auto-navigation.**
- Errors toast (`ai_not_configured` → "Set ai_api_key/ai_model in config
  to enable autofill"; rate limit → its message).
- No `v-html`; fetch stays the single API boundary.

## 6. Privacy & docs

Off by default. `stasher.yaml.example` + `docs/INSTALL.md`: enabling means
item photos are sent to the configured AI service (name the keys, note
local-model option via `ai_base_url`). `docs/api-notes.md`: endpoint
contract, clamps, rate limit, error codes.

## 7. Testing

- `internal/ai`: httptest fake provider — happy path (clean JSON), fenced
  JSON, extra keys ignored, missing fields → zero-value Fields, non-2xx →
  error, malformed content → `ai_bad_response`-style error, request shape
  pinned (auth header, model, image parts count, no response_format).
- `internal/api`: endpoint — happy (2 images → fields), clamps (oversized
  name truncated, invalid tags filtered, qty out-of-range dropped),
  415 non-image, 400 zero parts / 5 parts, 400 unconfigured, 429 second
  call, 502 provider down (fake AI server closed), 401 unauthenticated.
- `internal/config`: three keys — defaults, yaml, env override, empty-env
  fallback.
- Frontend (vitest, fetch mocked at client boundary): add-form button
  appears with queued photos + posts all queued (≤4); edit-form picker
  toggles/photos fetched from /files + posted; prefill writes fields +
  ✨ hints; ai_not_configured toast; disabled-while-analyzing states.

## 8. Non-goals

Location suggestion (personal tree, hallucination-prone — later if
asked); auto-save; per-vendor native adapters; batch/folder analysis;
persisting AI results or provenance; AI-suggested descriptions for
documents; configurable prompts.
