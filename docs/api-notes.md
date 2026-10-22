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
  (same shape as `POST /locations`) plus `"children_created": <int>`.
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
