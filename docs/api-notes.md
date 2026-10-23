# API contract notes (frontend implementer)

Quirks of the `/api/v1` JSON API that are easy to get wrong. Errors always
use `{"error": {"code": "...", "message": "..."}}`.

## PATCH semantics for nullable fields

JSON `null` and an absent field are **not** the same thing on PATCH. The
rule per resource:

### Items (`PATCH /items/{id}`)

| Field | Provided value | `null` / absent |
|---|---|---|
| `location_id` | **`0` clears the location**; any other id must exist (else 404) | keeps current |
| `name` | replaces (1-200 chars) | absent keeps (empty string also keeps) |
| `description`, `unit`, `qty`, `visibility` | replaces (validated) | keeps current |
| `tags` | **replaces the whole list** (validated first: max 50, each name 1-100 chars; invalid list → 400 and **nothing** is written) | keeps current |

Note the asymmetry: for an item's location you clear with `0`, not `null`.

### Locations (`PATCH /locations/{id}`)

- `parent_id`: **cannot be cleared.** A provided parent is validated
  (must be yours, else 404) and cycle-checked (400). Absent or `null`
  keeps the current parent.
- `notes`: absent or `null` **keeps** the current notes; a provided
  string — including `""` — replaces them. Send `""` to clear.
- `name`: absent keeps; provided replaces (1-200 chars).

## Sort/order on `GET /items`

- `sort` ∈ `updated_at` (default) | `created_at` | `name` | `qty`
- `order` ∈ `desc` (default) | `asc`
- Unknown values (typos, injection attempts) silently fall back to the
  defaults — never an error. The server maps `sort`/`order` through a
  fixed identifier whitelist, so only those four columns exist.
- Ties (e.g. same-second timestamps, equal qty) have unspecified order;
  don't rely on a secondary ordering.
- `GET /shared` has no `sort` param; it is always `updated_at DESC`.

## Search (`q` on `GET /items` and `GET /shared`)

- Since 2026-10-22 the search index uses SQLite FTS5's **trigram
  tokenizer**: `q` terms are **substring matches** inside item names and
  descriptions — `q=raspb` finds "raspberry pi". Matching is
  case-insensitive (`q=RASP` works too). Multiple terms are AND-ed.
- **A term must be at least 3 characters to match anything.** Shorter
  terms cannot form a trigram: a query containing one returns an **empty
  result** (200, `data: []`) — never an error, and the short term is not
  silently ignored (`q=pi 5` matches nothing, it is not `q=5`).
- FTS5 special characters in `q` (`"`, `AND`, `*`, parentheses, …) are
  still safe: each term is quoted before it reaches MATCH, so they match
  nothing rather than erroring or injecting operators.

## Pagination shape + clamps

- `page` is 1-based (default 1, values < 1 or non-numeric → 1).
- `page_size` default 50, clamped to max 200 (non-numeric/negative → 50).
- Response envelope: `{"data": [...], "total": n}` where `total` is the
  pre-pagination match count.
- **Quirk:** `total` is read from the first returned row, so an *empty*
  page (past-the-end `page`, or over-tight filters) reports `total: 0`
  even when the collection is non-empty. Don't treat `total == 0` on a
  page > 1 as "collection empty" — check page 1.

## File serving (`/files/{storageKey}`)

- Session-gated: owner or (for shared items) any logged-in user; otherwise
  404 (never 403, to avoid existence leaks).
- Content types rendered **inline**: `image/*`, `application/pdf`,
  `text/plain`. Everything else (HTML, SVG, JSON, unknown…) is forced to
  `application/octet-stream` with `Content-Disposition: attachment` — it
  downloads instead of executing on the API origin.
- Document mime comes from upload-time sniffing (PDF magic bytes are
  stored as `application/pdf`); photos are sniffed at serve time.
- `Cache-Control: private, max-age=86400`.

## 409 vs 404 on colliding tag renames (`PATCH /tags/{id}`)

The name-collision check runs **before** the tag-existence/ownership
check, so:

- Renaming to a name another of **your** tags holds → `409 tag_exists`
  (even before anything else is validated).
