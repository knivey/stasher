# Stasher — Personal/Home Inventory System

**Date:** 2026-10-21
**Status:** Approved design (pre-implementation)

## 1. Purpose

Stasher is a self-hosted, internet-facing inventory system for keeping track of
what you own and where it is, in the maker style: locations nest arbitrarily
(building > cabinet > parts bin — or just "garage"), items carry quantities,
notes, photos, and documents (datasheets, manuals), tags categorize, and
projects record what items were bought for and how much was used.

Multiple users (the owner plus friends) each get a **completely separate
inventory**. Users can opt to share individual items so others can view and
copy them. Collaboration (shared projects) is a future phase.

Success criteria:

- On desktop and phone (browser, installable as PWA), a user can find any item
  in seconds via full-text search or filters.
- Adding an item with a photo taken on the phone is a quick, single flow.
- The whole system is one binary + one data directory behind Caddy; backups are
  copying that directory.
- Schema changes ship as migrations applied automatically at startup.

## 2. Stack & Constraints

| Concern | Choice |
|---|---|
| Backend | Go (module `stasher`), `chi` router, stdlib `net/http` handlers |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no CGO), WAL mode, `foreign_keys=on`, `busy_timeout` |
| DB access | `sqlc` — handwritten SQL, generated type-safe Go |
| Migrations | `goose` SQL files embedded via `go:embed`, auto-applied on startup (plus `stasher migrate` subcommand) |
| Auth | Server-side sessions; Argon2id password hashing |
| Frontend | Vue 3 + TypeScript + Vite + Tailwind CSS v4, Pinia, vue-router |
| Search | SQLite FTS5 over item name + description, trigger-synced |
| Files | On disk under `data/uploads/` (sharded `yyyy/mm`), metadata in DB |
| Deployment | Single binary serving API + embedded SPA; Caddy in front for TLS |
| Scale target | Tens of thousands of items, handful of users |

## 3. Architecture

```
phone/desktop browser
   │ HTTPS
   ▼
Caddy (TLS, host reverse proxy)
   │ HTTP → localhost:8080
   ▼
stasher (single Go binary)
   ├── /api/v1/*        → JSON API (chi)
   ├── /files/*         → session-gated photo/document serving
   └── *                → embedded Vue SPA (go:embed of web/dist)
   ├── data/stasher.db  → SQLite (WAL)
   └── data/uploads/    → photos & documents
```

- Config via small YAML file (overridable by env vars): `listen_addr`
  (default `127.0.0.1:8080`), `base_url`, `data_dir`, `registration`
  (`invite` | `open`, default `invite`), log level.
- Startup order: load config → open DB → apply migrations → serve.
- SPA is served for all non-API routes (history-mode routing).
- Vite dev server proxies `/api` and `/files` to the Go server on `:8080`.

## 4. Data Model

All ids are `INTEGER PRIMARY KEY`. All timestamps are UTC. Every
user-owned row carries `user_id`; every query filters by it — isolation is
enforced in the data layer, not just the UI.

### users
`email` (unique, lowercase), `password_hash` (Argon2id), `display_name`,
`default_item_visibility` (`private` | `shared`, default `private`),
`is_admin` (default 0; **the first registered user becomes admin**),
`created_at`, `updated_at`.

### sessions
`token_hash` (unique; SHA-256 of the 256-bit random token sent in the cookie —
the DB never stores usable tokens), `user_id`, `user_agent`, `last_ip`,
`created_at`, `last_seen_at`.

**Sessions never expire.** They end only when the user logs out, revokes the
device from the account page, uses "log out everywhere", or (optionally, via
prompt) changes password. Revocation deletes the row.

### locations
`user_id`, `name`, `parent_id` (nullable self-FK, same user), `notes`,
`created_at`, `updated_at`. Arbitrary depth; cycles are prevented in
application logic (reject a move that would make an ancestor a descendant).
Uniqueness of `(user_id, parent_id, name)` is enforced in application logic
(SQLite treats NULLs as distinct in unique indexes).

