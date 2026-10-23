# Projects Flesh-Out Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make projects shareable (visibility toggle + masked BOM), give them an owner notes log, a sharee comments thread, a unified reference-materials attachment list, and URL bookmarks with opt-in chromium page archiving.

**Architecture:** Four new child tables under `projects` (`project_notes`, `project_comments`, `project_files`, `project_bookmarks`) plus a `visibility` column, all in one migration. Every project endpoint resolves through one access helper (owner → full projection; sharee-of-shared → masked projection). Materials reuse the item-document upload pipeline (sniffed mime, sharded storage, `/files` gate with a third owner-or-shared-project branch). Bookmark archiving reuses the in-memory jobs system and ArchiveEngine, generalized from "attach to item" to a two-kind completion switch.

**Tech Stack:** Go 1.27 stdlib + chi + sqlc + goose (modernc SQLite); Vue 3 + Pinia + Tailwind v4 SPA embedded via go:embed; vitest + happy-dom.

**Spec:** `docs/superpowers/specs/2026-10-23-projects-flesh-out-design.md` — the plan argues from the spec; read both.

## Global Constraints

- Error shape everywhere: `{"error":{"code":"...","message":"..."}}` via `writeErr`; foreign/private resources → **404, never 403** (no existence leaks); ownership checked BEFORE validation.
- Timestamps: TEXT RFC3339 UTC, set by Go (`time.Now().UTC().Format(time.RFC3339)`), never SQL defaults.
- sqlc workflow: write SQL in `internal/db/queries.sql`, run `make generate`, commit generated code. Query names are an API contract — never rename existing ones; extending a SELECT is allowed. Check generated struct names (nullable columns arrive as `sql.Null*`; repeated column names may gain `_2` suffixes) before writing handler code.
- Body/text caps are byte-count checks with character-count messages (item-notes convention, CJK mismatch documented in api-notes): notes and comments bodies are trimmed, 1–2000.
- Uploads: mime sniffed from bytes (client Content-Type never trusted), `%PDF-` magic forced to `application/pdf`, `sanitizeFilename` for names, BARE single-token extensions to `Storage.Save` (≤8 chars else `bin`), `http.MaxBytesReader` bounds the body (50 MB for project files = `maxDocBytes`).
- `/files` serving: `X-Content-Type-Options: nosniff` always; inline only `image/*`, `application/pdf`, `text/plain` (`isSafeInline`); everything else downloads as attachment.
- Frontend: fetch stays the single API boundary (`web/src/api/client.ts`); NO `v-html` anywhere; no silent empty `catch` blocks; `vue-tsc -b` (not `--noEmit`); dist-commit rule — after changing `web/`, run `make web` and commit `internal/webfs/dist`.
- Testing: stdlib `go test` only (no testify), `httptest.NewServer(s.Router())` against real temp SQLite via `newTestServer(t, nil)` (`internal/api/testutil_test.go`); frontend vitest with `fetch` mocked at the client boundary; TDD — failing test first, watch it fail, implement, watch it pass.
- `make check` (go vet+tests, web tests+typecheck, SPA build) must stay green at every commit.

## Review Focus

The five input/failure classes the spec implies but tasks' happy paths don't fully exercise — each pinned in the owning task:

1. **Sharee hitting an owner-only endpoint on a SHARED project** (note add/delete, file upload/delete, bookmark CRUD, archive, PATCH) must 404 — not 403, not partial success. Pinned in Tasks 3–7 tests (`owner-only` subtests).
2. **BOM masking key-absence**: sharee response rows must lack `item_id`/`item_qty`/`notes` keys entirely (not zero values), and `notes_log` must be absent from the sharee project detail. Pinned in Task 2 + 3 tests.
3. **Gate bypass attempt**: another user requesting a project file key (or thumb key) whose project is PRIVATE → 404; same key after the project goes shared → 200. Pinned in Task 5.
4. **Comment deletion matrix**: author deletes own ✓ 204; project owner deletes another's ✓ 204; a third viewer deletes someone else's → 404. Pinned in Task 4.
5. **Archive failure/re-archive cleanliness**: a failed archive leaves the bookmark with no `archived_file`; re-archive replaces the row AND deletes the old bytes (old key no longer on disk). Pinned in Task 7.

---

### Task 1: Visibility on projects (migration + create/patch/list)

**Files:**
- Create: `internal/migrations/00010_projects_flesh_out.sql`
- Modify: `internal/db/queries.sql` (projects region, ~line 295)
- Modify: `internal/api/projects_handlers.go` (`projectJSON`, `projectReq`, `handleCreateProject`, `handleUpdateProject`)
- Modify: `docs/api-notes.md` (projects section)
- Test: `internal/api/projects_handlers_test.go` (extend; create if patterns live in `projects_test`-style files — follow existing naming)

**Interfaces:**
- Consumes: `requireOwnedProject`, `decodeJSON`, `writeErr` (existing in `projects_handlers.go`).
- Produces: `dbpkg.Project` gains `Visibility string`; `CreateProjectParams`/`UpdateProjectParams` gain `Visibility string`; `projectJSON` output includes `"visibility"`; `projectReq` gains `Visibility *string json:"visibility"`. Later tasks rely on `p.Visibility` being present.

- [ ] **Step 1: Write the migration**

```sql
-- internal/migrations/00010_projects_flesh_out.sql

-- +goose Up
ALTER TABLE projects ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private'
    CHECK (visibility IN ('private','shared'));

CREATE TABLE project_notes (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_project_notes_project ON project_notes(project_id);

CREATE TABLE project_comments (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_project_comments_project ON project_comments(project_id);

CREATE TABLE project_files (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    thumb_key TEXT,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_project_files_project ON project_files(project_id);
CREATE INDEX idx_project_files_key ON project_files(storage_key);
CREATE INDEX idx_project_files_thumb_key ON project_files(thumb_key);

CREATE TABLE project_bookmarks (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    title TEXT NOT NULL,
    archived_file_id INTEGER REFERENCES project_files(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_project_bookmarks_project ON project_bookmarks(project_id);

-- +goose Down
DROP TABLE project_bookmarks;
DROP TABLE project_files;
DROP TABLE project_comments;
DROP TABLE project_notes;
ALTER TABLE projects DROP COLUMN visibility;
```

(All tables now, endpoints in later tasks — migrations are sequential state; unused tables are harmless.)

- [ ] **Step 2: Write the failing tests**

In `internal/api/projects_handlers_test.go`, follow the existing helper style (`registerUser`, `doJSON`, `decodeBody`):

```go
func TestProjectVisibility(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "projvis@test.dev", "longpassword1")

	// create: defaults to private
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "P1"}, cookie)
	var created struct {
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &created)
	if created.Visibility != "private" {
		t.Fatalf("default visibility = %q, want private", created.Visibility)
	}

	// create: explicit shared
	resp = doJSON(t, ts, "POST", "/api/v1/projects",
		map[string]any{"name": "P2", "visibility": "shared"}, cookie)
	var created2 struct {
		ID         int64  `json:"id"`
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &created2)
	if created2.Visibility != "shared" {
		t.Fatalf("visibility = %q, want shared", created2.Visibility)
	}

	// PATCH: flip + invalid value + absent-keeps
	resp = doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(created2.ID),
		map[string]any{"visibility": "private"}, cookie)
	var patched struct {
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &patched)
	if patched.Visibility != "private" {
		t.Fatalf("patched visibility = %q, want private", patched.Visibility)
	}
	resp = doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(created2.ID),
		map[string]any{"visibility": "public"}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid visibility status = %d, want 400", resp.StatusCode)
	}
	resp = doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(created2.ID),
		map[string]any{"status": "active"}, cookie) // visibility absent → stays
	var after struct {
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &after)
	if after.Visibility != "private" {
		t.Fatalf("absent visibility changed: %q", after.Visibility)
	}

	// list carries visibility
	var list struct {
		Data []struct {
			Visibility string `json:"visibility"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects", nil, cookie), &list)
	if len(list.Data) != 2 {
		t.Fatalf("list len = %d, want 2", len(list.Data))
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/api/ -run TestProjectVisibility -v`
Expected: FAIL — `visibility` key missing/empty (projectJSON has no field; POST ignores the key).

- [ ] **Step 4: Queries + implement**

In `internal/db/queries.sql`, extend the two project writes (extending, not renaming):

```sql
-- name: CreateProject :one
INSERT INTO projects (user_id, name, status, notes, visibility, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateProject :exec
UPDATE projects SET name = ?, status = ?, notes = ?, visibility = ?, updated_at = ?
WHERE id = ? AND user_id = ?;
```

Then `make generate`. In `internal/api/projects_handlers.go`:

```go
func projectJSON(p dbpkg.Project) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "status": p.Status, "notes": p.Notes,
		"visibility": p.Visibility,
		"created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

type projectReq struct {
	Name       string  `json:"name"`
	Status     *string `json:"status"`
	Notes      *string `json:"notes"`
	Visibility *string `json:"visibility"`
}
```

In `handleCreateProject`, after the status block (mirroring items' `handleCreateItem` visibility handling at `items_handlers.go:195`):

```go
	visibility := "private"
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		visibility = *req.Visibility
	}
```

— pass `Visibility: visibility` into `CreateProjectParams`. In `handleUpdateProject`, after the notes merge:

```go
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		p.Visibility = *req.Visibility
	}
```

— pass `Visibility: p.Visibility` into `UpdateProjectParams` (add to both call sites of `UpdateProject` — grep for others).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go vet ./... && go test ./internal/api/ -run 'TestProject' -v`
Expected: PASS (existing project tests still green — they ignore the new field).

- [ ] **Step 6: api-notes + commit**

Append to the projects section of `docs/api-notes.md`: `visibility` on create (default `private`)/PATCH (absent keeps)/all project JSON; note that viewer semantics land in the next endpoints.

```bash
git add internal/migrations/00010_projects_flesh_out.sql internal/db/queries.sql internal/db/queries.sql.go internal/api/projects_handlers.go internal/api/projects_handlers_test.go docs/api-notes.md
git commit -m "feat: project visibility flag + projects-flesh-out schema"
```

(If `make generate` touched more generated files, add them.)

---

### Task 2: Sharee read access, BOM projection, shared-projects list

**Files:**
- Modify: `internal/db/queries.sql` (extend `ListProjectItems`; add `GetProjectAnyOwner`, `ListSharedProjectsFromOthers`)
- Modify: `internal/api/projects_handlers.go` (new `requireViewableProject`, rework `handleGetProject`)
- Modify: `internal/api/shared_handlers.go` (new `handleListSharedProjects`)
- Modify: `internal/api/server.go` (route `GET /shared/projects`)
- Modify: `docs/api-notes.md`
- Test: `internal/api/projects_handlers_test.go`

**Interfaces:**
- Consumes: `dbpkg.Project.Visibility` (Task 1).
- Produces:
  - `func (s *Server) requireViewableProject(w http.ResponseWriter, r *http.Request, u dbpkg.User) (dbpkg.Project, bool)` — owner OR sharee-of-shared; writes 404 otherwise. Later tasks (3–7) authorize through this.
  - `GET /projects/{id}` response shape: `{"project": {...}, "owner_view": bool, "items": [...]}` — owner rows: `{item_id, item_name, item_qty, item_unit, qty_used, notes, added_at, shared}`; sharee rows: `{item_name, qty_used, shared}` (keys `item_id`/`item_qty`/`item_unit`/`notes`/`added_at` ABSENT).
  - `GET /api/v1/shared/projects` → `listEnvelope{data: [{id, name, status, owner_name, updated_at}], total}`.