- `PATCH` on a **nonexistent or foreign** tag id whose new name collides
  with one of your own tags → `409`, not `404`. Only a non-colliding
  rename of a bad id yields `404 not_found`.
- Name collisions with *other users'* tags are invisible (tags are
  per-user) and never block a rename.

## Photos: make-main (2026-10-22)

```
POST /items/{id}/photos/{photoId}/main — owner only. Renumbers the item's
photos (sort_order 0..n-1, target first, others keep current (sort_order,
id) order) in one transaction. Always 204 (already-first is a no-op);
404 for foreign/absent item or photo. This changes which photo lists
thumbnail (thumb_key follows (sort_order, id)).
```

## Bulk location generation (2026-10-22)

`POST /api/v1/locations/bulk` — body:

```json
{ "name": "Cabinet", "parent_id": 0, "dims": [12, 6],
  "structure": "flat", "naming": "compact", "prefix": "Bin" }
```

- Creates the root location plus generated sublocations in **one
  transaction** — all or nothing. `201` returns the root location JSON
  (same shape as `POST /locations`) plus `"children_created": <int>`,
  which counts every generated location besides the new root — in
  nested mode that includes intermediate levels.
- Validation (all `400 validation_error`, specific messages) runs before
  any write: `name` trimmed 1-200 chars; `dims` 1–3 entries, each 1–100,
  product ≤ 1000; `structure` exactly `flat`|`nested`; `naming` exactly
  `compact`|`verbose`; `prefix` trimmed ≤ 50 chars.
- `parent_id` 0 or absent = root; a provided id must be yours (else 404).
  A root-name collision under the parent is `409 location_exists`.
- **flat**: every axis combination is a direct child of the new root,
  dim0 iterating outermost so names sort naturally (A1, A2 … B1 …).
  **nested**: one level per dimension; a node at depth d is parented to
  its axes-0..d-1 node.
- **Naming** — compact concatenates axis labels: dim0 uppercase
  spreadsheet letters (1→A, 26→Z, 27→AA), dim1 numbers, dim2 lowercase
  letters (a, b, … aa). Verbose space-joins `Row {i} Col {j} Bin {k}`.
  A 1D config collapses to plain numbers (compact `1..N`) / `Slot 1..N`
  (verbose). Examples: 2D `A1`, `B3`; 3D `A1c`; verbose `Row 3`,
  `Row 3 Col 2`, `Row 3 Col 2 Bin 1`.
- `prefix` prepends to **leaf names only** (with a space): `Bin A1`.
  Nested intermediate levels stay unprefixed.
