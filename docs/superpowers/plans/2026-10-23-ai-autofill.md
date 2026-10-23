# AI Autofill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Operator-configured OpenAI-compatible vision AI proposes item fields from 1–4 photos, prefilling the add/edit item form (never auto-saving).

**Architecture:** New `internal/ai` client (plain HTTP to the operator-configured base URL — no SSRF policy by design), one `POST /api/v1/autofill` endpoint (multipart multi-image, per-user rate limit, server-side clamping), and one ItemFormView affordance with two photo sources (add: queued blobs; edit: multi-select of existing photos).

**Tech Stack:** Go 1.27 stdlib + chi; Vue 3 + TS + Tailwind; vitest + happy-dom.

**Spec:** `docs/superpowers/specs/2026-10-23-ai-autofill-design.md` — binding authority; read both.

## Global Constraints

- Error shape `{"error":{"code","message"}}`; provider response bodies are NEVER echoed to clients; 404/403 discipline n/a (no per-item resource) but auth required.
- Config: flat yaml keys + `STASHER_AI_*` env overrides, the `brave_api_key` pattern (`internal/config/config.go:25` region). `ai_api_key` empty disables; `ai_model` required when key set (startup warn + endpoint `400 ai_not_configured`).
- `ai_base_url` is OPERATOR config: plain `http.Client`, **no SSRF policy — loopback/private must be reachable** (local ollama). Comment this distinction at the client.
- Limits: 1–4 image parts, each sniffed `image/*` (415 otherwise) and ≤ `maxPhotoBytes` (20MB), request MaxBytesReader at 4×20MB; per-user rate 1/5s (`auth.NewLimiter(12, 1)`); 45s provider timeout; 64KB response cap; `max_tokens` ~1000; NO `response_format` in the request.
- Clamps (not rejects): name trimmed 1–200; description ≤10,000; qty finite 0..1e12 else dropped; unit ≤50; tags each 1–100 chars, case-insensitive dedup, ≤10.
- RFC3339/Go timestamps n/a (no persistence — nothing is stored).
- Frontend: fetch single boundary; NO v-html; no empty catches; `vue-tsc -b`; dist-commit rule (`make web` + commit).
- TDD both sides; `make check` green at every commit.

## Review Focus

1. **Unconfigured states**: no key → `400 ai_not_configured`; key-without-model → same + startup warning; button states reflect config only via endpoint errors (frontend never guesses config).
2. **Multi-image boundary**: 0 parts → 400; 5 parts → 400; mixed valid+invalid part → 415 (whole request rejected, no partial analysis).
3. **Clamp correctness**: over-limit values come back clamped/omitted, never 500; empty name omitted; qty NaN/Inf/2e12 dropped.
4. **Provider failure mapping**: non-2xx/timeout/malformed → 502 `ai_upstream_error` / 400 `ai_bad_response` with generic messages; provider body never in response.
5. **No auto-save**: prefill only touches form state; the save flow is untouched (pin with a test that no POST /items fires from autofill).

---

### Task 1: Config keys + docs scaffolding

**Files:**
- Modify: `internal/config/config.go` (+ `internal/config/config_test.go`), `stasher.yaml.example`, `docs/INSTALL.md`
- Test: `internal/config/config_test.go` (extend)

**Interfaces:**
- Produces: `config.Config.AIAPIKey`, `AIBaseURL`, `AIModel` (strings; env `STASHER_AI_API_KEY`/`STASHER_AI_BASE_URL`/`STASHER_AI_MODEL` in the env map with empty-env-falls-back-to-yaml behavior, exactly like `brave_api_key`).

- [ ] **Step 1: Failing tests** — defaults (`AIAPIKey == ""`, `AIBaseURL == "https://api.openai.com/v1"`, `AIModel == ""`); yaml sets all three; env overrides each; env-empty falls back to yaml.
- [ ] **Step 2: Run** `go test ./internal/config/ -run TestAI -v` → FAIL.
- [ ] **Step 3: Implement** the three fields (yaml `ai_api_key`, `ai_base_url`, `ai_model`), env map entries, default-filling in Load (default base URL only when empty).
- [ ] **Step 4: Run** → PASS; `go vet ./...` clean.
- [ ] **Step 5: Docs** — `stasher.yaml.example` block (commented-out example: all three keys, note "empty ai_api_key disables; ai_model is required when the key is set; ai_base_url lets you point at OpenRouter/Groq/local ollama — enabling sends item photos to this service"); `docs/INSTALL.md` short section (same content + example ollama base_url `http://localhost:11434/v1`).
- [ ] **Step 6: Commit** `feat: ai autofill config keys`.

### Task 2: `internal/ai` client

**Files:**
- Create: `internal/ai/ai.go`, `internal/ai/ai_test.go`

**Interfaces:**
- Consumes: none (pure client; `net/http`, `encoding/json`).
- Produces:

