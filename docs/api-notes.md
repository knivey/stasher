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
  is optional and must be owned. 201 returns the entry.
- `GET /api/v1/items/{id}/usage` — paginated ledger, newest first; rows
  `{id, delta, resulting_qty, kind, project_id, note, created_at}`.
- **Auto-logging**: item create and copy write an initial `adjustment`
  entry (note `initial`); any PATCH that *changes* qty writes an
  `adjustment` entry (delta = new − old) in the same transaction. No-op
  qty PATCHes write nothing.
- **Flag rule**: `disposed` appears on item detail and both list queries.
  ANY change resulting in qty > 0 clears it (usage POST and PATCH alike);
  landing on 0 via a non-disposed kind leaves the flag unchanged.
- **Delete stays for mistakes only** — it removes the item (and its
  ledger, via FK cascade) entirely; disposal is the qty-0 ledger path.
