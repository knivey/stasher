# External Content: URL Fetching & Page Archiving

**Date:** 2026-10-22
**Status:** Approved design (pre-implementation)

## 1. Purpose

Stasher items reference the outside world: product pages, datasheets,
manuals. Today the user must download those by hand and upload them. This
feature lets the **server** fetch a URL the user provides and store the
result as an ordinary item document:

- **Direct fetch** — plain HTTP download of a file (datasheet PDFs, manual
  PDFs, images). No browser needed.
- **Archive** — render a web page to a **full-page PDF** with headless
  chromium (`--print-to-pdf`), capturing what the page looked like.

Every fetch is strictly user-initiated (an explicit button); nothing
automatic ever dials out. The engine is built as a reusable subsystem that
later rounds extend: part-number datasheet search, project reference URLs +
archiving, camofox as an anti-bot engine, AI image analysis.

Success criteria:

- Paste a datasheet PDF URL on the item page → a document appears,
  served inline like any PDF.
- Paste a product page URL (or use the item's saved product URL) → an
  archived full-page PDF document appears.
- Works on the alpine deployment with `apk add chromium`; without chromium,
  direct fetch still works and archive errors clearly.
- A hostile URL (localhost, RFC1918, link-local, non-http scheme, redirect
  into any of those) is rejected — never dialed.

## 2. Fetch engine — `internal/fetcher`

```go
type Result struct {
    Body        []byte   // capped
    ContentType string   // sniffed/verified
    Filename    string   // sanitized suggestion
}
type Engine interface {
    Fetch(ctx context.Context, url string) (Result, error)
}
```

Two implementations + a resolver/IP-policy seam for tests:

### Direct engine

- Manual redirect following (max 5 hops); **every hop** re-applies the URL
  scheme check and the SSRF IP policy before dialing.
- `GET` with `User-Agent: stasher/1`, 30s overall timeout, response body
  streamed with a 25MB cap (abort beyond).
- Content type from headers, verified by sniffing the first 512 bytes
  (`http.DetectContentType`); the **allowlist**: `application/pdf`,
  `image/*`, `text/plain`. Anything else → error (no HTML pages via
  direct).
- Filename from the final URL path (basename, sanitized via the existing
  filename rules) or `Content-Disposition`, else `fetch.<ext>`.

### Archive engine (chromium)

- Locates the binary: `STASHER_CHROME_PATH` env → config `chrome_path` →
  first existing of `chromium`, `chromium-browser`, `google-chrome` on
  PATH. Not found → archive requests fail fast with a clear message;
  direct is unaffected.
- Executes `<chrome> --headless=new --no-sandbox --disable-gpu
  --virtual-time-budget=10000 --print-to-pdf=- <url>` capturing stdout to
  a temp file (or `--print-to-pdf=<tmp>`), 60s timeout via context kill,
  output capped at 25MB. Empty/invalid PDF output → error.
- The URL gets the same SSRF check before launch (DNS resolve + IP pin is
  not possible across the exec boundary; v1 accepts check-then-exec —
  documented limitation, mitigated by per-request user initiation).
- Filename: `<host> <YYYY-MM-DD>.pdf` from the URL host + UTC date
  (house rule: UTC everywhere).

### SSRF policy (shared, hard-coded in production)

- Schemes: `http`/`https` only.
- Resolve the hostname (A + AAAA); **every** address must be public:
  reject loopback (`127/8`, `::1`), RFC1918 (`10/8`, `172.16/12`,
  `192.168/16`), link-local (`169.254/16`, `fe80/10`), ULA (`fc00/7`),
  IPv4-mapped IPv6, and unspecified (`0.0.0.0`, `::`).
- Direct engine **pins** the connection to the approved IP via a custom
  `DialContext` (closes the DNS-rebinding window); redirects re-resolve
  and re-check each hop.
- The policy is injectable (`Policy` interface): production hard-codes the
  public-only set; **tests** inject an allow-loopback policy so
  `httptest` targets work. No config knob exposes the policy.

## 3. Jobs — in-memory

- `POST /api/v1/items/{id}/fetch` `{ "url": "...", "mode": "direct" | "archive" }`
  → `202 {"job_id": "..."}`. Owner-only (foreign/absent item → 404).
  Validation: URL parses, http(s), ≤2000 chars; mode one of the two → 400s.
- `GET /api/v1/jobs/{id}` → `200 {"status":"running"|"done"|"failed",
  "error"?: string, "document_id"?: number}` — owner-scoped (other user's
  job id → 404).
- Jobs live in a mutex-guarded map on the Server; a goroutine runs the
  fetch, stores the document on success (files pipeline + item_documents
  row, size/mime recorded like an upload), and marks the job. No
  persistence: a restart abandons running jobs (client polling gets 404 →
  surfaces as "fetch interrupted"). Map is pruned of finished jobs older
  than 1 hour.
- Concurrency: at most **one running fetch job at a time** server-wide (a
  buffered channel of 1); additional requests queue in "running" state
  behind it rather than spawning concurrent browsers. (Simple, predictable
  for a home instance.)

## 4. Item surfaces

### Schema — migration `00007_items_product_url.sql`

`ALTER TABLE items ADD COLUMN product_url TEXT;` (nullable).

### API

- Item create/PATCH gain `product_url`: absent = keep (PATCH), `""` =
  clear, non-empty must be a valid http(s) URL ≤2000 chars (400
  `validation_error`). Returned in item JSON (`null` when unset).
- Fetch endpoint as in §3; resulting documents are ordinary item documents
  (same listing, owner-or-shared gating, inline PDF serving).

### Frontend

- **Edit form**: "Product page URL" field (optional). Next to it, an
  **"Archive"** button (visible when the field is non-empty) → POST
  archive job for the entered URL.
- **Item page**: "Fetch document from URL…" action in the Documents
  section header → small dialog: URL input + mode select (auto-guess:
  direct if the URL path ends `.pdf`, else archive; user can override) →
  POST → toast "Fetching…" → poll `/jobs/{id}` every 2s (up to 90s) →
  toast success ("Added document") or the job's error → refetch item.
- Product URL renders as an external link on the item page (rel noopener,
  new tab) with its own small **Archive** button that runs the same flow.
- Dark-mode classes throughout; no new deps.

## 5. Config

- Optional `chrome_path` (config file) + `STASHER_CHROME_PATH` env, both
  may be empty → auto-detect. Documented in `stasher.yaml.example`.
- `docs/INSTALL.md`: "Optional: headless chromium" — `apk add chromium`
  (alpine), `apt install chromium` (debian), `pacman -S chromium` (arch);
  notes that direct fetch needs nothing extra.

## 6. Error & fallback semantics

- Chromium missing → archive jobs fail fast with a actionable message
  ("install chromium or set chrome_path").
- Fetch failures (DNS, timeout, cap exceeded, content-type rejected,
  browser crash/timeout) → job `failed` with a one-line cause; nothing
  stored.
- SSRF rejections fail the job before any dial; the error does not echo
  the resolved IP.
- A failed fetch never touches the item row or existing documents.
- Restart mid-job → job vanishes; client polling 404 → "fetch interrupted"
  toast.

## 7. Testing

- **fetcher unit**: SSRF table (each private range, both families,
  mapped-v6, unspecified), scheme rejection, redirect-chain re-check
  (redirect to private → rejected), IP pinning (resolver returns public
  then private — second resolution ignored), caps (26MB body abort,
  browser output cap), content allowlist (HTML via direct rejected),
  filename sanitization.
- **engine integration**: direct against `httptest` (loopback policy) for
  PDF/image/text + redirect + content-disposition; archive via a **stub
  chrome** (test fixture shell script that emits a minimal `%PDF-` file,
  wired through `chrome_path`) + skip-if-real-chrome-present variant.
- **handlers**: 202/404/400 shapes, job lifecycle (running → done with
  document_id; failed with error), owner scoping, queueing (second job
  waits), item JSON carries product_url, PATCH ""-clears, URL validation
  table.
- **frontend**: dialog POST body + mode guess, polling success/failure/
  404-interrupted paths, Archive buttons wired to the right mode, form
  field save/clear.
- Both smokes stay green; `make check` gate as usual.

## 8. Out of scope (v1)

Part-number datasheet search; camofox engine; project reference URLs;
AI image analysis; screenshot (PNG) archives; scheduled/bulk re-archiving;
job persistence; per-user rate limits on fetching (single-flight queue is
the v1 guard).

## 9. Docs

`docs/api-notes.md`: fetch + jobs endpoints, modes, caps, job semantics
(in-memory, 404-after-restart), product_url PATCH semantics. INSTALL +
stasher.yaml.example for chromium.

## 10. Implementation

Subagent batches with mandatory reviews (AGENTS.md): (1) fetcher package
+ SSRF + engines + tests; (2) migration + endpoints (fetch/jobs/
product_url) + handler tests + api-notes; (3) frontend (form, dialog,
buttons, polling) + dist; (4) config/INSTALL docs folded into (2)/(3).