- [ ] **Step 1: Write the failing tests**

```go
func TestProjectShareeAccessAndBOMMasking(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pom@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "psh@test.dev", "longpassword1")

	// owner: two items (one private, one shared), a project with both, shared
	itPriv := mkItem(t, ts, owner, "Priv part")
	itSh := mkItemS(t, ts, owner, "Shared part") // helper sets visibility shared
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Bench PSU", "visibility": "shared"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/items", map[string]any{"item_id": itPriv, "qty_used": 2}, owner)
	doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/items", map[string]any{"item_id": itSh, "qty_used": 1, "notes": "secret link note"}, owner)

	// sharee GET: 200, masked rows, owner_view false
	resp = doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(proj.ID), nil, sharee)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sharee GET = %d", resp.StatusCode)
	}
	var detail map[string]any
	decodeBody(t, resp, &detail)
	if detail["owner_view"] != false {
		t.Fatalf("owner_view = %v", detail["owner_view"])
	}
	rows := detail["items"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		m := r.(map[string]any)
		for _, banned := range []string{"item_id", "item_qty", "item_unit", "notes", "added_at"} {
			if _, has := m[banned]; has {
				t.Fatalf("sharee row leaks %q: %v", banned, m)
			}
		}
		if m["item_name"] == "" {
			t.Fatalf("row missing item_name: %v", m)
		}
	}

	// owner GET: full rows with shared flags, owner_view true
	resp = doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(proj.ID), nil, owner)
	decodeBody(t, resp, &detail)
	if detail["owner_view"] != true {
		t.Fatalf("owner owner_view = %v", detail["owner_view"])
	}
	for _, r := range detail["items"].([]any) {
		m := r.(map[string]any)
		if _, has := m["item_id"]; !has {
			t.Fatalf("owner row missing item_id: %v", m)
		}
	}
}

func TestProjectShareePrivate404(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pom2@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "psh2@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Secret"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	if r := doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(proj.ID), nil, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee GET private = %d, want 404", r.StatusCode)
	}
}

func TestListSharedProjects(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pom3@test.dev", "longpassword1")
	other := registerUser(t, ts, "psh3@test.dev", "longpassword1")
	doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Public", "visibility": "shared"}, owner)
	doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Hidden"}, owner)
	doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Mine shared", "visibility": "shared"}, other)

	var list struct {
		Data []struct {
			Name      string `json:"name"`
			OwnerName string `json:"owner_name"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/shared/projects", nil, other), &list)
	if len(list.Data) != 1 || list.Data[0].Name != "Public" || list.Data[0].OwnerName == "" {
		t.Fatalf("shared projects = %+v, want only Public with owner name", list.Data)
	}
	// owner's own view: also only others' projects
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/shared/projects", nil, owner), &list)
	if len(list.Data) != 0 {
		t.Fatalf("owner sees own project in shared list: %+v", list.Data)
	}
}
```

(`mkItem`/`mkItemS` — check existing item-creation helpers in the test package first and reuse them; if none fit, write tiny helpers next to the test: POST `/api/v1/items` with optional `visibility`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run 'TestProjectSharee|TestListSharedProjects' -v`
Expected: FAIL — sharee GET private project already 404s (accidentally right), shared GET also 404s; `/shared/projects` 404s.

- [ ] **Step 3: Queries**

In `internal/db/queries.sql`:

```sql
-- name: ListProjectItems :many
SELECT pi.project_id, pi.item_id, pi.qty_used, pi.notes, pi.added_at,
       i.name AS item_name, i.qty AS item_qty, i.unit AS item_unit,
       i.visibility AS item_visibility
FROM project_items pi JOIN items i ON i.id = pi.item_id
WHERE pi.project_id = ?;

-- name: GetProjectAnyOwner :one
SELECT * FROM projects WHERE id = ?;

-- name: ListSharedProjectsFromOthers :many
SELECT p.id, p.name, p.status, p.updated_at, u.display_name AS owner_name
FROM projects p JOIN users u ON u.id = p.user_id
WHERE p.user_id != ? AND p.visibility = 'shared'
ORDER BY p.updated_at DESC;
```

`make generate`. Fix any callers broken by the new `ListProjectItemsRow` field (additive; `handlePatchProjectItem` uses `cur.Notes`/`cur.AddedAt` — unaffected).

- [ ] **Step 4: Implement access helper + reworked GET**

In `internal/api/projects_handlers.go`:

```go
// requireViewableProject writes 404 unless the project is the user's own or
// another user's shared project. isOwner tells handlers which projection to
// build; mutating routes use requireOwnedProject instead.
func (s *Server) requireViewableProject(w http.ResponseWriter, r *http.Request, u dbpkg.User) (p dbpkg.Project, isOwner bool, ok bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid project id")
		return dbpkg.Project{}, false, false
	}
	p, err = s.q.GetProjectAnyOwner(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "project not found")
		return dbpkg.Project{}, false, false
	}
	if p.UserID != u.ID && p.Visibility != "shared" {
		writeErr(w, http.StatusNotFound, "not_found", "project not found")
		return dbpkg.Project{}, false, false
	}
	return p, p.UserID == u.ID, true
}
```

Rework `handleGetProject`:

```go
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, isOwner, ok := s.requireViewableProject(w, r, u)
	if !ok {
		return
	}
	items, err := s.q.ListProjectItems(r.Context(), p.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	itemList := make([]map[string]any, 0, len(items))
	for _, pi := range items {
		var qtyUsed any
		if pi.QtyUsed.Valid {
			qtyUsed = pi.QtyUsed.Float64
		}
		if isOwner {
			itemList = append(itemList, map[string]any{
				"item_id": pi.ItemID, "item_name": pi.ItemName,
				"item_qty": pi.ItemQty, "item_unit": pi.ItemUnit,
				"qty_used": qtyUsed, "notes": pi.Notes, "added_at": pi.AddedAt,
				"shared": pi.ItemVisibility == "shared",
			})
		} else {
			// sharee BOM: name + qty used + shared flag only (spec §2)
			itemList = append(itemList, map[string]any{
				"item_name": pi.ItemName, "qty_used": qtyUsed,
				"shared": pi.ItemVisibility == "shared",
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": projectJSON(p), "owner_view": isOwner, "items": itemList,
	})
}
```

