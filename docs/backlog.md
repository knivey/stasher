# Stasher Backlog

Record of deferred findings and their dispositions. The 2026-10-23 backlog
round resolved or closed every open item except two deliberately deferred
below. Kept for history; strikethrough = resolved.

## Security hardening

- ~~Last-admin / self-demotion guard~~ — **done 2026-10-22**.
- ~~Rate-limit `/auth/register`~~ — **done 2026-10-22**.
- ~~`nosniff` on `/files/*`~~ — **done 2026-10-22** (401s from requireAuth
  still lack it — JSON bodies, negligible; **closed accepted**).
- ~~Login timing side-channel~~ — **done 2026-10-22**.
- ~~Huge-`m` stored-hash allocation~~ — **done 2026-10-22**.

## API consistency

- ~~`DELETE /projects/{id}/items/{itemId}` 204-for-absent~~ — **done
  2026-10-22** (`:execrows` → 404).
- **Project-item PATCH can't clear `qty_used`/`notes`** — **closed accepted
  2026-10-23**: JSON null is indistinguishable from absent (the app-wide
  PATCH wart); workaround is set 0/"". No natural sentinel exists for a
  numeric nullable; changing it means a RawMessage decode — not worth it.
- **`PATCH` with `name: ""` keeps the name** (items, locations; bookmarks
  title mirrors it) — **closed accepted 2026-10-23**: the sentinel is
  load-bearing for diff-PATCH flows and documented in api-notes.
- ~~**Tag rename 409-before-404**~~ — **done 2026-10-23** (ownership check
  first; collision still 409 `tag_exists`).
- ~~**Tag create check-then-insert race (UNIQUE → 500)**~~ — **done
  2026-10-23** (insert-only; UNIQUE violation → 409 `duplicate_tag`).
- **`GET /items` empty page reports `total: 0`** — **closed accepted
  2026-10-23**: `count(*) OVER()` needs a row; page-1 reads unaffected,
  trash clamps. A separate count query costs more than it buys.
- ~~FTS whole-token semantics~~ — **done 2026-10-22** (trigram).
- ~~qty_used unbounded~~ — **done 2026-10-22**.
- ~~Usage rows carry no project name~~ — **done 2026-10-22**.
- ~~0-qty dispose logs delta-0~~ — **done 2026-10-22**.

## Concurrency — **all closed accepted 2026-10-23**

Single-user windows under SQLite's single-writer + one-connection pool;
recorded, not scheduled: bootstrap-admin race; invite double-consume;
location PATCH cycle-check outside write tx (formed cycles are harmless
since the depth caps — mitigated 2026-10-22); item copy half-apply
(retry-safe); limiter 10k map reset (bounded by design). (Tag create race
was the one with user impact — fixed above.)

## Ops / QoL

- ~~CLI unknown subcommand~~ / ~~shutdown error~~ / ~~TouchSession write
  churn~~ / ~~goose log noise~~ / ~~DSN pragmas~~ — **done 2026-10-22**.
- ~~**Unlogged file deletes**~~ — **done 2026-10-23** (doc-upload rollback
  + handleDeleteDocument now warn-log via `deleteFileBytes`; every storage
  delete in the codebase now logs failures).
- **Periodic orphan-file sweep** — **open, deferred**: all delete paths now
  warn-log, so orphans are visible in logs; a sweep needs a files↔DB
  reconciliation walk. Do if orphan warnings ever show up in practice.
- **goose global API → NewProvider** — **open, deliberately deferred**: the
  deprecated calls work identically; the migration touches startup on the
  deployment-critical path for zero behavior change. Revisit when a goose
  upgrade forces it.

## Docs

- ~~locations `""`-keeps + error-precedence~~ — **done 2026-10-22**.
- ~~CI actions Node 20~~ — **done 2026-10-22**.

## Frontend v1.1 (2026-10-21 review)

- Project delete UI / extended smoke / `make check` / upload race /
  negative qty_used / tap targets / label truncation / found-restock badge
  colors / purge-404 refetch / disposed-restore test / purge delete order /
  empty-trash count pin — **all done 2026-10-22**.
- ~~locations.ts impossible tail-reached-cycle clause~~ — **pruned
  2026-10-23**.
- History pager in-flight guard — **closed accepted**: matches the
  app-wide pagination pattern.
- **Trash leftovers — closed accepted 2026-10-23**: post-commit
  detail-fetch 500 window on restore (narrow); trash sweep on GET global
  not per-user (only expired entries, no leak); no snapshot size cap (scale
  target); item timestamps reset on restore (ledger/note timestamps
  preserved; documented).

## External content (2026-10-22)

- ~~Per-hop 30s timeout → unbounded chains~~ — **was already resolved** by
  the job layer's 90s overall `jobDeadline` (covers queue wait + fetch);
  struck 2026-10-23 when verified.
- ~~SSRF policy gaps: 0.0.0.0/8, 240.0.0.0/4, ::/96~~ — **done 2026-10-23**
  (PublicPolicy widened; loopback pinned still-allowed for tests).

## Notes feature (2026-10-22) — closed accepted 2026-10-23

Byte-count caps with char-count maxlength (CJK edge, documented);
uncapped notes per item (scale target); ItemsView unmount stale-flash
(needs request cancellation — an app-wide client concern, not worth a
one-off).

## Projects flesh-out (2026-10-23) — resolved in the backlog round

- ~~Bookmark `title: ""` asymmetry~~ — **resolved 2026-10-23**: PATCH
  empty/control-only title now KEEPS the current title (empty-name sentinel
  semantics, matching items/locations); documented in api-notes.
- ~~Titles with newlines break archived-PDF filenames~~ — **done
  2026-10-23** (control chars stripped in both title paths; rune-safe
  200-char cap).
- ~~Sharee BOM rows keyed by item_name~~ — **done 2026-10-23**
  (item_id-or-name+index; re-render test pins no duplicate-key warning).
- ~~Sharee picker load~~ — **done 2026-10-23** (owner_view-gated).
- ~~SharedView empty-state flash~~ — **done 2026-10-23** (projectsLoaded
  gate, error-safe).
- ~~Multi-file upload churn~~ — **done 2026-10-23** (single refresh;
  filename-prefixed error toasts; per-file isolation kept).
- ~~Owner deleting others' comments has no UI~~ — **done 2026-10-23**
  (`owner` prop; mine-or-owner affordance).
- Bookmark PATCH-title UI — **closed by ruling**: auto-title + the
  control-char hardening suffice; inline title editing adds surface for
  marginal value. Reopen if users ask.
- ~~No thumb-degradation test for project files~~ / ~~byte-cap rune
  split~~ — **done 2026-10-23** (test ported; cap now rune-safe).

## AI autofill (2026-10-23, final review)

- Rate check runs after full body buffering — an authenticated hammerer
  forces repeated ~80MB buffering before each 429 (ceiling is
  spec-mandated; moving Allow earlier trades 415-burns for tokens).
- Multipart temp files (parts >1MB) never RemoveAll'd — repo-wide pattern
  across all ParseMultipartForm sites; fix repo-wide or never.
- No-content sentinel branch (`{"choices":[]}` → 400) untested — two-line
  addition to internal/ai tests.
- Edit-form originals fetch: `resp.blob()` outside the guarded block (rare
  mid-read failure toasts unprefixed); `Promise.all` surfaces only the
  first failure (allSettled + joined message would report all).
- `truncateUTF8` rune-safety has no direct unit test (ASCII lengths pinned
  only). Byte-measured clamps vs spec "chars" is deliberate (item
  validator enforces bytes — prefill must survive create); don't "fix".