### items
`user_id`, `name`, `description` (free text notes), `qty` (NUMERIC, default 1 —
allows e.g. `2.5`), `unit` (optional free text, e.g. `m`, `pcs`),
`location_id` (nullable FK to locations), `visibility` (`private` | `shared`,
default = owner's `default_item_visibility` at creation),
`created_at`, `updated_at`.

### item_photos
`item_id` (FK CASCADE), `storage_key`, `caption` (optional), `sort_order`
(default 0), `created_at`.

### item_documents
`item_id` (FK CASCADE), `storage_key`, `filename`, `mime_type`, `size_bytes`,
`created_at`.

### tags / item_tags
`tags`: `user_id`, `name`, unique `(user_id, name)`.
`item_tags`: `item_id` (FK CASCADE), `tag_id` (FK CASCADE), PK `(item_id, tag_id)`.

### projects / project_items
`projects`: `user_id`, `name`, `status` (`planned` | `active` | `done` |
`archived`, default `planned`), `notes`, `created_at`, `updated_at`.
`project_items`: `project_id` (FK CASCADE), `item_id` (FK CASCADE), `qty_used`
(NUMERIC, optional), `notes`, PK `(project_id, item_id)`.

### Full-text search
`items_fts`: FTS5 external-content table over `items(name, description)`,
kept in sync by INSERT/UPDATE/DELETE triggers. Tags are **not** indexed;
they filter via `tag` query parameters.

## 5. Auth, Users, Sessions

- **Registration:** invite-code based by default; admin generates invite codes.
  Config `registration: open` allows direct signup. First user becomes admin.
- **Login:** email + password → Argon2id verify → create session → set cookie
  (`httpOnly`, `Secure`, `SameSite=Lax`, path `/`). Rate-limited per-IP and
  per-account.
- **Account page:** edit profile, change default visibility, change password
  (offers to revoke all other sessions), manage **devices** (list sessions:
  user agent, last IP, last seen; revoke individually or "log out everywhere").
- **Admin page:** list/disable users, generate invite codes. Disabling a user
  also revokes all their sessions immediately; their data is kept.

## 6. Sharing (V1 scope)

- An item's `visibility` is `private` (default) or `shared`. New items inherit
  the owner's `default_item_visibility`.
- `GET /shared` lists items with `visibility='shared'` from **other** users,
  with owner display names. Private items are never visible to anyone else.
- `POST /items/{id}/copy` copies an item that is either **yours** (duplicate)
  or **shared by another user** into your inventory: duplicates the item row
  and tags, physically copies photo/document files (new storage keys; files are
  never shared between rows), and sets visibility per your default. Project
  links are personal and are **not** copied.
- Collaborative projects, per-user grants, and share links are future phases.

## 7. API Surface (`/api/v1`, REST + JSON)

Errors use `{"error": {"code": "...", "message": "..."}}`. List endpoints take
`page` (1-based) and `page_size` (default 50, max 200) and return
`{"data": [...], "total": n}`.

| Area | Endpoints |
|---|---|
| Auth | `POST /auth/register`, `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `GET /auth/sessions`, `DELETE /auth/sessions/{id}`, `POST /auth/sessions/revoke-all` |
| Items | `GET /items` (filters: `q` full-text, `location_id` (includes descendants), `tag`, `project_id`, `visibility`; sort), `POST /items`, `GET /items/{id}`, `PATCH /items/{id}`, `DELETE /items/{id}`, `POST /items/{id}/copy` |
| Photos | `POST /items/{id}/photos` (multipart), `DELETE /items/{id}/photos/{photoId}`, `PATCH /items/{id}/photos/{photoId}` (caption/sort) |
| Documents | `POST /items/{id}/documents` (multipart), `DELETE /items/{id}/documents/{docId}` |
| Files | `GET /files/{storageKey}` — session-gated, only if you own the item or the item is shared |
| Locations | `GET /locations` (full tree), `POST /locations`, `GET /locations/{id}` (breadcrumbs), `PATCH /locations/{id}`, `DELETE /locations/{id}` (items keep `location_id=NULL`; child locations move up) |
| Tags | `GET /tags`, `POST /tags`, `PATCH /tags/{id}`, `DELETE /tags/{id}` |
| Projects | `GET /projects`, `POST /projects`, `GET /projects/{id}`, `PATCH /projects/{id}`, `DELETE /projects/{id}`, `POST /projects/{id}/items`, `PATCH /projects/{id}/items/{itemId}`, `DELETE /projects/{id}/items/{itemId}` |
| Shared | `GET /shared` |
| Admin | `GET /admin/users`, `PATCH /admin/users/{id}` (enable/disable), `GET /admin/invites`, `POST /admin/invites`, `DELETE /admin/invites/{id}` |

## 8. Frontend

Vue 3 + TypeScript + Vite + Tailwind v4, Pinia, vue-router. Mobile-first,
responsive (cards on phone, tables on desktop). PWA manifest + icons so it
installs to the home screen; no offline caching beyond the shell in V1.

**Views:**

- Login / Register (invite code field when required)
- Dashboard — item/location/project counts, recently added, active projects
- Items list — search box (FTS), filters (location, tag, project, visibility),
  sort; paginated; table on desktop / cards on mobile
- Item detail — photo gallery, documents, notes, qty+unit, location breadcrumb,
  tags, linked projects (qty used)
- Item form — create/edit, photo & document upload
- Locations — tree view with add/rename/move/delete, breadcrumbs
- Tags — list with item counts, rename, merge-free (rename only)
- Projects — list + detail (linked items with qty used, status)
- Shared — browse others' shared items, copy button
- Settings — profile, default visibility, change password, **devices**
- Admin — users, invite codes

**Photo upload on phones:** `<input type="file" accept="image/*" capture="environment">`
opens the camera; the client downscales to max ~2000px JPEG before upload.

## 9. Project Layout

```
stasher/
├── cmd/stasher/          # main: config, migrate, serve
├── internal/
│   ├── api/              # chi router, handlers, middleware
│   ├── auth/             # sessions, argon2, invites
│   ├── db/               # sqlc-generated code + queries.sql + schema
│   ├── migrations/       # goose SQL migrations (embedded)
│   ├── files/            # upload storage, storage keys
│   └── config/           # YAML + env config
└── web/                  # Vue app; dist embedded via go:embed
```

## 10. Testing

- **Backend (TDD):** each handler/feature gets `httptest` tests against the
  real router with a fresh temp-file SQLite DB (migrations applied) per test.
  Ownership isolation, sharing rules, and location-cycle prevention get
  dedicated test cases.
- **Frontend:** Vitest for stores and utilities (API client, image downscale);
  component smoke tests where valuable. Playwright E2E deferred.
- **Build checks:** `sqlc generate` + `go vet` + `go test` + `vue-tsc` +
  `vitest` in CI/dev script.

## 11. Out of Scope (future phases)

- Collaborative projects / shared editing
- QR/barcode labels and in-browser scanning for bin lookup
- Valuation, insurance, receipts, serial numbers
- Low-stock thresholds and alerts
- Item history/audit log, CSV export/import
- Postgres migration (if write contention or scale ever demands it)
