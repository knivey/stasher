# Projects Flesh-Out: Sharing, Notes, Comments, Materials, Bookmarks

**Date:** 2026-10-23
**Status:** Approved design (pre-implementation)

## 1. Purpose

Projects today are a name, a status, a notes blob, and a list of linked
items with quantities. This round makes a project a place where the actual
work of building something lives: private working notes, attached reference
materials (pinout charts, datasheets), URL bookmarks with opt-in page
archiving, and — when the project is shared — a comments thread where other
users on the instance can discuss it.

Sharing follows the items precedent exactly: a `private`/`shared` visibility
toggle. A shared project is viewable read-only by every logged-in user,
with the linked BOM masked so private inventory is never exposed.

Success criteria:

- Toggle a project shared → another user finds it under Shared → Projects,
  opens it, and sees the BOM (private items as name + qty used only),
  bookmarks and their archived PDFs, materials — and can post a comment.
  The owner's notes log is invisible to them.
- Owner adds a bookmark, hits Archive, and a full-page PDF (chromium) lands
  in reference materials linked to the bookmark; re-archiving replaces it.
- Video pages (YouTube etc.) get a soft "this captures the page, not the
  video" confirm before archiving — proceeding is allowed.
- Materials accept any file type in one list; images get EXIF-aware
  thumbnails; serving obeys the existing inline/attachment policy.
- Every foreign or private resource 404s; no existence leaks.

## 2. Sharing & access model

- `projects.visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility
  IN ('private','shared'))`. Settable at create (`private` unless
  explicitly provided — projects do **not** follow the per-user
  default-visibility setting items use) and via `PATCH`; toggled on-page:
  the visibility badge on ProjectDetailView **is** the toggle (the
  ItemDetailView pattern).
- **Owner**: full control of everything.
- **Sharee** (any other logged-in user, project shared):
  - `GET /projects/{id}` — viewer projection (below).
  - Read comments; post comments; delete **own** comments.
  - Read bookmarks and open/download archived PDFs via `/files`.
  - Read materials and download them via `/files`.
  - Nothing else: no edit, no notes, no upload, no bookmark management, no
    archiving.
- **Private or foreign project** → 404 on every project endpoint, before
  any validation (error precedence: ownership first).
- **SharedView** gains a Projects section: shared projects from others
  (`ListSharedProjectsFromOthers`), showing name, owner display name,
  status, linking to the detail view.

### BOM projection (`GET /projects/{id}` items list)

Per linked item, visibility of the **item** governs its row:

- Item `shared` → full row: `{item_id, name, qty, qty_used, notes,
  shared: true}` — clickable through to the item view.
- Item `private` → `{name, qty_used, shared: false}` — no item_id, no qty,
  no link notes, not clickable. (Name + qty used is the point of sharing a
  BOM; the inventory behind it stays closed.)

## 3. Notes — owner-only log

Mirrors `item_notes` (migration 00006) exactly:

- `project_notes(id, project_id → projects CASCADE, body, created_at)`;
  no user_id needed — reachable only by the owner.
- `POST /projects/{id}/notes {body}` → 201 entry; `DELETE
  /projects/{id}/notes/{noteId}` → 204 (404 when absent).
- Included in `GET /projects/{id}` **owner projection only** — entirely
  absent from the sharee response (not an empty array: the key is omitted).
- Body cap matches item notes (10,000 bytes, byte-count semantics,
  documented CJK maxlength mismatch). Timestamped, append-only, no edit.
- The pre-existing `projects.notes` blob stays as-is in the schema; the UI
  relabels it **Description** (edit form + detail header) so "notes" means
  the log, matching the items vocabulary (description + notes thread).

## 4. Comments — public thread on shared projects

- `project_comments(id, project_id → projects CASCADE, user_id → users
  CASCADE, body, created_at)`.