In `internal/api/shared_handlers.go` add `handleListSharedProjects` (mirror `handleListShared`'s envelope):

```go
func (s *Server) handleListSharedProjects(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	rows, err := s.q.ListSharedProjectsFromOthers(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, p := range rows {
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "status": p.Status,
			"owner_name": p.OwnerName, "updated_at": p.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}
```

In `internal/api/server.go`, next to `r.Get("/shared", s.handleListShared)`:

```go
			r.Get("/shared/projects", s.handleListSharedProjects)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go vet ./... && go test ./internal/api/ -run 'TestProject|TestListShared' -v`
Expected: PASS, including all pre-existing project tests.

- [ ] **Step 6: api-notes + commit**

Document in `docs/api-notes.md`: sharee GET semantics (owner_view, masked BOM key list), `/shared/projects`, and that mutating project routes stay owner-only (404 for sharees even on shared projects).

```bash
git add internal/db/queries.sql internal/db/queries.sql.go internal/api/projects_handlers.go internal/api/shared_handlers.go internal/api/server.go internal/api/projects_handlers_test.go docs/api-notes.md
git commit -m "feat: sharee project access with masked BOM + shared projects list"
```

---

### Task 3: Project notes (owner-only log)

**Files:**
- Modify: `internal/db/queries.sql` (notes queries)
- Create: `internal/api/project_notes_handlers.go`
- Modify: `internal/api/projects_handlers.go` (`handleGetProject` owner projection)
- Modify: `internal/api/server.go` (routes)
- Modify: `docs/api-notes.md`
- Test: `internal/api/project_notes_test.go`

**Interfaces:**
- Consumes: `requireViewableProject` (Task 2), `noteReq` (`items_handlers.go:779` — `Body string json:"body"`; reuse it).
- Produces: `GET /projects/{id}` owner projection gains `"notes_log": [{id, body, created_at}]` (newest first, `ListItemNotes` order); sharee response OMITS the key. Routes: `POST /api/v1/projects/{id}/notes` → 201 `{id, body, created_at}`; `DELETE /api/v1/projects/{id}/notes/{noteId}` → 204/404.

- [ ] **Step 1: Write the failing tests**

```go
func TestProjectNotes(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pnotes@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "pnotes2@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "N", "visibility": "shared"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// add: 201, trim + cap
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/notes", map[string]any{"body": "  first note  "}, owner); r.StatusCode != http.StatusCreated {
		t.Fatalf("add note = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/notes", map[string]any{"body": ""}, owner); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty note = %d, want 400", r.StatusCode)
	}
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/notes", map[string]any{"body": strings.Repeat("x", 2001)}, owner); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("long note = %d, want 400", r.StatusCode)
	}

	// owner detail carries notes_log (trimmed body)
	var detail map[string]any
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, owner), &detail)
	log0 := detail["notes_log"].([]any)[0].(map[string]any)
	if log0["body"] != "first note" {
		t.Fatalf("body = %q, want trimmed", log0["body"])
	}

	// sharee detail OMITS the key entirely
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, sharee), &detail)
	if _, has := detail["notes_log"]; has {
		t.Fatal("sharee sees notes_log")
	}

	// sharee cannot add/delete notes even on a shared project (owner-only → 404)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/notes", map[string]any{"body": "x"}, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee add note = %d, want 404", r.StatusCode)
	}

	// delete: 204 then 404 on repeat (execrows)
	id := int64(log0["id"].(float64))
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/notes/"+fmtInt64(id), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete note = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/notes/"+fmtInt64(id), nil, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("double delete = %d, want 404", r.StatusCode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run TestProjectNotes -v`
Expected: FAIL — routes don't exist (404/405 on POST), no `notes_log` key.

- [ ] **Step 3: Queries + handlers**

`internal/db/queries.sql`:

```sql
-- name: AddProjectNote :one
INSERT INTO project_notes (project_id, body, created_at) VALUES (?, ?, ?)
RETURNING id, project_id, body, created_at;

-- name: ListProjectNotes :many
SELECT id, project_id, body, created_at FROM project_notes
WHERE project_id = ? ORDER BY created_at DESC, id DESC;

-- name: DeleteProjectNote :execrows
DELETE FROM project_notes WHERE id = ? AND project_id = ?;
```

`make generate`, then `internal/api/project_notes_handlers.go` — clone the item-notes handlers (`items_handlers.go:783-839`) onto `requireOwnedProject`:

```go
func (s *Server) handleAddProjectNote(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	var req noteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" || len(body) > 2000 {
		writeErr(w, http.StatusBadRequest, "validation_error", "note must be 1-2000 characters")
		return
	}
	note, err := s.q.AddProjectNote(r.Context(), dbpkg.AddProjectNoteParams{
		ProjectID: p.ID, Body: body,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": note.ID, "body": note.Body, "created_at": note.CreatedAt,
	})
}

func (s *Server) handleDeleteProjectNote(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	noteID, err := strconv.ParseInt(chi.URLParam(r, "noteId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid note id")
		return
	}
	rows, err := s.q.DeleteProjectNote(r.Context(), dbpkg.DeleteProjectNoteParams{ID: noteID, ProjectID: p.ID})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if rows == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "note not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Routes in `server.go` next to the project block:

```go
			r.Post("/projects/{id}/notes", s.handleAddProjectNote)
			r.Delete("/projects/{id}/notes/{noteId}", s.handleDeleteProjectNote)
```

In `handleGetProject` (Task 2's rework), owner branch only:

```go
	if isOwner {
		notes, err := s.q.ListProjectNotes(r.Context(), p.ID)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		noteList := make([]map[string]any, 0, len(notes))
		for _, n := range notes {
			noteList = append(noteList, map[string]any{"id": n.ID, "body": n.Body, "created_at": n.CreatedAt})
		}
		body["notes_log"] = noteList // body = the response map being assembled
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go vet ./... && go test ./internal/api/ -run 'TestProject' -v`
Expected: PASS.

- [ ] **Step 5: api-notes + commit**

api-notes: notes endpoints, 1–2000 trimmed byte-cap semantics, owner-only, `notes_log` absent (not empty) for sharees.

```bash
git add internal/db/queries.sql internal/db/queries.sql.go internal/api/project_notes_handlers.go internal/api/projects_handlers.go internal/api/server.go internal/api/project_notes_test.go docs/api-notes.md
git commit -m "feat: owner-only project notes log"
```

---

### Task 4: Project comments (sharee-writable thread)

**Files:**
- Modify: `internal/db/queries.sql` (comments queries)
- Create: `internal/api/project_comments_handlers.go`
- Modify: `internal/api/projects_handlers.go` (`handleGetProject` both projections)
- Modify: `internal/api/server.go` (routes)
- Modify: `docs/api-notes.md`
- Test: `internal/api/project_comments_test.go`

**Interfaces:**
- Consumes: `requireViewableProject` (Task 2), `noteReq`.
- Produces: `GET /projects/{id}` (owner AND sharee) gains `"comments": [{id, author, mine, body, created_at}]` oldest-first (`ORDER BY created_at, id`). Routes: `POST /api/v1/projects/{id}/comments` → 201 `{id, author, mine, body, created_at}` (owner always; any user when shared); `DELETE /api/v1/projects/{id}/comments/{cid}` → 204 (author or project owner) / 404.

- [ ] **Step 1: Write the failing tests**

```go
func TestProjectComments(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pc1@test.dev", "longpassword1")
	alice := registerUser(t, ts, "pc2@test.dev", "longpassword1")
	bob := registerUser(t, ts, "pc3@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "C", "visibility": "shared"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// alice + owner comment; bob reads
	mk := func(user, body string) int64 {
		r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/comments", map[string]any{"body": body}, user)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("comment by = %d", r.StatusCode)
		}
		var c struct{ ID int64 `json:"id"` }
		decodeBody(t, r, &c)
		return c.ID
	}
	ca := mk(alice, "nice build")
	mk(owner, "thanks")

	// thread visible to sharee, oldest first, author display names, mine flag
	var detail struct {
		Comments []struct {
			Author string `json:"author"`
			Mine   bool   `json:"mine"`
			Body   string `json:"body"`
		} `json:"comments"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, bob), &detail)
	if len(detail.Comments) != 2 || detail.Comments[0].Body != "nice build" {
		t.Fatalf("comments = %+v", detail.Comments)
	}

	// deletion matrix: bob cannot delete alice's; alice deletes own; owner deletes any
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/comments/"+fmtInt64(ca), nil, bob); r.StatusCode != http.StatusNotFound {
		t.Fatalf("bob delete = %d, want 404", r.StatusCode)
	}
	ca2 := mk(alice, "again")
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/comments/"+fmtInt64(ca2), nil, alice); r.StatusCode != http.StatusNoContent {
		t.Fatalf("author delete = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/comments/"+fmtInt64(ca), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("owner delete = %d", r.StatusCode)
	}

	// private project: sharee cannot even POST (404 via requireViewableProject)
	resp = doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Solo"}, owner)
	decodeBody(t, resp, &proj)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/comments", map[string]any{"body": "hi"}, alice); r.StatusCode != http.StatusNotFound {
		t.Fatalf("private comment = %d, want 404", r.StatusCode)
	}
	// owner CAN comment on own private project (solo thread)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/comments", map[string]any{"body": "note to self"}, owner); r.StatusCode != http.StatusCreated {
		t.Fatalf("owner private comment = %d", r.StatusCode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run TestProjectComments -v`
Expected: FAIL — no routes.

- [ ] **Step 3: Queries + handlers**

`internal/db/queries.sql`:

```sql
-- name: AddProjectComment :one
INSERT INTO project_comments (project_id, user_id, body, created_at) VALUES (?, ?, ?, ?)
RETURNING id, project_id, user_id, body, created_at;

-- name: ListProjectComments :many
SELECT c.id, c.user_id, c.body, c.created_at, u.display_name AS author_name
FROM project_comments c JOIN users u ON u.id = c.user_id
WHERE c.project_id = ? ORDER BY c.created_at, c.id;

-- name: GetProjectComment :one
SELECT id, project_id, user_id FROM project_comments WHERE id = ?;

-- name: DeleteProjectComment :execrows
DELETE FROM project_comments WHERE id = ?;
```

`make generate`. `internal/api/project_comments_handlers.go`:

```go
func (s *Server) handleAddProjectComment(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	// requireViewableProject: owner passes always; others only when shared
	p, _, ok := s.requireViewableProject(w, r, u)
	if !ok {
		return
	}
	var req noteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" || len(body) > 2000 {
		writeErr(w, http.StatusBadRequest, "validation_error", "comment must be 1-2000 characters")
		return
	}
	c, err := s.q.AddProjectComment(r.Context(), dbpkg.AddProjectCommentParams{
		ProjectID: p.ID, UserID: u.ID, Body: body,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": c.ID, "author": u.DisplayName, "mine": true,
		"body": c.Body, "created_at": c.CreatedAt,
	})
}

func (s *Server) handleDeleteProjectComment(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, _, ok := s.requireViewableProject(w, r, u)
	if !ok {
		return
	}
	cid, err := strconv.ParseInt(chi.URLParam(r, "cid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid comment id")
		return
	}
	c, err := s.q.GetProjectComment(r.Context(), cid)
	if err != nil || c.ProjectID != p.ID {
		writeErr(w, http.StatusNotFound, "not_found", "comment not found")
		return
	}
	if c.UserID != u.ID && p.UserID != u.ID {
		writeErr(w, http.StatusNotFound, "not_found", "comment not found") // 404, not 403
		return
	}
	rows, err := s.q.DeleteProjectComment(r.Context(), cid)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if rows == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "comment not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Routes:

```go
			r.Post("/projects/{id}/comments", s.handleAddProjectComment)
			r.Delete("/projects/{id}/comments/{cid}", s.handleDeleteProjectComment)
```

In `handleGetProject` (BOTH owner and sharee projections — comments are public):

```go
	comments, err := s.q.ListProjectComments(r.Context(), p.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	comList := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		comList = append(comList, map[string]any{
			"id": c.ID, "author": c.AuthorName, "mine": c.UserID == u.ID,
			"body": c.Body, "created_at": c.CreatedAt,
		})
	}
	body["comments"] = comList
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go vet ./... && go test ./internal/api/ -run 'TestProject' -v`
Expected: PASS.

- [ ] **Step 5: api-notes + commit**

api-notes: comment routes, author matrix (author-or-owner delete, 404 otherwise), oldest-first order, `mine` flag, display-name authors, solo-thread-on-private allowed for owner.

```bash
git add internal/db/queries.sql internal/db/queries.sql.go internal/api/project_comments_handlers.go internal/api/projects_handlers.go internal/api/server.go internal/api/project_comments_test.go docs/api-notes.md
git commit -m "feat: project comments thread with author-or-owner deletes"
```

---

### Task 5: Reference materials (project files + gate + delete cleanup)

**Files:**
- Modify: `internal/db/queries.sql` (files queries)
- Create: `internal/api/project_files_handlers.go`
- Modify: `internal/api/files_handlers.go` (`handleFileGet` branch; `handleDeleteProject` cleanup lives in projects_handlers)
- Modify: `internal/api/projects_handlers.go` (`handleGetProject` projections + `handleDeleteProject` cleanup)
- Modify: `internal/api/server.go` (routes)
- Modify: `docs/api-notes.md`
- Test: `internal/api/project_files_test.go`

**Interfaces:**
- Consumes: `requireViewableProject`/`requireOwnedProject`; `thumbs.Generate`; `extForMime`, `sanitizeFilename`, `maxDocBytes`, `isSafeInline` (all in `files_handlers.go`).
- Produces: `GET /projects/{id}` (owner AND sharee) gains `"files": [{id, storage_key, thumb_key, filename, mime_type, size_bytes, created_at}]`. Routes: `POST /api/v1/projects/{id}/files` (multipart `file`, owner) → 201 row; `DELETE /api/v1/projects/{id}/files/{fid}` (owner) → 204/404. `/files/{key}` serves project files owner-or-shared-project.

- [ ] **Step 1: Write the failing tests**

Use the multipart helpers from `files_handlers_test.go` / `location_photos_test.go` (look for `newMultipart`/`doUpload` style helpers and reuse; otherwise build `multipart.Writer` inline as those tests do).

```go
func TestProjectFilesLifecycle(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pf1@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "pf2@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "F", "visibility": "shared"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// upload an image → thumb_key set; upload a text file → null thumb
	imgRow := uploadProjectFile(t, ts, owner, pid, "diagram.png", pngBytes(t))
	if imgRow["thumb_key"] == nil {
		t.Fatal("image upload missing thumb_key")
	}
	txtRow := uploadProjectFile(t, ts, owner, pid, "notes.txt", []byte("hello"))
	if txtRow["thumb_key"] != nil {
		t.Fatal("text upload has thumb_key")
	}
	if r := uploadRawProjectFile(t, ts, sharee, pid, "x.txt", []byte("no")); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee upload = %d, want 404", r.StatusCode)
	}

	// detail carries files for BOTH views
	var detail map[string]any
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, sharee), &detail)
	if files := detail["files"].([]any); len(files) != 2 {
		t.Fatalf("sharee files = %d, want 2", len(files))
	}

	// gate: sharee fetches image bytes (shared project) → 200; private → 404
	key := imgRow["storage_key"].(string)
	if r := doGet(t, ts, "/files/"+key, sharee); r.StatusCode != http.StatusOK {
		t.Fatalf("sharee file GET = %d", r.StatusCode)
	}
	doJSON(t, ts, "PATCH", "/api/v1/projects/"+pid, map[string]any{"visibility": "private"}, owner)
	if r := doGet(t, ts, "/files/"+key, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("private-project file GET = %d, want 404", r.StatusCode)
	}
	doJSON(t, ts, "PATCH", "/api/v1/projects/"+pid, map[string]any{"visibility": "shared"}, owner)

	// delete removes row + bytes
	fid := int64(imgRow["id"].(float64))
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/files/"+fmtInt64(fid), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete file = %d", r.StatusCode)
	}
	if r := doGet(t, ts, "/files/"+key, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted file still served: %d", r.StatusCode)
	}
}

