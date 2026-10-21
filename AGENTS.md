# AGENTS.md - Stasher

## Project Overview

Self-hosted home inventory: know what you own and where it is. Maker-style —
locations nest arbitrarily (building > cabinet > parts bin), items carry qty,
notes, photos, documents (datasheets), tags; projects link items with qty used.
Multi-user with separate inventories; per-item sharing (view/copy).

Single Go binary + SQLite (WAL, FTS5) + files on disk, deployed behind Caddy
for TLS. Vue 3 + Tailwind SPA frontend (plan pending — served from
`internal/webfs/dist` via go:embed once built).

**Module**: `stasher`
**Go**: 1.27

## Ground Rules

**NEVER** use `git add -f` to force-add files that are in `.gitignore`. The
`.gitignore` exists for a reason (data directories, build artifacts). If
`git add` refuses to track a file, respect that — do not override it.

**NEVER skip code reviews.** Reviews via the `requesting-code-review` skill
(or the per-task reviewer in subagent-driven development) are MANDATORY after
completing any task — no exceptions. "It's simple", "it's too small", "I
already tested it", or "the tests pass" are NOT valid reasons to skip review.

**Prefer subagents for implementation work.** Use the superpowers skills:
brainstorming → writing-plans → subagent-driven-development for features;
systematic-debugging for bugs; verification-before-completion before claiming
anything works. Dispatch subagents on the session's model (zai-coding-plan /
glm-5.3) unless instructed otherwise.

**The spec is the authority.** `docs/superpowers/specs/` beats the plan; the
plan beats convenience. Conflicts get ruled and recorded, not silently resolved.

## Commands

```bash
make build      # go build -o stasher ./cmd/stasher
make test       # go vet ./... && go test ./...
make generate   # go tool sqlc generate (REQUIRED after editing internal/db/queries.sql)
make run        # go run ./cmd/stasher

go test ./internal/api/ -v -run TestItem    # focused
./stasher migrate                             # apply migrations, exit
./stasher -config stasher.yaml                # serve with config
```

Config: YAML (`-config path`) + `STASHER_*` env overrides (LISTEN_ADDR,
BASE_URL, DATA_DIR, REGISTRATION, LOG_LEVEL). Data lives in `data/`
(stasher.db + uploads/) — gitignored, never commit it.

Headless Chromium is available on this system for browser-side checks.

## Architecture

```
cmd/stasher/          main: config → open → migrate → serve; SPA fallback; graceful shutdown
internal/api/         chi handlers, middleware, respond helpers (ALL HTTP)
internal/auth/        argon2, session tokens, rate limiter
internal/db/          open.go (pragmas + goose) + queries.sql + sqlc-generated
internal/migrations/  embedded goose SQL migrations
internal/files/       sharded upload storage (yyyy/mm keys, ValidKey gate)
internal/webfs/       go:embed of dist/ (SPA placeholder until frontend lands)
web/                  frontend sources (when built)
```

Key decisions (each has history in docs/superpowers/):

- **sqlc workflow**: write SQL in `internal/db/queries.sql`, `make generate`,
  commit the generated code. Query names are an API contract — don't rename.
  SQLite nullables arrive as `sql.Null*`; int64 for booleans (convert at the
  JSON boundary with `!= 0`).
- **Migrations**: goose files in `internal/migrations`, auto-applied at
  startup. Wrap trigger DDL in `-- +goose StatementBegin/StatementEnd` (goose's
  parser splits at inner semicolons otherwise).
- **One DB connection** (`SetMaxOpenConns(1)`) + WAL — intentional for
  modernc/SQLite; scale target is tens of thousands of items, a handful of
  users.
- **`GET /items` is hand-built SQL** (whitelisted sort columns + placeholder
  values) because sqlc can't parameterize ORDER BY. The whitelist is the
  injection boundary — identifiers ONLY from the Go map, never user input.
  See `buildItemListSQL` in items_handlers.go.
- **Isolation is in the data layer**: every user-owned query filters `user_id`;
  foreign/private resources return 404, never 403 (no existence leaks).
- **Auth**: Argon2id; session cookies are 10-year persistent, sessions never
  expire, revoked explicitly (logout/devices/admin-disable). Cookie holds the
  raw token; DB stores only its SHA-256 hash. Login is rate-limited
  (per-IP via last X-Forwarded-For entry — Caddy appends the real client last;
  per-account). First registered user becomes admin without an invite
  (bootstrap rule).
- **Files**: storage keys `yyyy/mm/<base64url>.<ext>`, validated by regex
  before any filesystem touch. Serving is session-gated owner-or-shared.
  Inline only `image/*`, `application/pdf`, `text/plain`; everything else
  downloads as attachment (XSS prevention). Uploads pass BARE single-token
  extensions to Storage.Save — never full filenames.
- **Timestamps**: TEXT RFC3339 UTC, set by Go, never SQL defaults.

## Testing Conventions

- `httptest.NewServer(s.Router())` against a real temp SQLite DB + real
  storage dir per test (`newTestServer` in internal/api/testutil_test.go) —
  real HTTP, real DB, no mocks of the system under test.
- `TestMain` silences chi's request logger; keep test output pristine.
- TDD: failing test first, watch it fail, implement, watch it pass.
- Stdlib testing only (no testify). Table-driven where natural.
- Error shape everywhere: `{"error":{"code":"...","message":"..."}}`.

## High-Signal Gotchas

- `docs/api-notes.md` documents the API contract quirks (PATCH nullable-field
  semantics, sort/order params, pagination clamps, file content-type policy).
  The frontend MUST follow it.
- `docs/backlog.md` is the accepted-as-is list from the final review —
  check it before "fixing" something that was deliberately deferred (or find
  it half-fixed).
- PATCH pointer semantics: absent field = keep. For items, `location_id: 0`
  clears (JSON null is indistinguishable from absent — documented wart).
  Locations: parent cannot be cleared via PATCH.
- Generated sqlc params may carry `_2` suffixes when a query repeats a column
  name — check the generated struct, don't guess.
- goose prints migration lines to stderr during tests; expected noise.
- Reviews deferred minors to the ledger during subagent-driven runs; the
  final review triages them into `docs/backlog.md`.

## Design Docs

- Spec: `docs/superpowers/specs/2026-10-21-stasher-design.md`
- Backend plan: `docs/superpowers/plans/2026-10-21-stasher-backend.md`
- API contract quirks: `docs/api-notes.md`
- Backlog: `docs/backlog.md`