- `POST /projects/{id}/comments {body}` → 201: owner always; any user **if
  the project is shared** (private → 404 for non-owners; owner commenting
  on a private project is allowed — the thread exists, it's just solo).
- `DELETE /projects/{id}/comments/{cid}` → 204: author deletes own;
  project owner deletes any; otherwise 404.
- Entries render `author` (display name via users join, never email),
  `created_at`, and a `mine` flag for the delete-own affordance.
- Same 10,000-byte body cap. No threading, no editing, no reactions.

## 5. Reference materials — one unified attachments list

One section, one upload button, any file type — a project is a reference
pile, not a photo album.

- `project_files(id, project_id → projects CASCADE, storage_key NOT NULL,
  thumb_key NULL, filename, mime_type, size_bytes, created_at)`.
- `POST /projects/{id}/files` — multipart `file`, owner only. Mirrors the
  item document upload rules: size cap, mime sniffed from bytes (client
  content-type ignored), filename sanitized. Images (`image/*` after
  sniff) additionally get an EXIF-aware thumbnail via `thumbs.Generate`;
  thumb failure degrades to null + warn log. Save order: files first, row
  second, warn-log orphan cleanup on row failure (the established
  crash-safe pattern).
- `DELETE /projects/{id}/files/{fid}` → 204; row first, then bytes.
- Response rows: `{id, filename, mime_type, size_bytes, thumb_key,
  storage_key, created_at}`. Frontend: `thumb_key ?? storage_key` for
  image previews; non-images list with icon + filename + humanized size.
- Serving via the existing `/files/{key}` gate (below) — inline only for
  the safe set (image/*, application/pdf, text/plain), attachment
  otherwise, `nosniff` always.
- Visible to sharees when shared (that's the point — "here's the datasheet
  I used").

## 6. URL bookmarks + opt-in archiving

- `project_bookmarks(id, project_id → projects CASCADE, url, title,
  archived_file_id → project_files ON DELETE SET NULL, created_at)`.
- `POST /projects/{id}/bookmarks {url, title?}` → 201, owner only.
  - `url`: absolute `http`/`https`, host required, length ≤ 2048,
    scheme-relative/rejecting everything else.
  - `title`: optional; when absent, best-effort auto-title: a
    **title-fetch** through the fetcher package's policy/pinning machinery
    — like the direct engine but permitting `text/html`, 5s timeout, 256KB
    read cap, extract `<title>` (case-insensitive, entity-decoded
    minimally), sanitized to one line. Any failure → the hostname.
    Owner-initiated, so no cross-user SSRF surface beyond what direct
    fetch already offers.
- `PATCH /projects/{id}/bookmarks/{bid} {url?, title?}` → owner only,
  absent = keep, same validation.
- `DELETE …/bookmarks/{bid}` → owner only; also deletes the linked
  archive file (row + bytes).
- Rendering: `{id, url, title, archived_file: {…file row…} | null,
  created_at}` — the archived PDF is a `project_files` row (so it shows in
  materials too) linked back to the bookmark.

### Archiving

- `POST /projects/{id}/bookmarks/{bid}/archive` → `202 {job_id}`, owner
  only. Runs the **existing in-memory jobs system** (single-flight,
  server-wide queue, 90s deadline, owner-scoped polling via
  `GET /jobs/{id}`) with the **existing ArchiveEngine** (chromium
  `--print-to-pdf`).
- Job completion today hard-codes "attach to item"; this adds a second
  completion target. The job request carries a target discriminator —
  `item-document` (existing) or `bookmark-archive {projectID, bookmarkID}`
  — and `runJob` switches on it. No persistence, no polling-shape change.
- `bookmark-archive` completion: store the PDF as a `project_files` row
  (filename = sanitized bookmark title + `.pdf`, mime `application/pdf`,
  size), link `archived_file_id`. Re-archive replaces: link the new row,
  then delete the old row + bytes. Archived files are deletable directly
  from materials too (bookmark's link goes NULL via ON DELETE SET NULL).
- **Video-host guard** (frontend only): a short constant list —
  `youtube.com, youtu.be, m.youtube.com, vimeo.com, twitch.tv,
  dailymotion.com` — matched on the bookmark's hostname; Archive shows a
  confirm ("Archiving captures the page as a PDF, not the video — archive
  anyway?"). Proceeding is allowed.

## 7. `/files` gate extension

Third lookup branch (after item photos, documents), same shape:

- `GetProjectFileByKey`: `SELECT p.user_id, p.visibility FROM project_files
  f JOIN projects p ON … WHERE f.storage_key = ? OR f.thumb_key = ?`.
- Owner always; `visibility = 'shared'` lets any user in. Content type
  sniffed at serve time when stored mime is empty/octet-stream; the
  inline/attachment policy and `nosniff` apply unchanged.
- Indexes on `storage_key` and `thumb_key` (the established gate-lookup
  pattern).

## 8. Project deletion cleanup

`DELETE /projects/{id}`: FK cascades remove all child rows, but file
**bytes** need explicit cleanup — gather every `project_files` key
(including archive PDFs) before the delete, remove the bytes after commit
(warn-log failures). Same pattern as delete-location-with-photo.

## 9. API surface summary

| Endpoint | Who | Notes |
|---|---|---|
| `PATCH /projects/{id}` | owner | + `visibility` (absent = keep, else `private`\|`shared`) |
| `POST /projects/{id}/notes` | owner | 201 entry |
| `DELETE /projects/{id}/notes/{nid}` | owner | 204 / 404 |
| `POST /projects/{id}/comments` | owner; sharee if shared | 201 entry |
| `DELETE /projects/{id}/comments/{cid}` | author or owner | 204 / 404 |
| `POST /projects/{id}/files` | owner | multipart, any type |
| `DELETE /projects/{id}/files/{fid}` | owner | row first, then bytes |
| `POST /projects/{id}/bookmarks` | owner | url validated, auto-title |
| `PATCH /projects/{id}/bookmarks/{bid}` | owner | url/title, absent = keep |
| `DELETE /projects/{id}/bookmarks/{bid}` | owner | cascades archive file |
| `POST /projects/{id}/bookmarks/{bid}/archive` | owner | 202 job |
| `GET /projects/{id}` | owner (full) / sharee (projection) | everything above |

List endpoints (`GET /projects`) unchanged shape plus `visibility`.
Error shape, RFC3339-UTC-Go-set timestamps, 404 discipline throughout.
`docs/api-notes.md` gets the full contract section.

## 10. Frontend

- **ProjectDetailView** grows sections: header (name, status, description,
  visibility badge-is-toggle), BOM, Notes (owner), Comments, Bookmarks
  (with archive button + job polling + video confirm), Reference
  materials. Sections extract into colocated components
  (`web/src/components/Project*Section.vue`) to keep view files sane;
  viewer mode is the same route driven by the response (no notes section,
  no edit/upload/archive controls, comment box present).
- **ProjectsView**: visibility badge per row (like ItemsView).
- **SharedView**: Projects section (name, owner, status → detail).
- Types + client functions for everything; fetch stays the single API
  boundary. No `v-html`; no empty catches.

## 11. Non-goals

Copying a shared project; comments on items; notifications; bookmark
export/import; scheduled or automatic archiving; archiving video content
itself; per-user grants or share links; changing the BOM link model
(`project_items` stays as-is).

## 12. Testing

- **Backend** (httptest + real DB per convention): owner vs sharee
  projections (private BOM rows masked, notes absent, archive/actions
  denied); comment authorship matrix; gate branch (owner / sharee-of-shared
  / sharee-of-private / unrelated-user 404); bookmark validation +
  auto-title fallback; archive job end-to-end with a fake engine (success
  → file row + link; re-archive replaces; failure leaves bookmark
  untouched); delete-project file cleanup; 404-before-validation
  precedence.
- **Frontend** (vitest, fetch mocked at the client boundary): each section
  component (render + actions), viewer-mode masking, badge toggle, video
  confirm, job polling states, SharedView projects section.