func TestDeleteProjectCleansFiles(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pf3@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Doomed"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)
	uploadProjectFile(t, ts, owner, pid, "a.png", pngBytes(t))
	uploadProjectFile(t, ts, owner, pid, "b.txt", []byte("bye"))

	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid, nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete project = %d", r.StatusCode)
	}
	// storage dir must be empty: walk the test uploads dir the same way
	// location_photos_test.go's disk assertions do (adjust the accessor to
	// whatever newTestServer exposes — e.g. ts.Config... or the Server's st
	// root; reuse the existing helper if one exists).
	n := countFilesUnder(t, uploadsDir(t))
	if n != 0 {
		t.Fatalf("orphan files after project delete: %d", n)
	}
}
```

(`countFilesUnder`/`uploadsDir` — reuse the helpers `location_photos_test.go` already uses for its disk-cleanliness assertions; if they are inline there, promote them to shared helpers in the same test package rather than duplicating.)

(`uploadProjectFile` wraps the multipart POST and decodes the 201 row; `pngBytes` — reuse whatever tiny-PNG helper exists in `files_handlers_test.go`; if none, copy the 1×1 PNG bytes from there.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run 'TestProjectFiles|TestDeleteProjectCleansFiles' -v`
Expected: FAIL — routes/keys missing; sharee gate fetch 404s today (right for the wrong reason).

- [ ] **Step 3: Queries**

```sql
-- name: AddProjectFile :one
INSERT INTO project_files (project_id, storage_key, thumb_key, filename, mime_type, size_bytes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, project_id, storage_key, thumb_key, filename, mime_type, size_bytes, created_at;

-- name: ListProjectFiles :many
SELECT * FROM project_files WHERE project_id = ? ORDER BY created_at, id;

-- name: DeleteProjectFile :execrows
DELETE FROM project_files WHERE id = ? AND project_id = ?;

-- name: GetProjectFile :one
SELECT * FROM project_files WHERE id = ? AND project_id = ?;

-- name: GetProjectFileByKey :one
SELECT p.user_id, p.visibility
FROM project_files f JOIN projects p ON p.id = f.project_id
WHERE f.storage_key = ? OR f.thumb_key = ?;

-- name: ListAllProjectFileKeys :many
SELECT storage_key, thumb_key FROM project_files WHERE project_id = ?;
```

`make generate` (expect `GetProjectFileByKey` to return a two-column row struct `{UserID int64; Visibility string}` — it selects two columns, unlike the single-column `GetLocationPhotoByKey`; params arrive as two `sql.NullString`s; and check for `_2` suffixes).

- [ ] **Step 4: Upload/delete handlers**

`internal/api/project_files_handlers.go` — hybrid of `handleUploadDocument` (`files_handlers.go:315`, streaming + sniff) and `handleUploadPhoto` (`:59`, buffered image + thumb):

```go
func (s *Server) handleUploadProjectFile(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_file", "file form field is required")
		return
	}
	defer f.Close()

	filename := sanitizeFilename(header.Filename)
	ext := strings.TrimPrefix(path.Ext(filename), ".")
	if ext == "" || len(ext) > 8 {
		ext = "bin"
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad upload body")
		return
	}
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "empty_file", "file is empty")
		return
	}
	head = head[:n]
	ct := http.DetectContentType(head)
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		ct = "application/pdf"
	}

	// images: buffer (bounded by the already-tripped MaxBytesReader) so the
	// thumb generator can re-read; everything else streams (doc pattern).
	var thumbKey sql.NullString
	var reader io.Reader = io.MultiReader(bytes.NewReader(head), f)
	if strings.HasPrefix(ct, "image/") {
		rest, err := io.ReadAll(f)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_upload", "bad upload body")
			return
		}
		buf := append(head, rest...)
		if tb, text, err := thumbs.Generate(bytes.NewReader(buf)); err != nil {
			slog.Warn("thumb generation failed", "err", err)
		} else if tkey, _, err := s.st.Save(bytes.NewReader(tb), text); err != nil {
			slog.Warn("thumb save failed", "err", err)
		} else {
			thumbKey = sql.NullString{String: tkey, Valid: true}
		}
		reader = bytes.NewReader(buf)
	}

	key, size, err := s.st.Save(reader, ext)
	if err != nil {
		s.st.Delete(thumbKey.String) // no-op when empty
		s.internalErr(w, err)
		return
	}
	row, err := s.q.AddProjectFile(r.Context(), dbpkg.AddProjectFileParams{
		ProjectID: p.ID, StorageKey: key, ThumbKey: thumbKey, Filename: filename,
		MimeType: ct, SizeBytes: size,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.st.Delete(key)
		s.st.Delete(thumbKey.String)
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectFileJSON(row))
}

func projectFileJSON(f dbpkg.ProjectFile) map[string]any {
	return map[string]any{
		"id": f.ID, "storage_key": f.StorageKey, "thumb_key": thumbJSON(f.ThumbKey),
		"filename": f.FileName, "mime_type": f.MimeType, "size_bytes": f.SizeBytes,
		"created_at": f.CreatedAt,
	}
}
```

(Check the generated field name for `filename` — could be `Filename`; use whatever sqlc emits. `thumbJSON` is the existing nullable-thumb helper in `respond.go`.)

```go
func (s *Server) handleDeleteProjectFile(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	fid, err := strconv.ParseInt(chi.URLParam(r, "fid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid file id")
		return
	}
	row, err := s.q.GetProjectFile(r.Context(), dbpkg.GetProjectFileParams{ID: fid, ProjectID: p.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if _, err := s.q.DeleteProjectFile(r.Context(), dbpkg.DeleteProjectFileParams{ID: fid, ProjectID: p.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	s.deleteFileBytes(row.StorageKey)
	if row.ThumbKey.Valid {
		s.deleteFileBytes(row.ThumbKey.String)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteFileBytes warns (never 500s) — bytes gone missing is an orphan, not a failure.
func (s *Server) deleteFileBytes(key string) {
	if err := s.st.Delete(key); err != nil {
		slog.Warn("file delete failed", "key", key, "err", err)
	}
}
```

Routes:

```go
			r.Post("/projects/{id}/files", s.handleUploadProjectFile)
			r.Delete("/projects/{id}/files/{fid}", s.handleDeleteProjectFile)
```

Gate branch in `handleFileGet` (`files_handlers.go`, after the document lookup's `else if`, before the final `else`):

```go
	} else if row, err := s.q.GetProjectFileByKey(r.Context(), dbpkg.GetProjectFileByKeyParams{
		StorageKey: sqlNullString(key), ThumbKey: sqlNullString(key),
	}); err == nil {
		ownerID, visibility = row.UserID, row.Visibility
	} else {
```

(`sqlNullString` — existing helper; if named differently, use it. Content type: no stored-mime pass-through here — leave `ct = ""` so serve-time sniffing applies, exactly like item photos.)

`handleGetProject` (both projections — materials are public): fetch `ListProjectFiles`, `body["files"] = […]` via `projectFileJSON`.

`handleDeleteProject` cleanup — before `s.q.DeleteProject`, gather; after a successful delete, remove bytes:

```go
	keys, err := s.q.ListAllProjectFileKeys(r.Context(), p.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	// ... existing DeleteProject ...
	for _, k := range keys {
		s.deleteFileBytes(k.StorageKey)
		if k.ThumbKey.Valid {
			s.deleteFileBytes(k.ThumbKey.String)
		}
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go vet ./... && go test ./internal/api/ -run 'TestProject|TestDeleteProject|TestFileGet' -v`
Expected: PASS.

- [ ] **Step 6: api-notes + commit**

api-notes: files endpoints (owner-only mutations, any mime, 50MB, sniffed, images thumbnailed), `files` key on both projections, gate semantics (owner-or-shared-project, sniffs at serve time), delete-project cleans bytes.

```bash
git add internal/db/queries.sql internal/db/queries.sql.go internal/api/project_files_handlers.go internal/api/files_handlers.go internal/api/projects_handlers.go internal/api/server.go internal/api/project_files_test.go docs/api-notes.md
git commit -m "feat: project reference materials with owner-or-shared gate"
```

---

### Task 6: Bookmarks CRUD + auto-title

**Files:**
- Modify: `internal/fetcher/direct.go` (extract allowlist seam + `FetchTitle`)
- Modify: `internal/db/queries.sql` (bookmark queries)
- Create: `internal/api/project_bookmarks_handlers.go`
- Modify: `internal/api/projects_handlers.go` (`handleGetProject` projections)
- Modify: `internal/api/server.go` (routes)
- Modify: `docs/api-notes.md`
- Test: `internal/fetcher/direct_test.go` (FetchTitle), `internal/api/project_bookmarks_test.go`

**Interfaces:**
- Consumes: `requireOwnedProject`; `validExternalURL` (`items_handlers.go:29`); `DirectEngine` internals (`checkURL`, redirect loop).
- Produces:
  - `func (d *DirectEngine) FetchTitle(ctx context.Context, rawURL string) string` — never errors; falls back to hostname.
  - `GET /projects/{id}` (owner AND sharee) gains `"bookmarks": [{id, url, title, created_at, archived_file: <file row> | null}]` (`archived_file` null until Task 7 populates it).
  - Routes: `POST /api/v1/projects/{id}/bookmarks` `{url, title?}` → 201; `PATCH /api/v1/projects/{id}/bookmarks/{bid}` `{url?, title?}` → 200; `DELETE …/bookmarks/{bid}` → 204/404.

- [ ] **Step 1: Write the failing fetcher tests**

In `internal/fetcher/direct_test.go` (follow its existing httptest + `LoopbackPolicy` patterns):

```go
func TestFetchTitle(t *testing.T) {
	// HTML page with a title → extracted, whitespace-collapsed
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><head><title>  LM358&nbsp;Datasheet </title></head><body>x</body></html>")
	}))
	defer srv.Close()
	d := NewDirectEngine(LoopbackPolicy())
	if got := d.FetchTitle(context.Background(), srv.URL+"/page"); got != "LM358 Datasheet" {
		t.Fatalf("title = %q", got)
	}

	// non-HTML body / network failure / bad URL → hostname fallback
	if got := d.FetchTitle(context.Background(), srv.URL+"/noTitle"); got != "127.0.0.1" {
		t.Fatalf("no-title fallback = %q, want hostname", got) // that path serves HTML without <title>
	}
	if got := d.FetchTitle(context.Background(), "http://127.0.0.1:1/nope"); got != "127.0.0.1" {
		t.Fatalf("dead-host title = %q, want hostname", got)
	}
	if got := d.FetchTitle(context.Background(), "not a url"); got != "not a url" {
		t.Fatalf("garbage url title = %q, want raw string", got)
	}
}
```

(The httptest server above must also serve `/noTitle` with title-less HTML; pinned fallback behaviors: hostname from `url.Parse`, raw string (≤200 chars) for unparseable URLs.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/fetcher/ -run TestFetchTitle -v`
Expected: FAIL — no `FetchTitle` method.

- [ ] **Step 3: Implement FetchTitle**

In `internal/fetcher/direct.go`: refactor `Fetch` — extract the redirect-following core into `func (d *DirectEngine) fetchAllow(ctx context.Context, rawURL string, allow func(string) bool) (Result, error)`; `Fetch` calls it with the existing `allowlisted`; a new `allowHTML` accepts `text/html` prefixes. Then:

```go
// FetchTitle does a best-effort HTML <title> extraction for bookmark
// labeling: 5s budget, 256KB read cap, policy-checked like every fetch.
// It NEVER returns an error — any failure collapses to the URL hostname.
func (d *DirectEngine) FetchTitle(ctx context.Context, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return truncateTitle(rawURL)
	}
	fallback := truncateTitle(u.Hostname())
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := d.fetchAllow(ctx, rawURL, allowHTML)
	if err != nil {
		return fallback
	}
	b := res.Body
	if len(b) > 256<<10 {
		b = b[:256<<10]
	}
	m := titleRe.FindSubmatch(b)
	if m == nil {
		return fallback
	}
	title := strings.Join(strings.Fields(string(m[1])), " ")
	title = strings.ReplaceAll(title, "&nbsp;", " ")
	title = html.UnescapeString(title)
	return truncateTitle(title)
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func truncateTitle(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		return "untitled"
	}
	return s
}
```

- [ ] **Step 4: Run fetcher tests green**

Run: `go test ./internal/fetcher/ -v`
Expected: PASS (existing direct tests unaffected — `Fetch` behavior identical through the seam).

- [ ] **Step 5: API tests**

```go
func TestProjectBookmarksCRUD(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pb1@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "pb2@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "B", "visibility": "shared"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// add with explicit title
	r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": srv.URL + "/docs", "title": "Docs"}, owner) // srv: local httptest serving a titled page (see below)
	var bm struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	decodeBody(t, r, &bm)
	if bm.Title != "Docs" {
		t.Fatalf("title = %q", bm.Title)
	}

	// add without title → auto-title from <title> (engine + LoopbackPolicy)
	r = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": srv.URL + "/page"}, owner)
	decodeBody(t, r, &bm)
	if bm.Title != "LM358 Datasheet" { // whatever the fixture page's <title> is
		t.Fatalf("auto title = %q", bm.Title)
	}

	// invalid urls → 400
	for _, bad := range []string{"", "javascript:alert(1)", "ftp://x/y", strings.Repeat("https://a/", 500)} {
		if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks", map[string]any{"url": bad}, owner); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad url %q = %d", bad, r.StatusCode)
		}
	}

	// PATCH title + absent-keeps
	r = doJSON(t, ts, "PATCH", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID), map[string]any{"title": "Renamed"}, owner)
	decodeBody(t, r, &bm)
	if bm.Title != "Renamed" {
		t.Fatalf("patched title = %q", bm.Title)
	}

	// sharee mutations 404
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks", map[string]any{"url": "https://x.test/"}, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee add bookmark = %d", r.StatusCode)
	}

	// detail carries bookmarks for sharee too (archived_file null for now)
	var detail map[string]any
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, sharee), &detail)
	if bms := detail["bookmarks"].([]any); len(bms) != 2 {
		t.Fatalf("sharee bookmarks = %d, want 2", len(bms))
	}

	// delete → 204 then 404
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete bookmark = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID), nil, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("double delete = %d", r.StatusCode)
	}
}
```

(`srv` = an `httptest.Server` serving `/page` with a `<title>LM358 Datasheet</title>` HTML body and `/docs` anything; the test server's `fetchDirect` engine uses `LoopbackPolicy` so loopback URLs are fetchable — same as the fetch-job tests.)

- [ ] **Step 6: Run to verify failure**

Run: `go test ./internal/api/ -run TestProjectBookmarks -v`
Expected: FAIL — no routes.

- [ ] **Step 7: Queries + handlers**

```sql
-- name: AddProjectBookmark :one
INSERT INTO project_bookmarks (project_id, url, title, created_at) VALUES (?, ?, ?, ?)
RETURNING id, project_id, url, title, archived_file_id, created_at;

