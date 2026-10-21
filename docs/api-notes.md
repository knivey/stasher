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
