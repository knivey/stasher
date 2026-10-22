# Stasher Backlog

Items accepted as-is by the final whole-branch review (2026-10-21) or deferred
by task reviews with rulings. None block v1; triage into v1.1 as desired.
File references are to the state at commit `60c61fe`.

## Security hardening

- ~~Last-admin / self-demotion guard~~ — **done 2026-10-22** (cannot_demote_self
  + cannot_demote_last_admin, disabled admins don't count).
- ~~Rate-limit `/auth/register`~~ — **done 2026-10-22** (per-IP 10/min, before
  any DB work).
- ~~`X-Content-Type-Options: nosniff` on `/files/*`~~ — **done 2026-10-22**
  (all serve paths + 404 variants; 401s from requireAuth still lack it —
  JSON bodies, negligible).
- ~~Login timing side-channel~~ — **done 2026-10-22** (dummy-hash verify on
  unknown email, computed once at init).
- **Huge-`m` stored-hash allocation** — a corrupted/hostile password_hash with
  `m≈4e9` attempts a giant allocation before failing; hashes are self-generated
  today. (`internal/auth/password.go`)

## API consistency

- ~~`DELETE /projects/{id}/items/{itemId}` returns 204 for absent rows~~ —
  **done 2026-10-22** (`:execrows` → 404, double-delete test).
- **Project-item PATCH can't clear `qty_used`/`notes`** — pointer semantics;
  workaround is set 0/"". (`internal/api/projects_handlers.go`)
- **`PATCH /items` with `name: ""` silently keeps the name** (sentinel for
  "absent"); same for locations. Documented in AGENTS.md/api-notes; rejecting
  explicitly remains a v1.1 option.
- **Tag rename 409-before-404** — foreign id + colliding name returns 409, not
  404. Documented in `docs/api-notes.md`; acceptable.
- **`GET /items` empty page reports `total: 0`** — `count(*) OVER()` needs a
  row; page-1 reads are unaffected. Documented in api-notes; trash view clamps.
- **FTS whole-token semantics** — `q=rasp` doesn't match "raspberry" without
  `*`; consider prefix queries or frontend-side `*` appending.
- ~~qty_used unbounded~~ — **done 2026-10-22** (0..1e12 on add + patch,
  matching items.qty).

## Concurrency (all accepted: single-user windows, SQLite single-writer)

- Bootstrap-admin race (two concurrent first registrations both admin).
- Invite double-consume race (`GetInvite` → `MarkInviteUsed` not atomic).
- Location PATCH cycle-check/uniqueness outside the write tx (a formed cycle
  would make the breadcrumbs CTE recurse unboundedly — consider wrapping PATCH
  in a tx like DELETE already is, or a depth guard).
- Tag create check-then-insert race (UNIQUE backstop surfaces as 500 not 409).
- Item copy half-apply without rollback (partial duplicate on mid-copy failure;
  retry-safe, user can delete).
- Limiter 10k map reset wipes all buckets under key spray (bounded memory by
  design; oldest-eviction would be kinder).

## Ops / QoL

- ~~**CLI**: unknown subcommand silently serves; flags after the subcommand
  ignored~~ — **done 2026-10-22** (usage + exit 2 for unknown subcommands and
  flag-after-subcommand; `-h`/`--help` still uses flag's default output).
- ~~**Shutdown error ignored**~~ — **done 2026-10-22** (slog.Warn on drain
  failure).
- ~~**`TouchSession` writes on every authed request**~~ — **done 2026-10-22**
  (shouldTouch helper: skip writes < 60s, fail-safe on parse errors).
- **Orphan files on failed deletes** — key-list query errors and
  `storage.Delete` errors are swallowed; add logging or a periodic sweep.
- ~~**goose log noise**~~ — **done 2026-10-22** (`goose.SetLogger(NopLogger)`;
  SetVerbose was a no-op in v3.29.0 — errors still propagate).
- **Modernc DSN pragmas** — move WAL/foreign_keys/busy_timeout into the DSN so
  a silently-replaced pooled connection can't lose them.
- **goose global API** — deprecated `SetBaseFS/SetDialect/Up`; migrate to
  `goose.NewProvider` when convenient.

## Docs

- ~~locations `name` row should note provided-`""`-keeps; error-precedence
  asymmetry undocumented~~ — **done 2026-10-22** (AGENTS.md + api-notes).
- CI actions target Node 20 runtime (deprecation warning on runners) — bump
  actions/checkout@v5+, setup-go@v6+, setup-node@v5+ when convenient.

## Frontend v1.1 (from the frontend final review, 2026-10-21)

- ~~**Project delete UI**~~ — **done 2026-10-22** (detail view, ConfirmDialog
  reassuring items survive, tests).
- ~~**Extended smoke**~~ — **done 2026-10-22** (web/scripts/smoke-authed.mjs:
  puppeteer-core login flow at 375px against the real binary; in CI).
- ~~**`make check`**~~ — **done 2026-10-22** (runs Go vet+tests, web
  tests+typecheck, SPA build; CI runs it).
- ~~**Residual upload race**~~ — **done 2026-10-22** (pick inputs disabled
  while saving, attribute-pinned test).
- ~~**Negative `qty_used` savable**~~ — **done 2026-10-22** (backend bounds
  0..1e12 on add + patch).
- ~~Tap-target size for the mobile header's emoji links~~ — **done 2026-10-22**
  (44px targets); 5-char bottom-nav label truncation and the
  `locations.ts` cycle-comment nit remain.
- **Usage ledger leftovers** (2026-10-21): disposing a 0-qty item logs a
  delta-0 entry (consider 400 or idempotent no-op); ledger rows carry no
  project name (frontend falls back to `project #id` — backend could join
  it); ~~`found`/`restock` badges share a color~~ **done 2026-10-22**;
  history pager has no in-flight guard (matches the app-wide pagination
  pattern).
- **Trash leftovers** (2026-10-21): ~~purge-404 path is toast-only~~ **done
  2026-10-22** (refetches like restore); disposed-item restore branch
  untested; post-commit detail-fetch 500 window on restore; purge deletes
  files before row (failure order); trash sweep on GET is global not
  per-user; no snapshot size cap; item timestamps reset on restore (ledger
  timestamps preserved); weak count pin in empty-trash test.