-- name: GetProjectBookmark :one
SELECT * FROM project_bookmarks WHERE id = ? AND project_id = ?;

-- name: UpdateProjectBookmark :exec
UPDATE project_bookmarks SET url = ?, title = ? WHERE id = ? AND project_id = ?;

-- name: DeleteProjectBookmark :execrows
DELETE FROM project_bookmarks WHERE id = ? AND project_id = ?;

-- name: ListProjectBookmarks :many
SELECT b.id, b.url, b.title, b.created_at,
       f.id AS f_id, f.storage_key AS f_key, f.thumb_key AS f_thumb,
       f.filename AS f_name, f.mime_type AS f_mime, f.size_bytes AS f_size,
       f.created_at AS f_created
FROM project_bookmarks b
LEFT JOIN project_files f ON f.id = b.archived_file_id
WHERE b.project_id = ? ORDER BY b.created_at, b.id;
```

`make generate` (expect `sql.Null*` on the joined side; verify exact names — the `_2` gotcha). `internal/api/project_bookmarks_handlers.go`:

```go
func (s *Server) handleAddProjectBookmark(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	var req struct {
		URL   string  `json:"url"`
		Title *string `json:"title"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validExternalURL(req.URL) {
		writeErr(w, http.StatusBadRequest, "validation_error", "url must be an http(s) URL of at most 2000 chars")
		return
	}
	title := ""
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = strings.TrimSpace(*req.Title)
		if len(title) > 200 {
			title = title[:200]
		}
	} else if s.fetchDirect != nil {
		title = s.fetchDirect.FetchTitle(r.Context(), req.URL)
	}
	bm, err := s.q.AddProjectBookmark(r.Context(), dbpkg.AddProjectBookmarkParams{
		ProjectID: p.ID, URL: req.URL, Title: title,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectBookmarkJSON(bm, dbpkg.ListProjectBookmarksRow{}))
}

func projectBookmarkJSON(bm dbpkg.ProjectBookmark, f dbpkg.ListProjectBookmarksRow) map[string]any {
	var archived any
	if f.FID.Valid { // LEFT JOIN hit — archived_file object (see Task 7 for row reuse)
		archived = map[string]any{
			"id": f.FID.Int64, "storage_key": f.FKey.String, "thumb_key": thumbJSON(f.FThumb),
			"filename": f.FName.String, "mime_type": f.FMime.String,
			"size_bytes": f.FSize.Int64, "created_at": f.FCreated.String,
		}
	}
	return map[string]any{
		"id": bm.ID, "url": bm.URL, "title": bm.Title,
		"created_at": bm.CreatedAt, "archived_file": archived,
	}
}
```

(The exact nullable plumbing depends on generated names — write `projectBookmarkJSON` against the generated `ListProjectBookmarksRow` so both the list and single-bookmark paths share it; for the 201/PATCH responses with no archive, pass a zero row. `nullStringOr` — use the existing nullable-JSON helpers in `respond.go` (`nullString`/`thumbJSON`) as appropriate.)

PATCH handler:

```go
func (s *Server) handlePatchProjectBookmark(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	bid, err := strconv.ParseInt(chi.URLParam(r, "bid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid bookmark id")
		return
	}
	bm, err := s.q.GetProjectBookmark(r.Context(), dbpkg.GetProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	var req struct {
		URL   *string `json:"url"`
		Title *string `json:"title"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	url, title := bm.URL, bm.Title
	if req.URL != nil {
		if !validExternalURL(*req.URL) {
			writeErr(w, http.StatusBadRequest, "validation_error", "url must be an http(s) URL of at most 2000 chars")
			return
		}
		url = *req.URL
	}
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		if len(title) > 200 {
			title = title[:200]
		}
	}
	if err := s.q.UpdateProjectBookmark(r.Context(), dbpkg.UpdateProjectBookmarkParams{
		URL: url, Title: title, ID: bm.ID, ProjectID: p.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	bm, _ = s.q.GetProjectBookmark(r.Context(), dbpkg.GetProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	writeJSON(w, http.StatusOK, projectBookmarkJSON(bm, dbpkg.ListProjectBookmarksRow{}))
}
```

DELETE handler (cascades the linked archive file — spec §6):

```go
func (s *Server) handleDeleteProjectBookmark(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	bid, err := strconv.ParseInt(chi.URLParam(r, "bid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid bookmark id")
		return
	}
	bm, err := s.q.GetProjectBookmark(r.Context(), dbpkg.GetProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	rows, err := s.q.DeleteProjectBookmark(r.Context(), dbpkg.DeleteProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if rows == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	if bm.ArchivedFileID.Valid { // also retire the archive row + bytes
		if f, err := s.q.GetProjectFile(r.Context(), dbpkg.GetProjectFileParams{ID: bm.ArchivedFileID.Int64, ProjectID: p.ID}); err == nil {
			if _, err := s.q.DeleteProjectFile(r.Context(), dbpkg.DeleteProjectFileParams{ID: f.ID, ProjectID: p.ID}); err == nil {
				s.deleteFileBytes(f.StorageKey)
				if f.ThumbKey.Valid {
					s.deleteFileBytes(f.ThumbKey.String)
				}
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Routes:

```go
			r.Post("/projects/{id}/bookmarks", s.handleAddProjectBookmark)
			r.Patch("/projects/{id}/bookmarks/{bid}", s.handlePatchProjectBookmark)
			r.Delete("/projects/{id}/bookmarks/{bid}", s.handleDeleteProjectBookmark)
```

`handleGetProject` (both projections): `ListProjectBookmarks` → `body["bookmarks"]` via the shared JSON helper.

- [ ] **Step 8: Run tests green; api-notes; commit**

Run: `go vet ./... && go test ./internal/... ` — PASS. api-notes: bookmark CRUD, auto-title semantics (best-effort, hostname fallback, never blocks the add), `validExternalURL` contract, archived_file shape (null until archived).

```bash
git add internal/fetcher/direct.go internal/fetcher/direct_test.go internal/db/queries.sql internal/db/queries.sql.go internal/api/project_bookmarks_handlers.go internal/api/projects_handlers.go internal/api/server.go internal/api/project_bookmarks_test.go docs/api-notes.md
git commit -m "feat: project bookmarks with best-effort auto-title"
```

---

### Task 7: Bookmark archiving (jobs generalization)

**Files:**
- Modify: `internal/api/jobs.go` (target kinds), `internal/api/project_bookmarks_handlers.go` (archive endpoint)
- Modify: `internal/api/server.go` (route)
- Modify: `docs/api-notes.md`
- Test: `internal/api/jobs_test.go` (extend) or `internal/api/project_bookmarks_test.go`

**Interfaces:**
- Consumes: `jobState`/`runJob` (`jobs.go:30,122`), `s.fetchArchive fetcher.Engine`, ArchiveEngine, Task 6's bookmark queries + `projectFileJSON`.
- Produces: `POST /api/v1/projects/{id}/bookmarks/{bid}/archive` → `202 {job_id}` (owner only; `fetchArchive == nil` → 503 `fetching_disabled`). `jobState` gains `kind` (`"item-doc"` | `"bookmark-archive"`), `bookmarkProject`, `bookmarkID`, `title`. Completion for bookmark jobs: new `project_files` row (`application/pdf`, `<sanitized title>.pdf`) linked via `SetBookmarkArchive`; re-archive deletes the previous row + bytes.

- [ ] **Step 1: Write the failing tests**

Follow `jobs_test.go` patterns (`servePDF`, `startFetchJob`, `waitJob` — here hand-rolled for bookmarks):

```go
func TestBookmarkArchiveJob(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "arch1@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "arch2@test.dev", "longpassword1")
	target := serveTitledPage(t) // httptest: HTML page (archive engine stub turns it into a PDF — check how jobs_test fakes archive mode)

	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "A", "visibility": "shared"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)
	resp = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": target.URL, "title": "Build Log"}, owner)
	var bm struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &bm)

	// sharee cannot archive
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee archive = %d, want 404", r.StatusCode)
	}

	// owner archives: 202 → poll → done → archived_file populated
	r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, owner)
	if r.StatusCode != http.StatusAccepted {
		t.Fatalf("archive = %d", r.StatusCode)
	}
	var job struct{ JobID string `json:"job_id"` }
	decodeBody(t, r, &job)
	waitForJobDone(t, ts, owner, job.JobID) // reuse jobs_test.go's poll helper

	var detail struct {
		Bookmarks []struct {
			Title        string `json:"title"`
			ArchivedFile *struct {
				Filename string `json:"filename"`
				MimeType string `json:"mime_type"`
				StorageKey string `json:"storage_key"`
			} `json:"archived_file"`
		} `json:"bookmarks"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, owner), &detail)
	if len(detail.Bookmarks) != 1 || detail.Bookmarks[0].ArchivedFile == nil {
		t.Fatalf("archived_file missing: %+v", detail.Bookmarks)
	}
	af := detail.Bookmarks[0].ArchivedFile
	if af.Filename != "Build Log.pdf" || af.MimeType != "application/pdf" {
		t.Fatalf("archived file = %+v", af)
	}
	oldKey := af.StorageKey

	// re-archive: new row linked, OLD bytes gone
	doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, owner)
	// (poll again) then:
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, owner), &detail)
	newKey := detail.Bookmarks[0].ArchivedFile.StorageKey
	if newKey == oldKey {
		t.Fatalf("re-archive reused key %q", newKey)
	}
	if r := doGet(t, ts, "/files/"+oldKey, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("old archive still served: %d", r.StatusCode)
	}
}

func TestBookmarkArchiveFailureLeavesClean(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "arch3@test.dev", "longpassword1")
	// a target the (stubbed) archive engine fails on — follow jobs_test.go's
	// failing-archive fixture (e.g. a URL that returns 500)
	target := serveArchiveFailure(t)
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "AF"}, owner)
	var proj struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)
	resp = doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks",
		map[string]any{"url": target.URL, "title": "Broken"}, owner)
	var bm struct{ ID int64 `json:"id"` }
	decodeBody(t, resp, &bm)

	r := doJSON(t, ts, "POST", "/api/v1/projects/"+pid+"/bookmarks/"+fmtInt64(bm.ID)+"/archive", nil, owner)
	var job struct{ JobID string `json:"job_id"` }
	decodeBody(t, r, &job)
	jobStatus := waitForJobDone(t, ts, owner, job.JobID)
	if jobStatus.Status != "failed" {
		t.Fatalf("job = %+v, want failed", jobStatus)
	}

	// bookmark untouched: archived_file stays null, files list stays empty
	var detail struct {
		Bookmarks []struct {
			ArchivedFile any `json:"archived_file"`
		} `json:"bookmarks"`
		Files []any `json:"files"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, owner), &detail)
	if len(detail.Bookmarks) != 1 || detail.Bookmarks[0].ArchivedFile != nil {
		t.Fatalf("bookmark after failed archive: %+v", detail.Bookmarks)
	}
	if len(detail.Files) != 0 {
		t.Fatalf("orphan files after failed archive: %d", len(detail.Files))
	}
}
```

(`serveArchiveFailure`/`waitForJobDone` — reuse/adapt the helpers `jobs_test.go` already has for failing archive-mode jobs and job polling.)

(For the engine: check how existing `jobs_test.go` tests `mode: "archive"` — `stubChromePath()` + the archive engine's chromium stub script; reuse that mechanism verbatim, including its titled-page/PDF fixture helpers.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run TestBookmarkArchive -v`
Expected: FAIL — route missing.

- [ ] **Step 3: Generalize jobs + implement the endpoint**

`internal/api/jobs.go`:

```go
type jobState struct {
	id      string
	userID  int64
	kind    string // "item-doc" | "bookmark-archive"
	itemID  int64
	// bookmark-archive target (kind == "bookmark-archive")
	bookmarkProject int64
	bookmarkID      int64
	title           string
	status  string
	err     string
	docID   int64
	created time.Time
}
```

`handleItemFetch` sets `kind: "item-doc"` in its `jobState` literal. `runJob` — everything through `s.st.Save` stays shared; replace the `AddItemDocument` tail with a switch:

```go
	switch j.kind {
	case "item-doc":
		doc, err := s.q.AddItemDocument(storeCtx, dbpkg.AddItemDocumentParams{ /* existing fields */ })
		if err != nil { /* existing rollback + failJob */ }
		s.jobsMu.Lock()
		j.status, j.docID = "done", doc.ID
		s.jobsMu.Unlock()
	case "bookmark-archive":
		if err := s.completeBookmarkArchive(storeCtx, j, key, size); err != nil {
			s.st.Delete(key)
			s.failJob(j, "could not attach archive")
			return
		}
		s.jobsMu.Lock()
		j.status = "done"
		s.jobsMu.Unlock()
	}
```

New `completeBookmarkArchive` in `project_bookmarks_handlers.go`:

```go
// completeBookmarkArchive stores the fetched PDF as a project file and links
// it to the bookmark, replacing (and deleting) any previous archive.
func (s *Server) completeBookmarkArchive(ctx context.Context, j *jobState, key string, size int64) error {
	bm, err := s.q.GetProjectBookmark(ctx, dbpkg.GetProjectBookmarkParams{ID: j.bookmarkID, ProjectID: j.bookmarkProject})
	if err != nil {
		return err // bookmark vanished mid-job: caller deletes the stored file
	}
	file, err := s.q.AddProjectFile(ctx, dbpkg.AddProjectFileParams{
		ProjectID: j.bookmarkProject, StorageKey: key,
		Filename: sanitizeFilename(j.title) + ".pdf", MimeType: "application/pdf",
		SizeBytes: size, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if err := s.q.SetBookmarkArchive(ctx, dbpkg.SetBookmarkArchiveParams{ArchivedFileID: sqlNullInt64(file.ID), ID: bm.ID}); err != nil {
		s.q.DeleteProjectFile(ctx, dbpkg.DeleteProjectFileParams{ID: file.ID, ProjectID: j.bookmarkProject})
		s.deleteFileBytes(key)
		return err
	}
	if bm.ArchivedFileID.Valid { // re-archive: retire the previous PDF
		old, err := s.q.GetProjectFile(ctx, dbpkg.GetProjectFileParams{ID: bm.ArchivedFileID.Int64, ProjectID: j.bookmarkProject})
		if err == nil {
			if _, err := s.q.DeleteProjectFile(ctx, dbpkg.DeleteProjectFileParams{ID: old.ID, ProjectID: j.bookmarkProject}); err == nil {
				s.deleteFileBytes(old.StorageKey)
			}
		}
	}
	return nil
}
```

Add the query (`queries.sql` + `make generate`):

```sql
-- name: SetBookmarkArchive :exec
UPDATE project_bookmarks SET archived_file_id = ? WHERE id = ?;
```

The endpoint (in `project_bookmarks_handlers.go`):

```go
// handleArchiveBookmark queues an archive job (POST
// /projects/{id}/bookmarks/{bid}/archive). Owner-only; 404 for sharees even
// on shared projects (spec §6).
func (s *Server) handleArchiveBookmark(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	bid, err := strconv.ParseInt(chi.URLParam(r, "bid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid bookmark id")
		return
	}
	bm, err := s.q.GetProjectBookmark(r.Context(), dbpkg.GetProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	if s.fetchArchive == nil {
		writeErr(w, http.StatusServiceUnavailable, "fetching_disabled", "archiving is not available on this server")
		return
	}
	id, err := newJobID()
	if err != nil {
		s.internalErr(w, err)
		return
	}
	j := &jobState{
		id: id, userID: u.ID, kind: "bookmark-archive",
		bookmarkProject: p.ID, bookmarkID: bm.ID, title: bm.Title,
		status: "running", created: time.Now(),
	}
	s.jobsMu.Lock()
	pruneJobsLocked(s.jobs, time.Now())
	s.jobs[id] = j
	s.jobsMu.Unlock()
	go s.runJob(context.Background(), j, s.fetchArchive, bm.URL)
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}
```

Route:

```go
			r.Post("/projects/{id}/bookmarks/{bid}/archive", s.handleArchiveBookmark)
```

- [ ] **Step 4: Run tests green**

Run: `go vet ./... && go test ./internal/api/ -run 'TestBookmarkArchive|TestFetchJob' -v`
Expected: PASS (existing item fetch jobs unaffected).

- [ ] **Step 5: api-notes + commit**

api-notes: archive endpoint (202 job, owner-only, chromium required → 503), re-archive replacement semantics, job completion adds a `project_files` row visible in materials, failed archives leave the bookmark clean.

```bash
git add internal/api/jobs.go internal/api/project_bookmarks_handlers.go internal/db/queries.sql internal/db/queries.sql.go internal/api/server.go internal/api/jobs_test.go internal/api/project_bookmarks_test.go docs/api-notes.md
git commit -m "feat: opt-in bookmark archiving via generalized fetch jobs"
```

---

### Task 8: Frontend — types, client, badges, SharedView projects

**Files:**
- Modify: `web/src/api/types.ts`, `web/src/api/client.ts`
- Modify: `web/src/views/ProjectsView.vue` (+ its test)
- Modify: `web/src/views/SharedView.vue` (+ its test)
- Rebuild: `internal/webfs/dist` via `make web` (commit)

**Interfaces:**
- Consumes: Task 2–7 API shapes (owner_view, notes_log?, comments, files, bookmarks).
- Produces (used by Tasks 9–11): `Project` type with `visibility`; `ProjectDetail`, `ProjectNote`, `ProjectComment`, `ProjectFile`, `ProjectBookmark`, `ProjectOwnerItem`, `ProjectViewerItem` types; client fns `addProjectNote`, `deleteProjectNote`, `addProjectComment`, `deleteProjectComment`, `uploadProjectFile`, `deleteProjectFile`, `addProjectBookmark`, `patchProjectBookmark`, `deleteProjectBookmark`, `archiveProjectBookmark`.

- [ ] **Step 1: Types + client (with the Project type test following existing type-test conventions — types are exercised via view tests; no separate unit test)**

`web/src/api/types.ts`:

```ts
export interface Project {
  id: number; name: string; status: string; notes: string
  visibility: 'private' | 'shared'
  created_at: string; updated_at: string
}
export type ProjectOwnerItem = {
  item_id: number; item_name: string; item_qty: number; item_unit: string
  qty_used: number | null; notes: string; added_at: string; shared: boolean
}
export type ProjectViewerItem = { item_name: string; qty_used: number | null; shared: boolean }
export interface ProjectNote { id: number; body: string; created_at: string }
export interface ProjectComment { id: number; author: string; mine: boolean; body: string; created_at: string }
export interface ProjectFile {
  id: number; storage_key: string; thumb_key: string | null
  filename: string; mime_type: string; size_bytes: number; created_at: string
}
export interface ProjectBookmark {
  id: number; url: string; title: string; created_at: string
  archived_file: ProjectFile | null
}
export interface ProjectDetail {
  project: Project; owner_view: boolean
  items: (ProjectOwnerItem | ProjectViewerItem)[]
  notes_log?: ProjectNote[] // key absent on the sharee projection
  comments: ProjectComment[]; files: ProjectFile[]; bookmarks: ProjectBookmark[]
}
export interface SharedProject { id: number; name: string; status: string; owner_name: string; updated_at: string }
```

(If an existing `ProjectItem` type exists, extend/replace consistently with its current field names — owner rows keep today's names plus `shared`.)

`web/src/api/client.ts` (follow the `addLocationPhoto` section style):

```ts
// Projects flesh-out: notes, comments, materials, bookmarks (api-notes "Projects").
export const addProjectNote = (id: number, body: string) =>
  api.post<ProjectNote>(`/api/v1/projects/${id}/notes`, { body })
export const deleteProjectNote = (id: number, nid: number) =>
  api.del(`/api/v1/projects/${id}/notes/${nid}`)
export const addProjectComment = (id: number, body: string) =>
  api.post<ProjectComment>(`/api/v1/projects/${id}/comments`, { body })
export const deleteProjectComment = (id: number, cid: number) =>
  api.del(`/api/v1/projects/${id}/comments/${cid}`)
export const uploadProjectFile = (id: number, file: File) =>
  api.upload<ProjectFile>(`/api/v1/projects/${id}/files`, 'file', file, file.name)
export const deleteProjectFile = (id: number, fid: number) =>
  api.del(`/api/v1/projects/${id}/files/${fid}`)
export const addProjectBookmark = (id: number, url: string) =>
  api.post<ProjectBookmark>(`/api/v1/projects/${id}/bookmarks`, { url })
export const patchProjectBookmark = (id: number, bid: number, body: { url?: string; title?: string }) =>
  api.patch<ProjectBookmark>(`/api/v1/projects/${id}/bookmarks/${bid}`, body)
export const deleteProjectBookmark = (id: number, bid: number) =>
  api.del(`/api/v1/projects/${id}/bookmarks/${bid}`)
export const archiveProjectBookmark = (id: number, bid: number) =>
  api.post<{ job_id: string }>(`/api/v1/projects/${id}/bookmarks/${bid}/archive`)
```

- [ ] **Step 2: Write failing view tests**

`ProjectsView.test.ts` (extend; follow the file's existing mount+stub pattern):

```ts
it('shows a shared badge for shared projects', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
    data: [{ id: 1, name: 'Bench PSU', status: 'active', notes: '', visibility: 'shared', created_at: '', updated_at: '' }],
    total: 1,
  }), { status: 200 })))
  const w = await mountView()
  const badge = w.find('[data-visibility-badge]')
  expect(badge.text()).toBe('shared')
})
```

`SharedView.test.ts` (extend):

```ts
it('lists shared projects from other users', async () => {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    calls.push(`GET ${url}`)
    if (url.includes('/shared/projects'))
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'Bench PSU', status: 'active', owner_name: 'Alice', updated_at: '2026-10-23T00:00:00Z' }], total: 1 }), { status: 200 })
    return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
  }))
  const { w } = await mountView()
  expect(calls).toContain('GET /api/v1/shared/projects')
  const block = w.find('[data-shared-projects]')
  expect(block.text()).toContain('Bench PSU')
  expect(block.text()).toContain('Alice')
  expect(block.find('a[href="/projects/5"]').exists()).toBe(true)
})
```

- [ ] **Step 3: Run tests to fail**

Run: `cd web && npx vitest run src/views/ProjectsView.test.ts src/views/SharedView.test.ts`
Expected: FAIL.

- [ ] **Step 4: Implement**

`ProjectsView.vue`: per-row badge next to the status badge (copy the `statusBadgeClass` pattern; shared = green-ish accent, private = gray; `data-visibility-badge`).
`SharedView.vue`: after the items block, add:

```vue
<h2 v-if="projects.length" class="text-lg font-semibold dark:text-slate-100">Projects</h2>
<div v-if="projects.length" data-shared-projects class="space-y-2">
  <router-link v-for="p in projects" :key="p.id" :to="`/projects/${p.id}`"
    class="block bg-white dark:bg-slate-800 rounded-xl shadow px-4 py-3 hover:shadow-md">
    <div class="font-medium dark:text-slate-100">{{ p.name }}
      <span class="ml-1.5 rounded-full bg-slate-200 dark:bg-slate-700 text-slate-600 dark:text-slate-300 px-1.5 py-0.5 text-xs font-normal align-middle">{{ p.status }}</span>
    </div>
    <div class="text-xs text-slate-500 dark:text-slate-400">from {{ p.owner_name }}</div>
  </router-link>
</div>
<p v-else-if="!items.length && !projects.length" class="…existing empty state…">No shared items found.</p>
```

with `projects` fetched in parallel (`api.get<ListResponse<SharedProject>>('/api/v1/shared/projects')`), loading/error handling matching the file's existing conventions (toast on error, no empty catch).

- [ ] **Step 5: Tests green + build + commit**

Run: `cd web && npx vitest run && npx vue-tsc -b && cd .. && make web`
Expected: all green, dist rebuilt.

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/views/ProjectsView.vue web/src/views/ProjectsView.test.ts web/src/views/SharedView.vue web/src/views/SharedView.test.ts internal/webfs/dist
git commit -m "feat(web): project visibility badges + shared projects section"
```

---

### Task 9: Frontend — ProjectDetailView viewer mode + masked BOM + visibility toggle

**Files:**
- Modify: `web/src/views/ProjectDetailView.vue` (+ its test)
- Rebuild: dist via `make web`

**Interfaces:**
- Consumes: `ProjectDetail` + client fns (Task 8); `owner_view`, `notes_log?`.
- Produces: the detail view renders owner controls only when `owner_view`; the header badge toggles visibility via PATCH (the ItemDetailView `toggleVisibility` pattern at `ItemDetailView.vue:130-139`); BOM rows render masked rows for sharees. Tasks 10–11 mount their section components into this view's layout (they will receive `project`, `detail` slices, and an `owner_view` prop).

- [ ] **Step 1: Failing tests**

Extend `ProjectDetailView.test.ts` (existing fetch-mock patterns; `ownerDetail`/`shareeDetail` are fixtures shaped like `ProjectDetail`):

```ts
it('hides owner controls and links in the sharee view', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(shareeDetail), { status: 200 })))
  const w = await mountView()
  expect(w.find('[data-status]').exists()).toBe(false)
  expect(w.find('[data-edit]').exists()).toBe(false)
  expect(w.find('[data-delete]').exists()).toBe(false)
  expect(w.find('[data-add-item]').exists()).toBe(false)
  expect(w.find('[data-qty-used]').exists()).toBe(false)
  expect(w.find('[data-item-name]').exists()).toBe(false) // plain text, no router-link
  expect(w.text()).toContain('Priv part') // masked row: name still shown
})

