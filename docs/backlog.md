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
- ~~Huge-`m` stored-hash allocation~~ — **done 2026-10-22** (VerifyPassword
  bounds-checks m ≤ 1 GiB / t ≤ 64 / p ≤ 16 before the KDF runs).

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
- ~~**FTS whole-token semantics** — `q=rasp` doesn't match "raspberry" without
  `*`~~ — **done 2026-10-22** (trigram tokenizer, migration 00005: substring
  matching for terms ≥ 3 chars; shorter terms return empty — see api-notes).
- ~~qty_used unbounded~~ — **done 2026-10-22** (0..1e12 on add + patch,
  matching items.qty).
- ~~Usage rows carry no project name~~ — **done 2026-10-22** (LEFT JOIN,
  `project_name` on rows + 201 body; frontend prefers it, `project #id`
  fallback kept).
- ~~Disposing a 0-qty item logs a delta-0 entry~~ — **done 2026-10-22**
  (idempotent 204, no ledger write, no updated_at bump).

## Concurrency (all accepted: single-user windows, SQLite single-writer)

- Bootstrap-admin race (two concurrent first registrations both admin).
- Invite double-consume race (`GetInvite` → `MarkInviteUsed` not atomic).
- ~~Location PATCH cycle-check/uniqueness outside the write tx~~ —
  **mitigated 2026-10-22**: both recursive CTEs are depth-capped (< 50), so a
  formed cycle can no longer hang the breadcrumbs/descendants queries. The
  PATCH itself remains non-tx (cycle formation still possible under
  concurrent same-user PATCHes, now harmless to query).
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
- ~~**Orphan files on failed deletes**~~ — **partially done 2026-10-22**:
  photo/trash-delete failures now `slog.Warn` instead of silent, backfill
  deletes its orphaned thumb when the row vanished mid-loop, and single-entry
  purge deletes the row BEFORE files. Still unlogged: document-upload rollback
  delete and `handleDeleteDocument` (files_handlers.go:375/:412). A periodic
  orphan sweep remains unimplemented.
- ~~**goose log noise**~~ — **done 2026-10-22** (`goose.SetLogger(NopLogger)`;
  SetVerbose was a no-op in v3.29.0 — errors still propagate).
- ~~**Modernc DSN pragmas**~~ — **done 2026-10-22** (WAL/foreign_keys/
  busy_timeout in the DSN so any pooled connection gets them; post-open loop
  kept as belt-and-suspenders).
- **goose global API** — deprecated `SetBaseFS/SetDialect/Up`; migrate to
  `goose.NewProvider` when convenient.

## Docs

- ~~locations `name` row should note provided-`""`-keeps; error-precedence
  asymmetry undocumented~~ — **done 2026-10-22** (AGENTS.md + api-notes).
- ~~CI actions target Node 20 runtime~~ — **done 2026-10-22**
  (checkout@v5, setup-go@v6, setup-node@v5).

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
  (44px targets); ~~5-char bottom-nav label truncation~~ **done 2026-10-22**
  (full labels, 11px); the `locations.ts` cycle-comment operative claims were
  corrected 2026-10-22 — one illustrative clause (tail-reached cycle) describes
  an impossible state and may be pruned someday.
- **Usage ledger leftovers** (2026-10-21): ~~disposing a 0-qty item logs a
  delta-0 entry~~ **done 2026-10-22**; ~~ledger rows carry no project
  name~~ **done 2026-10-22**; ~~`found`/`restock` badges share a color~~
  **done 2026-10-22**; history pager has no in-flight guard (matches the
  app-wide pagination pattern).
- **Trash leftovers** (2026-10-21): ~~purge-404 path is toast-only~~ **done
  2026-10-22** (refetches like restore); ~~disposed-item restore branch
  untested~~ **done 2026-10-22**; post-commit detail-fetch 500 window on
  restore; ~~purge deletes files before row (failure order)~~ **done
  2026-10-22** (row first now); trash sweep on GET is global not per-user; no
  snapshot size cap; item timestamps reset on restore (ledger/note timestamps
  preserved); ~~weak count pin in empty-trash test~~ **judged fine 2026-10-22**
  (page-1 leak surfaces in count).

## External content (2026-10-22, task 1 review)

- Per-hop 30s timeout: a 5-hop redirect chain can run ~3 minutes under the
  direct engine's 30s-per-request client timeout. The job layer (task 2)
  should impose an overall deadline on fetch jobs rather than relying on the
  engine's per-request timeout.
- SSRF policy rejects the spec-exact Global-Constraint list only; still
  allowed (future hardening candidates): 0.0.0.0/8, 240.0.0.0/4 (reserved +
  broadcast), and IPv4-compatible IPv6 ::/96. PublicPolicy is hard-coded, so
  widening it is a one-place change when ruled on.

## Notes feature review minors (2026-10-22)

- Note body cap counts bytes not runes (consistent with other limits; client
  maxlength counts chars — CJK edge mismatch documented in api-notes).
- No cap on notes per item (scale target: fine).
- Fetch-in-flight at ItemsView unmount repopulates the cleared list (narrow
  stale-flash window; needs request cancellation to fully fix).