- **`labels`** (optional, verbose naming only): per-axis replacement
  words for the verbose names, e.g. `["Drawer", "Tray"]` with
  `dims [2,2]` → `Drawer 1 Tray 2`; nested intermediates use their own
  axis's word (`Drawer 1` › `Drawer 1 Tray 2`). Rules (all `400
  validation_error`): present `labels` must have exactly `len(dims)`
  entries (`labels must match dims length`); each label is trimmed and
  the trimmed length must be ≤ 50 (`labels must be at most 50 chars`);
  an entry that is empty **after trimming falls back to that axis's
  default word** (Slot/Row/Col/Bin by position) — it is not an error.
  With `naming: "compact"` a label that is non-empty **after trimming**
  is rejected (`labels only apply to verbose naming`) — whitespace-only
  labels are the empty/default case, not an error. Compact output never
  contains the words. Absent `labels` behaves exactly as before.

## Location pictures (2026-10-23)

A location carries **at most one optional picture** (columns, not a table):
`photo_key` + derived `photo_thumb_key`, both nullable. Same upload rules
as item photos (sniffed `image/*` only, thumb generation that degrades to
null). **Oversize is pinned**: a body over the 20 MB cap (`MaxBytesReader`)
is rejected as `400 invalid_upload` — exactly what `POST /items/{id}/photos`
returns for the same oversized payload.

```
POST   /api/v1/locations/{id}/photo — multipart field `file`; owner-only,
       foreign/absent location → 404. Responds 200 {photo_key, thumb_key}
       (thumb_key null when generation failed). REPLACES any existing
       picture: new files are saved and committed first, then the old
       photo+thumb files are deleted (best-effort, warn-logged).
DELETE /api/v1/locations/{id}/photo — owner-only (foreign → 404). Clears
       both columns and deletes the files; idempotent (no picture → 204).
```

- `GET /locations` (and `GET /locations/{id}`, copy/bulk responses) expose
  `photo_key` and `photo_thumb_key` as JSON strings or explicit `null`.
- `/files/{key}` serves location pictures to the **owner only** (matched
  against `photo_key OR photo_thumb_key`): sharing an item never exposes
  the location's picture. Content type is sniffed at serve time, like item
  photos.
- `DELETE /locations/{id}` deletes the deleted row's picture files
  best-effort. Layout copy (`copy-layout`) copies **names/shape only —
  pictures are not copied**.

## Layout copying (2026-10-22)

`POST /api/v1/locations/{id}/copy-layout` — body
`{ "name": "Cabinet copy", "parent_id": 0 }`.

- Copies the source's ENTIRE subtree (names + shape only — **items and
  notes are never copied**) under a NEW location in one transaction.
  `201` returns the new location JSON + `"children_created": <int>`
  (descendants copied, excluding the new root).
- Source must be yours (else 404). `name` trimmed 1-200; `parent_id` 0
  or absent = root, else must be yours (404). Copying a subtree INTO
  itself is `400 validation_error`
  (`cannot copy a location into its own subtree`) — the new parent may
  not be the source or any descendant of it. Subtrees over 1000 nodes
  (source + descendants) are rejected `400`. Root-name collision under
  the parent is `409 location_exists`.
- **Wart:** on these two endpoints (bulk, copy-layout) an explicit
  `parent_id: 0` means root, same as omitting it. The legacy
  `POST /locations` instead treats an explicit `0` as a lookup miss →
  404 — don't copy that behavior to new callers.

## Bulk move (2026-10-22)

`POST /api/v1/items/bulk-move` — authed, owner-only. Body:
`{ "item_ids": [id, …], "location_id": n }`.

- Changes the location of up to **200 items in one statement**. `200`
  returns `{"moved": <n>}` where `moved` counts rows that actually
  matched — ids belonging to **other users or to no item are silently not
  counted**, never an error (the list view can pass a selection spanning
  a filter change and only owned items move). Duplicate ids in
  `item_ids` are harmless: the IN-list dedups and each matching row is
  counted once.
- Validation (all `400 validation_error`, specific messages, before any
  write): `item_ids` must have **1–200 entries**; each entry must be a
  **positive integer**; `location_id` is **REQUIRED** (an absent field
  must not silently mass-clear) and must be `>= 0`.
- `location_id: 0` **clears** the selected items' locations (SQL NULL);
  any other id must be one of YOUR locations — foreign or absent →
  `404 not_found`, nothing is written (same path as item create/PATCH).
- `updated_at` is bumped to ONE server-set RFC3339 UTC timestamp shared
  by every moved item. `disposed` items are movable (no special case).
  Search needs no reindex: `items_fts` indexes name+description only.
- Error-precedence: all 400s (shape/count of `item_ids`, `location_id`
  presence/sign) run before the 404 location check, which runs before
  the write.

## External fetch & jobs (2026-10-22)

The server can fetch a URL and store the result as an ordinary item
document. Two modes:

- **`direct`** — plain HTTP download (datasheet PDFs, images, text).
  Caps: **25MB body, 30s per hop** (each request, redirect chains included,
  max 5 hops — the whole chain is additionally bounded by the 90s job
  deadline below).
- **`archive`** — renders the page to a **full-page PDF** with headless
  chromium. Caps: **25MB output, 60s** render. Filename is
  `<host> <YYYY-MM-DD>.pdf` (UTC date). Needs chromium installed/configured
  (`chrome_path` / `STASHER_CHROME_PATH`); without it the job fails with an
  actionable message and direct mode is unaffected.

```
POST /api/v1/items/{id}/fetch  { "url": "...", "mode": "direct"|"archive" }
→ 202 { "job_id": "..." }
GET  /api/v1/jobs/{id}
→ 200 { "status": "running"|"done"|"failed",
        "error"?: string, "document_id"?: number }
```

- **Validation** (owner-only route; foreign/absent item → `404` like every
  owned-item route): `url` must parse as http(s) and be ≤2000 chars
  (`400 validation_error` — same rule as `product_url`); `mode` must be
  exactly `direct` or `archive`. Private/loopback targets are rejected by
  the SSRF policy at fetch time (job → `failed`), not at submit time.
- **Job JSON omits empty fields**: a running job is just
  `{"status":"running"}`; failed adds `error`; done adds `document_id`.
- **Overall job deadline: 90s**, covering the queue wait AND the fetch —
  poll for at most that long before giving up.
- **Single-flight**: at most ONE fetch runs at a time server-wide. Extra
  requests queue in `running` state behind it (they can sit there for a
  while — a 90s-polling client covers a queue slot too).
- **In-memory only**: jobs do not survive a restart. A job id that 404s
  after a restart means "fetch interrupted" — surface it that way, the
  document was NOT created.
- **Owner-scoped**: another user's job id → `404 not_found` (no existence
  leak), same for garbage ids.
- **Content allowlist (direct)**: `application/pdf`, `image/*`,
  `text/plain` only (verified by sniffing, not by the response header).
  HTML pages are rejected — use `archive` for pages. The stored mime is
  the sniffed type; documents serve like uploads (inline for pdf/image/
  text).
- **Security note (archive)**: the SSRF check runs before chromium
  launches, but chromium re-resolves the host itself and follows its own
  redirects — those are **NOT policy-rechecked** (check-then-exec
  limitation, accepted for v1; every fetch is an explicit user action).

### `product_url` on items

- `POST /items` and `PATCH /items/{id}` accept `product_url`: absent =
  keep (PATCH), `""` = clear (stored NULL), non-empty must be an http(s)
  URL ≤2000 chars (`400 validation_error`). Returned on item detail
  (`null` when unset); **list rows never carry it** (detail-only field).
  Same table as the other nullable PATCH fields above.

## Document search (2026-10-23)

Brave-powered "find datasheets for this item". Requires `brave_api_key`
(yaml) / `STASHER_BRAVE_API_KEY`; **empty key disables the feature**.

```
POST /api/v1/items/{id}/search-documents  { "query": "lm358 datasheet" }
→ 200 { "results": [ { "title", "url", "host", "size"? }, … ],
        "rejected": <int> }
```

- **Owner-only** like every owned-item route: foreign/absent item → `404`,
  **before** the search-disabled check (config state is not probeable
  through someone else's item).
- **No key configured** → `400 search_disabled` (`set brave_api_key in
  config to enable document search`). Query must trim to **1-200 chars**
  (else `400 validation_error`). Per-user rate limit **1 search / 3s** →
  `429 rate_limited` on the second immediate call.
- **The response is SYNCHRONOUS** (15s overall budget) — no job, no
  polling (unlike `/fetch`). `results` contains ONLY candidates that
  verified as downloadable PDFs server-side (Brave web results, top 6
  ranked `.pdf`-suffix first, each fetched by the server through the same
  SSRF policy/pinning path as direct fetch — a Range read of the first
  512 bytes must show the `%PDF-` magic). `rejected` counts the
  candidates dropped by verification; zero verified → `results: []` (the
  UI shows "no downloadable PDFs found").
- **`size` is OPTIONAL**: present with the PDF's total bytes when the
  candidate server reported a Content-Range total, **absent** when unknown
  (plain 200 or unparseable Content-Range). Don't assume the key exists.
  `size: -1` is never sent.
- Brave-side failures (key rejected, network) → `502 search_failed`.
- **Picking a result uses the EXISTING fetch flow**: `POST
  /items/{id}/fetch` with `{url: <result.url>, mode: "direct"}` → 202
  `{job_id}` → poll `/jobs/{id}`. The search endpoint itself never stores
  anything.
- Brave API key handling: 401/422 from Brave surface as "Brave API
  rejected the key" (in the 502 message); 10 results per query are
  requested (cap 10 kept server-side), non-http(s) URLs are dropped.

## Frontend notes

Facts discovered by the frontend round, kept here so they survive:

- **Locations, tags and projects lists ignore pagination.** `GET
  /locations`, `GET /tags` and `GET /projects` return the **full list**
  every time — `page`/`page_size` params are accepted but unused. Only
  `GET /items` and `GET /shared` actually paginate (default 50, max 200).
  Frontend callers of the unpaginated endpoints may pass `page_size` (a
  harmless no-op) but must not rely on it; the `page_size=200 = backend
  max` comments that used to sit next to those calls were wrong and are
  gone.
- **`thumb_key` is a derived thumbnail** (2026-10-22, supersedes the
  2026-10-21 storage_key note): on `GET /items` and `GET /shared` rows,
  `thumb_key` is the item's first photo's (ordered by `sort_order, id`)
  **derived ≤480px file** (`jpg`/`png`/`gif`), selected as
  `COALESCE(thumb_key, storage_key)` — when generation failed (legacy
  photos until backfill, corrupt images) the original key is returned
  instead; `null` when the item has no photos. `GET /items/{id}`
  `photos[]` and the `POST /items/{id}/photos` response carry the photo's
  own `thumb_key` (nullable — `null` when generation failed;
  `storage_key` sits alongside for the fallback). `/files/{thumb_key}`
  passes the same owner-or-shared gate as all files (matched against
  `storage_key OR thumb_key`). `photo_count` remains on rows; the SPA
  renders the image when `thumb_key` is present and the 📷 count badge
  otherwise.

## Trash (2026-10-21)

- `DELETE /api/v1/items/{id}` no longer hard-deletes: it snapshots the item
  (fields, tags, photos, documents, project links, usage ledger) into a
  trash table in one transaction and deletes the row. Files stay on disk.
  **While an item is trashed, `/files/{key}` 404s** (photo/doc rows cascade
  with the item) — `thumb_key` in trash rows is NOT renderable until
  restore. The frontend shows a 📷 count badge instead.
- `GET /api/v1/trash` — paginated, newest-trashed first; sweeps expired
  entries (and their files) on every call. Rows: `{id, name, qty,
  photo_count, thumb_key, trashed_at, purges_at}` — `purges_at` null means
  keep until manually purged (`trash_retention_days` config, default 30;
  `0` disables auto-purge). Trash rows pick the snapshot's derived thumb
  when present, falling back to the original storage_key for legacy
  snapshots.
- `POST /api/v1/trash/{id}/restore` — recreates the item under a NEW id
  (tags, photos/docs rows with the same storage keys, ledger in original
  order, project links and location only if still owned — vanished deps
  are skipped/NULLed). Files serve again immediately.
- `DELETE /api/v1/trash/{id}` purges one permanently (files + row);
  `DELETE /api/v1/trash` empties all owned trash. Both irreversible.
- Trash listings interact with the pagination quirk: removing the last row
  of the last page yields `{data: [], total: 0}` — clients must clamp
  `page` down, not treat it as empty (the SPA does).

## Usage ledger (2026-10-21)

- `POST /api/v1/items/{id}/usage` `{kind, delta?, note?, project_id?}` —
  applies a qty change and writes a ledger entry in one transaction.
  Kinds: `used`, `sold`, `lost`, `found`, `restock`, `disposed`,
  `adjustment`. `delta` is required and non-zero for all kinds except
  `disposed`, which forces delta = −current qty, sets qty 0, and marks the
  item `disposed`. Resulting qty can never go below 0 (400). `project_id`
  is optional and must be owned. 201 returns the entry (with
  `project_name` when a project is linked). **Dispose is idempotent**:
  disposing an item that is already `disposed` at qty 0 is a no-op — 204,
  no ledger entry, no item rewrite.
- `GET /api/v1/items/{id}/usage` — paginated ledger, newest first; rows
  `{id, delta, resulting_qty, kind, project_id, project_name, note,
  created_at}`. `project_name` is the linked project's **current** name
  (it follows renames) or `null` when the entry has no project.
- **Auto-logging**: item create and copy write an initial `adjustment`
  entry (note `initial`); any PATCH that *changes* qty writes an
  `adjustment` entry (delta = new − old) in the same transaction. No-op
  qty PATCHes write nothing.
- **Flag rule**: `disposed` appears on item detail and both list queries.
  ANY change resulting in qty > 0 clears it (usage POST and PATCH alike);
  landing on 0 via a non-disposed kind leaves the flag unchanged.
- **Delete stays for mistakes only** — it removes the item (and its
  ledger, via FK cascade) entirely; disposal is the qty-0 ledger path.

## Item notes (2026-10-22)

- `POST /api/v1/items/{id}/notes` `{body}` — owner only. The body is
  **trimmed** and must be **1-2000 bytes after the trim** (the cap is
  measured in BYTES, like the other length limits — a multibyte note hits
  it sooner than 2000 characters; whitespace-only = empty → `400
  validation_error`, message `note must be 1-2000 characters`). 201
  returns `{id, body, created_at}` with the TRIMMED body and an RFC3339
  UTC `created_at` set by the server.
- `DELETE /api/v1/items/{id}/notes/{noteId}` — owner only. 204 on success;
  a non-numeric `noteId` → `400 invalid_id`; absent/foreign note → `404
  not_found` (execrows). No edit endpoint — notes are add + delete only.
- `GET /api/v1/items/{id}` carries a `notes` array, **newest first**
  (`ORDER BY created_at DESC, id DESC` — two notes in the same RFC3339
  second tie-break by id, newest first). Rows: `{id, body, created_at}`.
- **Trash**: notes ride the trash snapshot (`{body, created_at}` only) and
  are restored with their **original `created_at`** (same as ledger
  entries); new ids are assigned. Trash LISTING rows carry no notes payload.
- **Copy**: notes are NOT copied — the copy starts with an empty notes
  list (same ruling as the usage ledger's fresh baseline).

## Project visibility (2026-10-23)

- `visibility` ∈ `private` | `shared` on projects. **Create** accepts it
  (default `private`); anything else → `400 validation_error`.
- **PATCH** (`PATCH /projects/{id}`) follows the standard absent-keeps
  rule: `visibility` absent = keep; a provided value must be `private`
  or `shared` (else `400`).
- All project JSON (create/detail/list) carries `"visibility"`.
- `shared` **has landed sharee semantics** — see the sections below:
  `GET /projects/{id}` is owner-or-sharee and returns `owner_view`
  (sharees get the masked BOM — rows cut to `{item_name, qty_used,
  shared}`), and `GET /api/v1/shared/projects` lists other users'
  shared projects.

## Shared projects: sharee read access (2026-10-23)

- **`GET /projects/{id}` is now owner-or-sharee** (was owner-only). A
  sharee is any logged-in user when the project's `visibility` is
  `shared`; everyone else (and every private project) still gets `404`
  (no existence leak). The response gains `"owner_view": bool`:
  - **Owner** (`owner_view: true`): `project` carries everything
    (including `notes`); each `items[]` row is
    `{item_id, item_name, item_qty, item_unit, qty_used, notes, added_at, shared}`
    — plus `project_id` context server-side. `shared` mirrors the
    **item's** `visibility` (not the project's).
  - **Sharee** (`owner_view: false`): `project` is the standard project
    JSON (it contains no secrets — notes are project-level and visible),
    but each `items[]` row is masked to exactly
    `{item_name, qty_used, shared}`. The keys `item_id`, `item_qty`,
    `item_unit`, `notes`, `added_at` are **absent**, not null — the BOM
    is a parts list, not an inventory view. Note the asymmetry: the
    link row's `notes` (owner's per-BOM-line note) is hidden, while the
    project's top-level `notes` field is not.
- **`GET /api/v1/shared/projects`** lists **other users'** shared
  projects: `{data: [{id, name, status, owner_name, updated_at}], total}`.
  The caller's own projects never appear, even when `shared`. No
  pagination (projects-list ruling applies); order is `updated_at DESC`.
  It carries no BOM contents — fetch the project for the masked view.
- **Mutating project routes stay owner-only**: `PATCH`/`DELETE`
  `/projects/{id}` and all `/projects/{id}/items[/...]` routes still
  answer `404` for sharees **even on shared projects**. `visibility`
  itself is only changeable by the owner. (**Exception**: the comments
  thread below is sharee-writable by design.)

## Project notes (2026-10-23)

- `POST /api/v1/projects/{id}/notes` `{body}` — **owner only** (sharees
  and strangers get `404`, even on shared projects). The body is
  **trimmed** and must be **1-2000 bytes after the trim** — identical
  byte-cap semantics to item notes (whitespace-only = empty → `400
  validation_error`, message `note must be 1-2000 characters`). 201
  returns `{id, body, created_at}` with the TRIMMED body and an RFC3339
  UTC `created_at` set by the server.
- `DELETE /api/v1/projects/{id}/notes/{noteId}` — owner only. 204 on
  success; a non-numeric `noteId` → `400 invalid_id`; an absent,
  already-deleted, or other-project note → `404 not_found` (execrows,
  `project_id` bound in the WHERE). No edit endpoint — notes are add +
  delete only.
- **`GET /api/v1/projects/{id}`**: the **owner** projection gains a
  `notes_log` array, **newest first** (`ORDER BY created_at DESC, id
  DESC` — same-second notes tie-break by id, newest first). Rows:
  `{id, body, created_at}`. The **sharee projection OMITS `notes_log`
  entirely** (absent, not empty/null) — the log is owner-private,
  unlike the project's top-level `notes` field.

## Project comments (2026-10-23)

- `POST /api/v1/projects/{id}/comments` `{body}` — the **public thread**:
  the owner can always comment (even on a **private** project — a solo
  thread), and **any logged-in user** can comment when the project is
  `shared`. Everyone else gets `404` via the shared-view gate (no
  existence leak). Body semantics are identical to item/project notes:
  **trimmed**, **1-2000 bytes after the trim** (whitespace-only = empty →
  `400 validation_error`, message `comment must be 1-2000 characters`).
  201 returns `{id, author, mine, body, created_at}` — `author` is the
  commenting user's **display name**, `mine` is `true`.
- `DELETE /api/v1/projects/{id}/comments/{cid}` — deletion matrix:
  **the comment's author** or **the project owner**; everyone else —
  including sharees who can read the thread — gets `404 not_found`
  (**never 403**). A comment id from a *different* project is also `404`.
  204 on success; non-numeric `cid` → `400 invalid_id`; absent /
  already-deleted → `404`.
- **`GET /api/v1/projects/{id}`** carries a `comments` array in **BOTH**
  projections (owner and sharee — comments are public), **oldest first**
  (`ORDER BY created_at, id` — the opposite of the notes ordering:
  same-second comments tie-break by id ascending, oldest first). Rows:
  `{id, author, mine, body, created_at}` where `author` is the author's
  display name and `mine` is relative to the **viewer** of the response.

## Project reference materials (2026-10-23)

Projects carry an attachment list of reference materials — **any file
type** (datasheets, manuals, images, whatever).

```
POST   /api/v1/projects/{id}/files — multipart field `file`; owner only
       (sharees/strangers → 404, even on shared projects). 201 returns the
       row {id, storage_key, thumb_key, filename, mime_type, size_bytes,
       created_at}. Cap 50MB (`MaxBytesReader`, over → 400 invalid_upload),
       empty file → 400. `mime_type` is always the SERVER-SNIFFED type
       (client Content-Type never trusted; `%PDF-` magic stored as
       application/pdf). `filename` is sanitized (path stripped). Images
       (sniffed `image/*`) also get a derived ≤480px `thumb_key` — null
       when generation failed (corrupt-but-sniffable images still store).
DELETE /api/v1/projects/{id}/files/{fid} — owner only. 204; non-numeric
       `fid` → 400 invalid_id; absent/foreign file → 404 not_found.
```

- **`GET /projects/{id}` gains `"files"` in BOTH projections** (owner and
  sharee — materials are public), oldest first. Same row shape as the 201.
- **`/files/{key}` serves project files to the owner OR any user when the
  project's `visibility` is `shared`** (matched against
  `storage_key OR thumb_key`; otherwise 404, no existence leak). Unlike
  documents, NO stored-mime pass-through: content type is **sniffed at
  serve time**, exactly like item photos — so the inline/attachment policy
  (`image/*`, `application/pdf`, `text/plain` inline; everything else
  forced to `attachment`) is re-evaluated on every fetch.
- **`DELETE /projects/{id}` removes every file's bytes** (originals AND
  thumbs) after the row delete succeeds. Byte-deletion failures are
  warn-logged, never surfaced (the 204 stands).

## Project bookmarks (2026-10-23)

Projects carry a list of URL bookmarks with best-effort auto-titling.

```
POST   /api/v1/projects/{id}/bookmarks — {url, title?}; owner only
       (sharees/strangers → 404, even on shared projects). 201 returns
       {id, url, title, created_at, archived_file}. `url` follows the
       shared external-URL contract (http/https, ≤2000 chars — same
       `validExternalURL` as product_url; violations → 400
       validation_error). An absent/blank `title` triggers a BEST-EFFORT
       auto-title: the direct fetch engine fetches the page (5s budget,
       256KB read cap, full SSRF policy like every fetch) and extracts
       `<title>` (entity-decoded, whitespace-collapsed, ≤200 chars). It
       NEVER blocks the add: any failure (dead host, non-HTML, policy
       rejection) falls back to the URL hostname; unparseable URLs fall
       back to the raw string. An explicit `title` wins untouched
       (trimmed, ≤200 chars).
PATCH  /api/v1/projects/{id}/bookmarks/{bid} — {url?, title?} → 200 with
       the updated row. Absent field = keep. A provided `url` is
       re-validated (400 on violation). 200/404 semantics as usual.
DELETE /api/v1/projects/{id}/bookmarks/{bid} — 204; absent/foreign →
       404. When the bookmark has an archived file (Task 7+), the
       archive row AND its bytes (original + thumb) are retired with it.
```

- **`GET /projects/{id}` carries `bookmarks` in BOTH projections** (owner
  and sharee — bookmarks are public), oldest first
  (`ORDER BY created_at, id`). Row: `{id, url, title, created_at,
  archived_file}` where `archived_file` is a project-file row shape
  (`{id, storage_key, thumb_key, filename, mime_type, size_bytes,
  created_at}`) or **null** until a bookmark has been archived.

## Bookmark archiving (2026-10-23)

`POST /api/v1/projects/{id}/bookmarks/{bid}/archive` snapshots the bookmark's
page as a PDF, reusing the item-fetch job machinery (same `GET /jobs/{id}`
polling, 90s deadline, single-flight, in-memory-only, owner-scoped jobs).

- **Owner-only**: sharees and strangers get `404 not_found`, even on shared
  projects (spec §6) — same as every other bookmark mutation. Non-numeric
  `bid` → `400 invalid_id`; absent/foreign bookmark → `404 not_found`.
- **Chromium required** (`chrome_path` / `STASHER_CHROME_PATH`): with
  chromium missing the **JOB fails** with the actionable chromium error —
  exactly like item archive fetches. The shipped binary wires the archive
  engine unconditionally, so `503 fetching_disabled` is reachable only on
  hand-built wirings that pass a nil engine; that (unshipped) check runs
  AFTER the bookmark lookup, so a missing bookmark 404s regardless
  (feature state is not probeable through someone else's bookmark).
- **`202 {job_id}`** on success. Done bookmark jobs carry **no
  `document_id`** (that key is item-fetch only) — re-fetch the project to
  see the archive.
- **Completion stores an ordinary `project_files` row** (`application/pdf`,
  filename `<sanitized bookmark title>.pdf`, path-stripped) and links it via
  the bookmark's `archived_file_id`. The archive therefore ALSO appears in
  the project's `files` (materials) list, and `bookmarks[].archived_file`
  populates on `GET /projects/{id}`.
- **Re-archive replaces**: a NEW file row is linked first, THEN the previous
  archive row and its bytes are deleted — storage keys never repeat, and at
  most one archive per bookmark exists at any time.
- **Failure is invisible**: a failed job (dead host, SSRF rejection, over
  the 25MB render cap, chromium missing, bookmark deleted mid-job) leaves
  the bookmark and any previous archive completely untouched — no
  `archived_file` appears, no orphan `project_files` rows remain.