it('toggles visibility from the badge (owner)', async () => {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push(`${init?.method ?? 'GET'} ${url}`)
    if (init?.method === 'PATCH') return new Response(JSON.stringify({ ...ownerDetail.project, visibility: 'shared' }), { status: 200 })
    if (url.endsWith('/items?page_size=200')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    return new Response(JSON.stringify(ownerDetail), { status: 200 })
  }))
  const w = await mountView()
  await w.find('[data-visibility-badge]').trigger('click')
  await flushPromises()
  expect(calls.some(c => c.startsWith('PATCH /api/v1/projects/') && c.includes('{"visibility":"shared"}') || c === 'PATCH /api/v1/projects/1')).toBe(true)
})
```

(Adapt assertion style to whatever the existing file uses for request bodies — some existing tests inspect `init.body`; match them. `ownerDetail.items` uses the owner row shape with `item_id`; `shareeDetail.items` uses masked rows WITHOUT `item_id`.)

- [ ] **Step 2: Run to fail** — `cd web && npx vitest run src/views/ProjectDetailView.test.ts`

- [ ] **Step 3: Implement**

In `ProjectDetailView.vue`:
- `const detail = ref<ProjectDetail | null>(null)`; `fetchDetail` stores the whole response; `project.value = res.project`; `items.value = res.items`; `notes = res.notes_log ?? []` (held for Task 10).
- Header: visibility badge-is-toggle (owner only), cloned from ItemDetailView:

```ts
const visibilitySaving = ref(false)
async function toggleVisibility() {
  if (!project.value || !detail.value?.owner_view || visibilitySaving.value) return
  visibilitySaving.value = true
  const next = project.value.visibility === 'shared' ? 'private' : 'shared'
  try {
    project.value = await api.patch<Project>(`/api/v1/projects/${project.value.id}`, { visibility: next })
    toast(next === 'shared' ? 'Project shared' : 'Project private', 'success')
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    visibilitySaving.value = false
  }
}
```

```html
<button v-if="detail?.owner_view" data-visibility-badge type="button" :disabled="visibilitySaving"
  class="rounded-full px-2 py-0.5 text-xs font-medium …"
  :class="project.visibility === 'shared' ? 'bg-emerald-100 text-emerald-700 …' : 'bg-slate-200 …'"
  :title="project.visibility === 'shared' ? 'Visible to every user — click to make private' : 'Only you — click to share'"
  @click="toggleVisibility">{{ project.visibility }}</button>
