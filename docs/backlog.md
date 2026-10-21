# Stasher Backlog

Items accepted as-is by the final whole-branch review (2026-10-21) or deferred
by task reviews with rulings. None block v1; triage into v1.1 as desired.
File references are to the state at commit `60c61fe`.

## Security hardening

- **Last-admin / self-demotion guard** — an admin can set `is_admin: false` on
  themselves; the only admin on an invite-mode instance leaves it unmanageable
  (recovery = raw SQLite). Mirror the `cannot_disable_self` guard.
  (`internal/api/admin_handlers.go`)
- **Rate-limit `/auth/register`** — in `registration: open` mode it is
  unauthenticated Argon2 grinding + account spam on an internet-facing host.
  Default invite mode is safe (code check precedes hashing).
- **`X-Content-Type-Options: nosniff`** on `/files/*` responses — defense in
  depth on top of explicit Content-Type. (`internal/api/files_handlers.go`)
- **Login timing side-channel** — unknown email returns fast (no argon2), wrong
  password slow. A dummy-hash verify equalizes. (`internal/api/auth_handlers.go`)
- **Huge-`m` stored-hash allocation** — a corrupted/hostile password_hash with
  `m≈4e9` attempts a giant allocation before failing; hashes are self-generated
  today. (`internal/auth/password.go`)

## API consistency

- **`DELETE /projects/{id}/items/{itemId}` returns 204 for absent rows** —
  diverges from tags' post-fix 404 semantics (`:exec` vs `:execrows`).
- **Project-item PATCH can't clear `qty_used`/`notes`** — pointer semantics;
  workaround is set 0/"". (`internal/api/projects_handlers.go`)
- **`PATCH /items` with `name: ""` silently keeps the name** (sentinel for
  "absent"); same for locations. Document or reject explicitly.
- **Tag rename 409-before-404** — foreign id + colliding name returns 409, not
  404. Documented in `docs/api-notes.md`; acceptable.
- **`GET /items` empty page reports `total: 0`** — `count(*) OVER()` needs a
  row; page-1 reads are unaffected. Documented in api-notes.
- **FTS whole-token semantics** — `q=rasp` doesn't match "raspberry" without
  `*`; consider prefix queries or frontend-side `*` appending.

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

- **CLI**: unknown subcommand silently serves (`stasher serv` boots); flags
  after the subcommand are ignored (`stasher migrate -config x` uses defaults).
- **Shutdown error ignored** — a hung drain is indistinguishable from clean.
- **`TouchSession` writes on every authed request** — write amplifier on the
  single conn; throttle to ~60s per session.
- **Orphan files on failed deletes** — key-list query errors and
  `storage.Delete` errors are swallowed; add logging or a periodic sweep.
- **goose log noise** — `goose.SetVerbose(false)` quiets migration lines in
  every test run.
- **Modernc DSN pragmas** — move WAL/foreign_keys/busy_timeout into the DSN so
  a silently-replaced pooled connection can't lose them.
- **goose global API** — deprecated `SetBaseFS/SetDialect/Up`; migrate to
  `goose.NewProvider` when convenient.

## Docs

- `docs/api-notes.md`: locations `name` row should note provided-`""`-keeps
  (items row documents the pattern); create-vs-PATCH tags/location error
  precedence asymmetry (400-on-create, 404-on-PATCH) undocumented.