```go
type Fields struct {
    Name        string
    Description string
    Qty         *float64
    Unit        string
    Tags        []string
}
type Client struct {
    BaseURL string // default applied by constructor
    APIKey  string
    Model   string
    HTTP    *http.Client
}
func New(apiKey, baseURL, model string) *Client // 45s timeout client; baseURL default https://api.openai.com/v1
func (c *Client) Enabled() bool                  // APIKey != "" && Model != ""
func (c *Client) Autofill(ctx context.Context, images [][]byte, mimes []string) (Fields, error)
```

- [ ] **Step 1: Failing tests** (httptest fake provider at `srv.URL`; construct via `New(key, srv.URL, "test-model")`):
  1. Happy path: provider echoes `choices[0].message.content = {"name":"LM358 op-amp","qty":14,"unit":"pcs","tags":["op-amp","dip-8"]}` → Fields match; ALSO pin the REQUEST: `Authorization: Bearer k`, `model == "test-model"`, body has N `image_url` entries with `data:image/png;base64,` prefixes (1 per image), `max_tokens` ≤1000 present, and **no `response_format` key**.
  2. Fenced JSON: content wrapped in ```json fences → parses.
  3. Extra/missing keys: content `{"description":"x","qty":null,"tags":["a"],"junk":1}` → Name "", Description "x", Qty nil, Tags ["a"].
  4. Non-2xx (500 with juicy body) → error mentions status, does NOT contain the body text.
  5. Malformed content (`"the item is..."`) → error (ai wraps as unparseable).
  6. Timeout: handler sleeps 46s? — no: use a short-deadline variant (Client with 100ms HTTP timeout via injected field or ctx cancel) → error.
- [ ] **Step 2: Run** → FAIL (package missing).
- [ ] **Step 3: Implement** — fixed system+user prompt (demand strict JSON, all fields optional, qty is a number, ≤10 short lowercase-ish tags, no markdown); message with N image parts then a text part; 64KB `io.LimitReader` on response; content parse: trim → strip optional ```/```json fences → `json.Unmarshal` into a `map[string]any`-backed private struct with `json.RawMessage`-safe optional fields; all errors via `fmt.Errorf("ai: …")` wrapping.
- [ ] **Step 4: Run** → PASS; `go vet ./...` clean.
- [ ] **Step 5: Commit** `feat: openai-compatible vision client`.

### Task 3: `POST /api/v1/autofill` endpoint

**Files:**
- Create: `internal/api/autofill_handlers.go`
- Modify: `internal/api/server.go` (route + `aiRate *auth.Limiter` field + `aiClient *ai.Client` wiring in `New`), `cmd/stasher/main.go` (construct client from config), `internal/api/testutil_test.go` (wire `ai.New("", "", "")` by default; tests override)
- Modify: `docs/api-notes.md`
- Test: `internal/api/autofill_test.go`

**Interfaces:**
- Consumes: `internal/ai` (Task 2), config (Task 1), `auth.NewLimiter(12, 1)` (1/5s), `maxPhotoBytes` (files_handlers.go:56).
- Produces: `POST /api/v1/autofill` multipart `files` (1–4 images) → `200 {name?, description?, qty?, unit?, tags?}`; error codes `ai_not_configured` (400), `ai_bad_response` (400), `ai_upstream_error` (502), `rate_limited` (429), `validation_error` (400 zero parts / >4 parts), `invalid_upload` (400 oversize/bad multipart), `missing_file` (400), image-415 via existing `allowedPhotoTypes` semantics.

- [ ] **Step 1: Failing tests** (fake provider via httptest; test sets `ts2.aiClient = ai.New(key, fakeURL, "m")` on the Server — check how search tests swap `s.searcher` and mirror that seam):
  1. Happy: 2 PNG parts → 200, fields forwarded (fake returns known JSON).
  2. Clamps: fake returns name 250 chars → 200 with name len 200; tags `["ok","",strings.Repeat("x",101),"Ok","Ok"]` → `["ok","xxx…truncated? NO — invalid DROPPED"]` → `["ok"]` (over-length dropped not truncated, dupe folded); qty 2e12 → absent; qty 5.5 → present; description >10k → truncated.
  3. 0 parts → 400 validation_error; 5 parts → 400.
  4. Non-image part (text/plain sniff) → 415; one valid + one invalid → 415 whole request.
  5. Oversize part → 400 invalid_upload (MaxBytesReader trips — same code as photo uploads).
  6. Unconfigured (`aiClient.Enabled() == false`) → 400 ai_not_configured naming missing keys.
  7. Rate limit: two immediate calls → second 429 rate_limited (first must succeed against fake).
  8. Provider 500 → 502 ai_upstream_error, generic message (assert body does NOT contain fake's error text).
  9. Provider malformed → 400 ai_bad_response.
  10. Unauthenticated → 401.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** handler: MaxBytesReader(4×maxPhotoBytes) → ParseMultipartForm → collect `r.MultipartForm.File["files"]` (each part: open, sniff 512 via ReadFull pattern, image check, size via the already-bounded reader `io.ReadAll` — buffer like the photo path since images pass whole to the AI) → guards → rate limit → `aiClient.Autofill` → clamp → 200. Wire route `r.Post("/autofill", s.handleAutofill)` INSIDE the authed group; `aiRate: auth.NewLimiter(12, 1)` in New; main constructs `ai.New(cfg.AIAPIKey, cfg.AIBaseURL, cfg.AIModel)` and passes to New (append param — update all callers incl. testutil; nil-safe: store the concrete `*ai.Client`, handler checks `s.aiClient == nil || !s.aiClient.Enabled()`).
- [ ] **Step 4: Run** full `go vet ./... && go test ./...` → PASS.
- [ ] **Step 5: api-notes** — endpoint contract (multipart files 1–4, clamps table, rate limit, error codes, privacy note).
- [ ] **Step 6: Commit** `feat: POST /api/v1/autofill endpoint`.

### Task 4: Frontend — client fn + add-form autofill

**Files:**
- Modify: `web/src/api/client.ts` (add `autofill(files: Blob[])` — builds FormData with `files` entries), `web/src/views/ItemFormView.vue` + its test
- Rebuild: dist via `make web` (commit)

**Interfaces:**
- Consumes: Task 3 endpoint; existing `queuedPhotos`/pick flow (ItemFormView.vue ~line 194).
- Produces: `applyAIFields(fields: Partial<{name; description; qty; unit; tags}>)` — internal form-apply helper that Task 5 reuses (sets refs, tracks ✨-touched keys in a `Set<string>` cleared on manual input of that field).

- [ ] **Step 1: Failing tests** (fetch mocked at client boundary; follow ItemFormView.test.ts conventions):
  1. No queued photos → button absent.
  2. Two queued photos → button visible with "2 photos" hint; click → POST /api/v1/autofill with BOTH blobs as `files` parts (inspect FormData via the fetch stub's body).
  3. Response fills name/description/qty/unit/tags; ✨ hint markers present (`data-ai-hint` on touched labels); a queued 6-photo case posts only the first 4 and the hint says "first 4".
  4. `ai_not_configured` → toast contains the config hint; fields untouched.
  5. While analyzing: button disabled + "Analyzing…"; no auto-POST of /items (autofill alone never saves).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** — `autofillItems()` using `queuedPhotos.value.slice(0, 4)`; apply helper writes only returned fields (tags APPEND to existing tag input, dedup, comma-joined per the form's tag entry convention — check how ItemFormView represents tags and match it); ✨ `aiTouched` Set; toast "Filled: name, qty, …".
- [ ] **Step 4: Run** `npx vitest run && npx vue-tsc -b` → PASS; `make web`.
- [ ] **Step 5: Commit** `feat(web): add-form photo autofill`.

### Task 5: Frontend — edit-form multi-select picker + polish

**Files:**
- Modify: `web/src/views/ItemFormView.vue` + its test (edit-mode branch)
- Rebuild: dist via `make web` (commit)

**Interfaces:**
- Consumes: Task 4's apply helper + `autofill` client fn; item detail's `photos` (loaded in edit mode today — verify how ItemFormView loads the item; if it loads only item fields, fetch `/api/v1/items/{id}` for photos).
- Produces: edit-mode autofill: button opens an inline multi-select thumbnail grid (≤4), Analyze fetches each selected ORIGINAL from `/files/{storage_key}` (same-origin fetch → Blob) and posts them.

- [ ] **Step 1: Failing tests**:
  1. Edit mode with 3 photos: button opens picker with 3 thumbs (`/files/{thumb_key ?? storage_key}` srcs); click toggles selection; badge shows count; Analyze disabled at 0.
  2. Two selected → fetches both originals (`GET /files/k1`, `GET /files/k2`) → POSTs autofill with both blobs.
  3. Add mode does NOT render the picker (button acts directly).
  4. Photo fetch failure → toast, no POST.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** — picker state (`aiPickOpen`, `aiSelected: Set<number>`), fetch-original loop with Promise.all + per-file error toast prefix (filename/id), reuse apply helper; max-4 guard on toggling.
- [ ] **Step 4: Run** full `npx vitest run && npx vue-tsc -b` → PASS; `make web`; then repo root `make check`.
- [ ] **Step 5: Commit** `feat(web): edit-form multi-photo autofill picker`.

## Post-plan verification

`make check` green; `go build`; `./scripts/smoke-web.sh` + `node scripts/smoke-authed.mjs`; manual-style check optional (feature is config-gated; without a key the endpoint 400s and buttons degrade to error toasts). Strike `docs/todo.md` line 4 with `done 2026-10-23`.