```

- BOM table: `template v-for` over rows; owner rows keep the current cells; sharee rows (`!detail.owner_view`) render `<td>{{ pi.item_name }}</td><td>—</td><td>{{ pi.qty_used ?? '—' }}</td><td></td>` (plain text, no link, no inputs). Add-item form, qty inputs, Remove buttons, status select, Edit/Delete: wrap in `v-if="detail?.owner_view"`.
- Relabel the notes blob: textarea + header display read "Description" (maxlength stays; the field is still `notes` on the wire — api-notes documents the label).

- [ ] **Step 4: Tests green + build + commit**

Run: `cd web && npx vitest run && npx vue-tsc -b && cd .. && make web`

```bash
git add web/src/views/ProjectDetailView.vue web/src/views/ProjectDetailView.test.ts internal/webfs/dist
git commit -m "feat(web): project detail viewer mode, visibility toggle, masked BOM"
```

---

### Task 10: Frontend — notes + comments sections

**Files:**
- Create: `web/src/components/ProjectNotesSection.vue` + `ProjectNotesSection.test.ts`
- Create: `web/src/components/ProjectCommentsSection.vue` + `ProjectCommentsSection.test.ts`
- Modify: `web/src/views/ProjectDetailView.vue` (mount sections; owner gets notes, everyone gets comments)
- Rebuild: dist via `make web`

**Interfaces:**
- Consumes: `ProjectNote`, `ProjectComment` types + note/comment client fns (Task 8); the view passes `projectId`, `notes: ProjectNote[]`, `comments: ProjectComment[]`, `mine: boolean` (owner_view).
- Produces: two presentational-async components emitting `refresh` after mutations. Props contract (used by the view):

```ts
// ProjectNotesSection
defineProps<{ projectId: number; notes: ProjectNote[] }>()
defineEmits<{ refresh: [] }>()
// ProjectCommentsSection
defineProps<{ projectId: number; comments: ProjectComment[]; canPost: boolean }>()
defineEmits<{ refresh: [] }>()
```

- [ ] **Step 1: Failing component tests**

`ProjectNotesSection.test.ts`:

```ts
import ProjectNotesSection from './ProjectNotesSection.vue'

const notes = [
  { id: 2, body: 'second', created_at: '2026-10-23T02:00:00Z' },
  { id: 1, body: 'first', created_at: '2026-10-23T01:00:00Z' },
]

it('adds a note and emits refresh', async () => {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push(`${init?.method} ${url}`)
    return new Response(JSON.stringify({ id: 3, body: 'third', created_at: '' }), { status: 201 })
  }))
  const w = mount(ProjectNotesSection, { props: { projectId: 1, notes } })
  await w.find('[data-note-body]').setValue('third')
  await w.find('[data-note-add]').trigger('submit')
  await flushPromises()
  expect(calls).toContain('POST /api/v1/projects/1/notes')
  expect(w.emitted('refresh')).toBeTruthy()
})

it('empty body is a no-op; delete posts to the right id', async () => {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push(`${init?.method} ${url}`)
    return new Response(null, { status: 204 })
  }))
  const w = mount(ProjectNotesSection, { props: { projectId: 1, notes } })
  await w.find('[data-note-add]').trigger('submit')
  expect(calls).toHaveLength(0)
  await w.findAll('[data-note-delete]')[0].trigger('click')
  await flushPromises()
  expect(calls).toContain('DELETE /api/v1/projects/1/notes/2')
  expect(w.emitted('refresh')).toBeTruthy()
})
```

`ProjectCommentsSection.test.ts` (same mount style; `comments` fixture with `mine` true/false entries):

```ts
it('shows delete only on own comments and only posts when canPost', async () => {
  const w = mount(ProjectCommentsSection, {
    props: { projectId: 1, comments: [
      { id: 1, author: 'Alice', mine: false, body: 'nice', created_at: '' },
      { id: 2, author: 'You', mine: true, body: 'thanks', created_at: '' },
    ], canPost: false },
  })
  expect(w.findAll('[data-comment-delete]')).toHaveLength(1) // only mine
  expect(w.find('[data-comment-body]').exists()).toBe(false) // no post box
})
```

- [ ] **Step 2: Run to fail** — `cd web && npx vitest run src/components/ProjectNotesSection.test.ts src/components/ProjectCommentsSection.test.ts`

- [ ] **Step 3: Implement components**

Mirror ItemDetailView's notes thread UI (textarea + Add button; entries with timestamp + Delete; whitespace-pre-wrap bodies; `maxlength=2000`; error toasts; no empty catches). Comments adds `canPost` gating and per-entry `mine` delete buttons; a viewer sees the thread read-only plus (when `canPost`) the post box. Mount in the detail view after the BOM card:

```html
<ProjectNotesSection v-if="detail?.owner_view" :project-id="project.id" :notes="notes" @refresh="fetchDetail" />
<ProjectCommentsSection :project-id="project.id" :comments="detail?.comments ?? []" :can-post="!!detail?.owner_view || project.visibility === 'shared'" @refresh="fetchDetail" />
```

- [ ] **Step 4: Tests green + build + commit**

Run: `cd web && npx vitest run && npx vue-tsc -b && cd .. && make web`

```bash
git add web/src/components/ProjectNotesSection.vue web/src/components/ProjectNotesSection.test.ts web/src/components/ProjectCommentsSection.vue web/src/components/ProjectCommentsSection.test.ts web/src/views/ProjectDetailView.vue internal/webfs/dist
git commit -m "feat(web): project notes log and comments thread"
```

---

### Task 11: Frontend — materials + bookmarks sections (archive UI, video confirm)

**Files:**
- Create: `web/src/utils/videoHosts.ts` (+ test)
- Create: `web/src/components/ProjectFilesSection.vue` + test
- Create: `web/src/components/ProjectBookmarksSection.vue` + test
- Modify: `web/src/views/ProjectDetailView.vue` (mount sections)
- Rebuild: dist via `make web`

**Interfaces:**
- Consumes: `ProjectFile`, `ProjectBookmark` + file/bookmark client fns; `GET /api/v1/jobs/{id}` polling (the ItemDetailView `runFetchJob` pattern); `isVideoHost`.
- Produces:

```ts
// web/src/utils/videoHosts.ts
const VIDEO_HOSTS = new Set([
  'youtube.com', 'www.youtube.com', 'm.youtube.com', 'youtu.be',
  'vimeo.com', 'player.vimeo.com', 'twitch.tv', 'www.twitch.tv',
  'dailymotion.com', 'www.dailymotion.com',
])
export function isVideoHost(url: string): boolean {
  try {
    return VIDEO_HOSTS.has(new URL(url).hostname.toLowerCase())
  } catch {
    return false
  }
}
```

Props contract for the components (view mounts them with these + `@refresh="fetchDetail"`; bookmarks also gets `owner: boolean`):

```ts
// ProjectFilesSection
defineProps<{ projectId: number; files: ProjectFile[]; owner: boolean }>()
// ProjectBookmarksSection
defineProps<{ projectId: number; bookmarks: ProjectBookmark[]; owner: boolean }>()
```

- [ ] **Step 1: Failing tests**

`videoHosts.test.ts`:

```ts
import { isVideoHost } from './videoHosts'

it.each(['https://www.youtube.com/watch?v=x', 'https://youtu.be/x', 'https://m.youtube.com/watch?v=x',
  'https://vimeo.com/123', 'https://www.twitch.tv/videos/1'])('flags video host: %s', u => {
  expect(isVideoHost(u)).toBe(true)
})
it.each(['https://www.ti.com/lit/ds', 'https://example.com/youtube', 'garbage'])('flags non-video: %s', u => {
  expect(isVideoHost(u)).toBe(false)
})
```

`ProjectFilesSection.test.ts`:

```ts
it('renders image thumbs and file rows; owner can upload/delete', async () => {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push(`${init?.method ?? 'GET'} ${url}`)
    if (init?.method === 'POST') return new Response(JSON.stringify({ id: 3, storage_key: 'k3', thumb_key: null, filename: 'manual.pdf', mime_type: 'application/pdf', size_bytes: 10, created_at: '' }), { status: 201 })
    return new Response(null, { status: 204 })
  }))
  const w = mount(ProjectFilesSection, { props: { projectId: 1, owner: true, files: [
    { id: 1, storage_key: 'k1', thumb_key: 't1', filename: 'diagram.png', mime_type: 'image/png', size_bytes: 4096, created_at: '' },
    { id: 2, storage_key: 'k2', thumb_key: null, filename: 'spec.pdf', mime_type: 'application/pdf', size_bytes: 1048576, created_at: '' },
  ] } })
  const img = w.find('img[data-file-thumb]')
  expect(img.attributes('src')).toBe('/files/t1') // thumb ?? storage_key
  expect(w.find('a[href="/files/k1"]').exists()).toBe(true) // click-through = ORIGINAL key
  expect(w.text()).toContain('spec.pdf')
  expect(w.text()).toContain('1.0 MB') // humanized — match the component's humanSize output exactly
  await w.find('[data-file-upload]').setValue(...) // File input trigger per existing upload tests
  await w.findAll('[data-file-delete]')[0].trigger('click')
  await flushPromises()
  expect(calls).toContain('DELETE /api/v1/projects/1/files/1')
})

it('viewer sees no upload/delete controls', () => {
  const w = mount(ProjectFilesSection, { props: { projectId: 1, owner: false, files: [/* one file */] } })
  expect(w.find('[data-file-upload]').exists()).toBe(false)
  expect(w.find('[data-file-delete]').exists()).toBe(false)
})
```

`ProjectBookmarksSection.test.ts`:

```ts
const bm = (over: Partial<ProjectBookmark> = {}): ProjectBookmark => ({
  id: 1, url: 'https://example.com/build', title: 'Build log', created_at: '', archived_file: null, ...over,
})

it('archives after job polling and emits refresh', async () => {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push(`${init?.method ?? 'GET'} ${url}`)
    if (url.includes('/archive')) return new Response(JSON.stringify({ job_id: 'j1' }), { status: 202 })
    if (url.includes('/jobs/j1')) return new Response(JSON.stringify({ status: 'done' }), { status: 200 })
    return new Response(null, { status: 204 })
  }))
  const w = mount(ProjectBookmarksSection, { props: { projectId: 1, owner: true, bookmarks: [bm()] } })
  await w.find('[data-archive]').trigger('click')
  await flushPromises()
  expect(calls).toContain('POST /api/v1/projects/1/bookmarks/1/archive')
  expect(calls).toContain('GET /api/v1/jobs/j1')
  expect(w.emitted('refresh')).toBeTruthy()
})

it('requires confirmation for video hosts — decline sends nothing', async () => {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push(`${init?.method ?? 'GET'} ${url}`)
    return new Response(null, { status: 204 })
  }))
  const w = mount(ProjectBookmarksSection, {
    props: { projectId: 1, owner: true, bookmarks: [bm({ url: 'https://www.youtube.com/watch?v=x' })] },
  })
  await w.find('[data-archive]').trigger('click')
  await flushPromises()
  expect(w.find('[data-video-confirm]').exists()).toBe(true)
  await w.find('[data-confirm-cancel]').trigger('click') // ConfirmDialog's cancel control — match its test anchors
  await flushPromises()
  expect(calls.filter(c => c.includes('/archive'))).toHaveLength(0)
})

it('links archived PDFs and hides owner controls from viewers', () => {
  const archived = bm({ archived_file: { id: 9, storage_key: 'pdf1', thumb_key: null, filename: 'Build log.pdf', mime_type: 'application/pdf', size_bytes: 5, created_at: '' } })
  const viewer = mount(ProjectBookmarksSection, { props: { projectId: 1, owner: false, bookmarks: [archived] } })
  expect(viewer.find('a[href="/files/pdf1"]').exists()).toBe(true) // View PDF
  expect(viewer.find('[data-archive]').exists()).toBe(false)
  expect(viewer.find('[data-bm-add]').exists()).toBe(false)
})
```

- [ ] **Step 2: Run to fail** — `cd web && npx vitest run src/utils/videoHosts.test.ts src/components/ProjectFilesSection.test.ts src/components/ProjectBookmarksSection.test.ts`

- [ ] **Step 3: Implement**

`ProjectFilesSection.vue`: hidden file input + "Add file" button (owner); grid/list of rows — `thumb_key ?? storage_key` img for `mime_type` starting `image/`, else an icon glyph + `filename` + `humanize(size_bytes)` (write a tiny local `humanSize`, or reuse an existing one if the codebase has one — grep first); every row links the ORIGINAL `storage_key` via `/files/…` (`target="_blank" rel="noopener"`); Delete per row (owner) with `ConfirmDialog`? — files are cheap to re-upload: inline button, no dialog (match ItemDetailView's document delete behavior — check and match it).
`ProjectBookmarksSection.vue`: url input + Add (owner; title auto-computed server-side); rows show `title` (link to `url`, external), hostname, created; when `archived_file`: "View PDF" link + "Re-archive"; else "Archive" (owner). Archive flow:

```ts
async function archive(bm: ProjectBookmark) {
  if (isVideoHost(bm.url) && !(await confirmVideo())) return
  archiving.value.add(bm.id)
  try {
    const { job_id } = await archiveProjectBookmark(props.projectId, bm.id)
    await pollJob(job_id) // GET /api/v1/jobs/:id every ~500ms until done/failed (runFetchJob pattern)
    emit('refresh')
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    archiving.value.delete(bm.id)
  }
}
```

with `confirmVideo` driving a `ConfirmDialog` ("Archiving captures the page as a PDF, not the video — archive anyway?", confirm-label "Archive anyway"). Failed jobs surface the job's `error` via toast; the row returns to its Archive state.
Mount both sections in the detail view (owner controls gated on `owner_view` via the `owner` prop).

- [ ] **Step 4: All green + build + commit**

Run: `cd web && npx vitest run && npx vue-tsc -b && cd .. && make web && make check`
Expected: everything green.

```bash
git add web/src/utils/videoHosts.ts web/src/utils/videoHosts.test.ts web/src/components/ProjectFilesSection.vue web/src/components/ProjectFilesSection.test.ts web/src/components/ProjectBookmarksSection.vue web/src/components/ProjectBookmarksSection.test.ts web/src/views/ProjectDetailView.vue internal/webfs/dist
git commit -m "feat(web): project materials + bookmarks with opt-in archive UI"
```

---

## Post-plan verification (whole feature)

After Task 11, before merge/push:
1. `make check` green; `go build -o stasher ./cmd/stasher`; `./scripts/smoke-web.sh` → `smoke ok`; `cd web && node scripts/smoke-authed.mjs` → ok.
2. Manual-style sweep via the authed smoke (optional but recommended): create project → toggle shared → second user sees it under Shared → Projects, comments, materials download; owner notes absent for sharee; bookmark + archive round-trip.
3. Update `docs/todo.md`: strike the projects line and the per-URL archive line (`~~…~~ done 2026-10-23`).
