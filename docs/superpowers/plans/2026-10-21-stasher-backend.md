# Stasher Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the complete Go backend for Stasher — auth, inventory, search, files, projects, sharing, and admin — as a single binary serving a JSON API behind Caddy.

**Architecture:** Single Go binary with an embedded SQLite database (WAL, FTS5) and goose migrations applied at startup. chi router exposes `/api/v1/*` and session-gated `/files/*`. sqlc generates the data layer from handwritten SQL. Every user-owned row is filtered by `user_id` in the data layer. The Vue SPA is a separate plan; this plan embeds a placeholder page under `internal/webfs/dist`.

**Tech Stack:** Go 1.27 (module `stasher`), `chi` v5, `modernc.org/sqlite` (pure Go, no CGO), `sqlc` (via `go tool`), `goose/v3` (embedded migrations), `golang.org/x/crypto` (Argon2id), `golang.org/x/time/rate`, `gopkg.in/yaml.v3`.

**Spec:** `docs/superpowers/specs/2026-10-21-stasher-design.md` — the plan argues from the spec; read both.

## Global Constraints

- Module path is `stasher`; all imports are `stasher/internal/...`.
- No CGO: SQLite driver is `modernc.org/sqlite` (driver name `"sqlite"`).
- DB pragmas on every connection: `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`.
- Timestamps are `TEXT` RFC3339 UTC (`time.Now().UTC().Format(time.RFC3339)`), set by Go, never by SQL defaults.
- JSON errors are exactly `{"error": {"code": "...", "message": "..."}}`.
- List endpoints take `page` (1-based) and `page_size` (default 50, max 200) and return `{"data": [...], "total": N}`.
- Every query on user-owned rows filters by `user_id` — isolation in the data layer, not the handler.
- Session cookies: `httpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, `MaxAge=315360000` (10 years). Sessions never expire; revocation deletes the row.
- Password hashing: Argon2id, encoded `$argon2id$v=19$m=65536,t=3,p=4$<b64salt>$<b64hash>`.
- Registration: `invite` mode (default) requires a valid invite code; **the first user ever registered needs no code and becomes admin** (bootstrap rule); config `registration: open` allows anyone.
- Photos accepted: jpeg/png/webp/gif (sniffed, not trusted from client); max 20 MB. Documents: any type, max 50 MB. Uploads shard under `data/uploads/yyyy/mm/`.
- File serving is session-gated: only the owner or (if `visibility='shared'`) other logged-in users.
- Migrations live in `internal/migrations/*.sql`, embedded, applied by goose at startup and via `stasher migrate`.
- Commit after every task, conventional commits (`feat:`, `test:`, `chore:`).

## Review Focus

The five input/failure classes most likely to bite a user, each pinned by a test in the owning task:

1. **Path traversal in storage keys** — `GET /api/v1/files/../../etc/passwd` must never read outside the upload root (expect 404). Test in Task 15.
2. **FTS syntax errors from raw search input** — quotes/operators in `q` must not 500; they are tokenized into quoted phrases (expect safe results, not an error). Test in Task 14.
3. **Logout must invalidate server-side** — replaying a logged-out cookie must 401 (cookie clearing alone is not enough). Test in Task 7.
4. **Pagination input abuse** — `page=0`, `page_size=99999`, non-numeric values must clamp, not error or blow up memory. Test in Task 13.
5. **Private-item existence leak** — copying or fetching another user's private item must 404 (not 403), so outsiders can't probe ids. Test in Task 17.

---

## File Structure

```
stasher/
├── cmd/stasher/main.go               # entrypoint: flags, subcommands serve/migrate, SPA fallback
├── internal/
│   ├── config/config.go              # Config struct, Load (YAML + env), defaults
│   ├── config/config_test.go
│   ├── db/open.go                    # Open (pragmas), Migrate (goose embedded)
│   ├── db/open_test.go
│   ├── db/queries.sql                # ALL sqlc queries (appended per task)
│   ├── db/*.go                       # sqlc-generated (do not hand-edit)
│   ├── migrations/00001_init.sql     # full initial schema + FTS + triggers
│   ├── migrations/embed.go           # //go:embed *.sql
│   ├── auth/password.go              # HashPassword / VerifyPassword (Argon2id)
│   ├── auth/password_test.go
│   ├── auth/session.go               # NewSessionToken, HashToken, CookieName, cookie helpers
│   ├── auth/session_test.go
│   ├── auth/limiter.go               # in-memory rate limiter
│   ├── auth/limiter_test.go
│   ├── files/storage.go              # Storage: Save/Open/Copy/Delete/Path, ValidKey
│   ├── files/storage_test.go
│   ├── api/server.go                 # Server struct, New, Router
│   ├── api/respond.go                # writeJSON/writeErr/decodeJSON/pagination
│   ├── api/middleware.go             # requireAuth, requireAdmin, logging
│   ├── api/auth_handlers.go          # register/login/logout/me/profile/devices/password
│   ├── api/admin_handlers.go         # users, invites
│   ├── api/locations_handlers.go
│   ├── api/tags_handlers.go
│   ├── api/items_handlers.go         # CRUD + list filters
│   ├── api/search.go                 # ftsQuery helper
│   ├── api/projects_handlers.go
│   ├── api/files_handlers.go         # photo/document upload, GET /files/*
│   ├── api/shared_handlers.go        # GET /shared, POST /items/{id}/copy
│   └── api/testutil_test.go          # newTestServer, registerUser, login helpers
├── sqlc.yaml                         # repo root
├── Makefile
├── .gitignore
└── internal/webfs/dist/index.html    # placeholder so go:embed compiles; replaced by frontend plan
```

---

### Task 1: Module scaffold, config, healthz

**Files:**
- Create: `go.mod`, `cmd/stasher/main.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/webfs/webfs.go`, `internal/webfs/dist/index.html`, `.gitignore` (Makefile arrives in Task 18)

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `config.Config{ListenAddr, BaseURL, DataDir, Registration, LogLevel string}` and `config.Load(path string) (Config, error)`; binary that serves `/healthz`.

- [ ] **Step 1: Init module and add placeholder embed**

```bash
cd /home/knivey/stasher
go mod init stasher
mkdir -p cmd/stasher internal/config internal/db internal/migrations internal/auth internal/files internal/api internal/webfs/dist
```

`internal/webfs/dist/index.html` (placeholder so `go:embed` compiles until the real SPA exists; the frontend plan will output the built SPA here):

```html
<!doctype html>
<html><head><title>stasher</title></head>
<body><p>Stasher API is running. The web UI is not built yet.</p></body></html>
```

`internal/webfs/webfs.go`:

```go
package webfs

import "embed"

//go:embed all:dist
var FS embed.FS
```

`.gitignore`:

```
/data/
/stasher
/web/node_modules/
*.db
*.db-wal
*.db-shm
```

- [ ] **Step 2: Write the failing config test**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q, want 127.0.0.1:8080", cfg.ListenAddr)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want ./data", cfg.DataDir)
	}
	if cfg.Registration != "invite" {
		t.Errorf("Registration = %q, want invite", cfg.Registration)
	}
	if cfg.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestLoadYamlAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stasher.yaml")
	os.WriteFile(path, []byte("listen_addr: 0.0.0.0:9000\ndata_dir: /srv/data\n"), 0o644)

	t.Setenv("STASHER_DATA_DIR", "/other/data")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "0.0.0.0:9000" {
		t.Errorf("ListenAddr = %q, want from YAML", cfg.ListenAddr)
	}
	if cfg.DataDir != "/other/data" {
		t.Errorf("DataDir = %q, want env override", cfg.DataDir)
	}
	if cfg.Registration != "invite" {
		t.Errorf("Registration = %q, want default", cfg.Registration)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `Load` not defined.

- [ ] **Step 4: Implement config**

`internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr   string `yaml:"listen_addr"`
	BaseURL      string `yaml:"base_url"`
	DataDir      string `yaml:"data_dir"`
	Registration string `yaml:"registration"` // "invite" | "open"
	LogLevel     string `yaml:"log_level"`
}

// Load reads the YAML file at path (optional), then applies env overrides.
// Empty path or missing file falls back to defaults.
func Load(path string) (Config, error) {
	cfg := Config{
		ListenAddr:   "127.0.0.1:8080",
		BaseURL:      "http://localhost:8080",
		DataDir:      "./data",
		Registration: "invite",
		LogLevel:     "info",
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		if err == nil {
			if err := yaml.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("parse config %s: %w", path, err)
			}
		}
	}
	env := map[string]*string{
		"STASHER_LISTEN_ADDR": &cfg.ListenAddr,
		"STASHER_BASE_URL":    &cfg.BaseURL,
		"STASHER_DATA_DIR":    &cfg.DataDir,
		"STASHER_REGISTRATION": &cfg.Registration,
		"STASHER_LOG_LEVEL":   &cfg.LogLevel,
	}
	for k, dst := range env {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			*dst = v
		}
	}
	if cfg.Registration != "invite" && cfg.Registration != "open" {
		return cfg, fmt.Errorf("registration must be invite or open, got %q", cfg.Registration)
	}
	return cfg, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/`
Expected: PASS. Then `go get gopkg.in/yaml.v3@latest` was implied — run `go mod tidy`.

- [ ] **Step 6: Minimal main with healthz**

`cmd/stasher/main.go` (temporary; replaced in Task 18):

```go
package main

import (
	"log"
	"net/http"

	"stasher/internal/config"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	log.Printf("listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
}
```

(`internal/webfs` is committed now but first referenced by `main` in Task 18 — that is fine, unused packages compile.)

- [ ] **Step 7: Verify build**

Run: `go build ./... && go vet ./...`
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add -A && git commit -m "chore: module scaffold with config and healthz"
```

---

### Task 2: Database open, pragmas, full initial migration

**Files:**
- Create: `internal/db/open.go`, `internal/db/open_test.go`, `internal/migrations/00001_init.sql`, `internal/migrations/embed.go`
- Modify: `go.mod` (add goose, modernc sqlite)

**Interfaces:**
- Consumes: nothing.
- Produces: `db.Open(path string) (*sql.DB, error)` (pragmas applied), `db.Migrate(*sql.DB) error` (embedded goose). Schema from the spec §4 — all tables, FTS5, triggers, indexes.

- [ ] **Step 1: Write the failing test**

`internal/db/open_test.go`:

```go
package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMigrateCreatesSchemaAndFTS(t *testing.T) {
	d := newMigratedDB(t)

	var tables []string
	rows, err := d.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	want := []string{"item_documents", "item_photos", "item_tags", "items", "items_fts", "locations", "project_items", "projects", "sessions", "tags", "users", "invites"}
	for _, w := range want {
		found := false
		for _, got := range tables {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("table %q missing; got %v", w, tables)
		}
	}
}

func TestFTSTriggersKeepIndexInSync(t *testing.T) {
	d := newMigratedDB(t)
	d.Exec(`INSERT INTO users (email, password_hash, display_name, created_at, updated_at) VALUES ('a@b.c','h','A','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	d.Exec(`INSERT INTO items (user_id, name, description, qty, visibility, created_at, updated_at) VALUES (1,'raspberry pi 5','8GB model',1,'private','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)

	var n int
	d.QueryRow(`SELECT COUNT(*) FROM items_fts WHERE items_fts MATCH 'raspberry'`).Scan(&n)
	if n != 1 {
		t.Fatalf("FTS match after insert = %d, want 1", n)
	}
	d.Exec(`UPDATE items SET name='orange pi' WHERE id=1`)
	d.QueryRow(`SELECT COUNT(*) FROM items_fts WHERE items_fts MATCH 'raspberry'`).Scan(&n)
	if n != 0 {
		t.Fatalf("FTS stale after update: %d matches, want 0", n)
	}
	d.QueryRow(`SELECT COUNT(*) FROM items_fts WHERE items_fts MATCH 'orange'`).Scan(&n)
	if n != 1 {
		t.Fatalf("FTS missing after update: %d, want 1", n)
	}
	d.Exec(`DELETE FROM items WHERE id=1`)
	d.QueryRow(`SELECT COUNT(*) FROM items_fts WHERE items_fts MATCH 'orange'`).Scan(&n)
	if n != 0 {
		t.Fatalf("FTS stale after delete: %d, want 0", n)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	d := newMigratedDB(t)
	_, err := d.Exec(`INSERT INTO items (user_id, name, qty, created_at, updated_at) VALUES (999,'x',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("expected FK violation inserting item for nonexistent user")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/`
Expected: FAIL — `Open`/`Migrate` undefined (also `go get` needed).

- [ ] **Step 3: Add dependencies**

```bash
go get modernc.org/sqlite@latest
go get github.com/pressly/goose/v3@latest
```

- [ ] **Step 4: Implement open.go**

`internal/db/open.go`:

```go
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/pressly/goose/v3"
)

// Open opens the SQLite database at path and applies required pragmas.
func Open(path string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// modernc sqlite: set pragmas via DSN-free execution; single writer conn avoids SQLITE_BUSY.
	d.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
	} {
		if _, err := d.Exec(pragma); err != nil {
			d.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return d, nil
}

// Migrate applies all embedded migrations.
func Migrate(d *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite"); err != nil {
		return err
	}
	return goose.Up(d, ".")
}
```

- [ ] **Step 5: Write migration files**

`internal/migrations/embed.go`:

```go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

`internal/db/open.go` references `migrationsFS` — add above `Migrate`:

```go
import "stasher/internal/migrations"

var migrationsFS = migrations.FS
```

(merge into the import block; shown separately here for clarity).

`internal/migrations/00001_init.sql`:

```sql
-- +goose Up
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    default_item_visibility TEXT NOT NULL DEFAULT 'private' CHECK (default_item_visibility IN ('private','shared')),
    is_admin INTEGER NOT NULL DEFAULT 0,
    disabled INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent TEXT NOT NULL DEFAULT '',
    last_ip TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE invites (
    id INTEGER PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    created_by INTEGER NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    used_by INTEGER REFERENCES users(id),
    used_at TEXT
);

CREATE TABLE locations (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    parent_id INTEGER REFERENCES locations(id) ON DELETE SET NULL,
    notes TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_locations_user_parent ON locations(user_id, parent_id);

CREATE TABLE items (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    qty NUMERIC NOT NULL DEFAULT 1 CHECK (qty >= 0),
    unit TEXT NOT NULL DEFAULT '',
    location_id INTEGER REFERENCES locations(id) ON DELETE SET NULL,
    visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','shared')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_items_user ON items(user_id);
CREATE INDEX idx_items_user_location ON items(user_id, location_id);

CREATE TABLE item_photos (
    id INTEGER PRIMARY KEY,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    caption TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_item_photos_item ON item_photos(item_id);
CREATE INDEX idx_item_photos_key ON item_photos(storage_key);

CREATE TABLE item_documents (
    id INTEGER PRIMARY KEY,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_item_documents_item ON item_documents(item_id);
CREATE INDEX idx_item_documents_key ON item_documents(storage_key);

CREATE TABLE tags (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    UNIQUE (user_id, name)
);

CREATE TABLE item_tags (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, tag_id)
);
CREATE INDEX idx_item_tags_tag ON item_tags(tag_id);

CREATE TABLE projects (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','active','done','archived')),
    notes TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_projects_user ON projects(user_id);

CREATE TABLE project_items (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    qty_used NUMERIC,
    notes TEXT NOT NULL DEFAULT '',
    added_at TEXT NOT NULL,
    PRIMARY KEY (project_id, item_id)
);
CREATE INDEX idx_project_items_item ON project_items(item_id);

CREATE VIRTUAL TABLE items_fts USING fts5(
    name, description, content='items', content_rowid='id'
);

CREATE TRIGGER items_ai AFTER INSERT ON items BEGIN
    INSERT INTO items_fts(rowid, name, description) VALUES (new.id, new.name, new.description);
END;
CREATE TRIGGER items_ad AFTER DELETE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, name, description)
        VALUES ('delete', old.id, old.name, old.description);
END;
CREATE TRIGGER items_au AFTER UPDATE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, name, description)
        VALUES ('delete', old.id, old.name, old.description);
    INSERT INTO items_fts(rowid, name, description) VALUES (new.id, new.name, new.description);
END;

-- +goose Down
DROP TRIGGER items_au;
DROP TRIGGER items_ad;
DROP TRIGGER items_ai;
DROP TABLE items_fts;
DROP TABLE project_items;
DROP TABLE projects;
DROP TABLE item_tags;
DROP TABLE tags;
DROP TABLE item_documents;
DROP TABLE item_photos;
DROP TABLE items;
DROP TABLE locations;
DROP TABLE invites;
DROP TABLE sessions;
DROP TABLE users;
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/db/ -v`
Expected: PASS (3 tests). Note: `locations.parent_id` uses `ON DELETE SET NULL` for orphans, but the delete handler (Task 11) re-parents children first and nulls items explicitly, so the cascade is only a safety net.

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "feat: sqlite open with pragmas and full initial migration"
```

---

### Task 3: sqlc setup + users/sessions/invites queries

**Files:**
- Create: `sqlc.yaml`, `internal/db/queries.sql`
- Modify: `go.mod` (sqlc as tool), generated files in `internal/db/`

**Interfaces:**
- Consumes: schema from Task 2.
- Produces: `db.New(dbtx db.DBTX) *db.Queries` plus these methods (used by later tasks):
  - `CreateUser(ctx, arg CreateUserParams) (User, error)`, `GetUserByEmail(ctx, email string) (User, error)`, `GetUserByID(ctx, id int64) (User, error)`, `CountUsers(ctx) (int64, error)`, `UpdateUserProfile(ctx, arg)`, `UpdateUserPassword(ctx, arg)`, `SetUserAdmin(ctx, arg)`, `SetUserDisabled(ctx, arg)`, `ListUsers(ctx) ([]ListUsersRow, error)`
  - `CreateSession(ctx, arg) (Session, error)`, `GetSessionWithUser(ctx, tokenHash string) (GetSessionWithUserRow, error)`, `ListUserSessions(ctx, userID int64) ([]Session, error)`, `DeleteSession(ctx, arg) error`, `DeleteOtherUserSessions(ctx, arg) error`, `DeleteAllUserSessions(ctx, userID int64) error`, `TouchSession(ctx, arg) error`
  - `CreateInvite(ctx, arg) (Invite, error)`, `GetInvite(ctx, code string) (Invite, error)`, `ListInvites(ctx) ([]ListInvitesRow, error)`, `MarkInviteUsed(ctx, arg) error`, `DeleteInvite(ctx, id int64) error`

- [ ] **Step 1: Install sqlc as a tool dependency**

```bash
go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```

`sqlc.yaml` (repo root):

```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "internal/db/queries.sql"
    schema: "internal/migrations"
    gen:
      go:
        package: "db"
        out: "internal/db"
        emit_json_tags: true
```

- [ ] **Step 2: Write queries and a failing smoke test**

Append to `internal/db/queries.sql` (this is the whole file for now):

```sql
-- name: CreateUser :one
INSERT INTO users (email, password_hash, display_name, default_item_visibility, is_admin, disabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 0, ?, ?)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: UpdateUserProfile :exec
UPDATE users SET display_name = ?, default_item_visibility = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?;

-- name: SetUserAdmin :exec
UPDATE users SET is_admin = ?, updated_at = ? WHERE id = ?;

-- name: SetUserDisabled :exec
UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?;

-- name: ListUsers :many
SELECT id, email, display_name, default_item_visibility, is_admin, disabled, created_at, updated_at FROM users ORDER BY id;

-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, user_agent, last_ip, created_at, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSessionWithUser :one
SELECT s.id AS session_id, s.token_hash, s.last_seen_at,
       u.id AS user_id, u.email, u.password_hash, u.display_name,
       u.default_item_visibility, u.is_admin, u.disabled
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ?;

-- name: ListUserSessions :many
SELECT * FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ? AND user_id = ?;

-- name: DeleteOtherUserSessions :exec
DELETE FROM sessions WHERE user_id = ? AND id != ?;

-- name: DeleteAllUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE id = ?;

-- name: CreateInvite :one
INSERT INTO invites (code, created_by, created_at) VALUES (?, ?, ?) RETURNING *;

-- name: GetInvite :one
SELECT * FROM invites WHERE code = ?;

-- name: ListInvites :many
SELECT i.id, i.code, i.created_at, i.used_by, i.used_at, u.email AS used_by_email
FROM invites i LEFT JOIN users u ON u.id = i.used_by ORDER BY i.id DESC;

-- name: MarkInviteUsed :exec
UPDATE invites SET used_by = ?, used_at = ? WHERE code = ?;

-- name: DeleteInvite :exec
DELETE FROM invites WHERE id = ?;
```

Smoke test `internal/db/queries_test.go`:

```go
package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestUserSessionInviteQueries(t *testing.T) {
	d := newMigratedTestDB(t)
	q := New(d)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	if n, err := q.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0", n, err)
	}

	u, err := q.CreateUser(ctx, CreateUserParams{
		Email: "a@b.c", PasswordHash: "h", DisplayName: "A",
		DefaultItemVisibility: "private", IsAdmin: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 || u.IsAdmin != true {
		t.Fatalf("created user = %+v", u)
	}

	s, err := q.CreateSession(ctx, CreateSessionParams{
		TokenHash: "th1", UserID: u.ID, UserAgent: "test", LastIP: "127.0.0.1",
		CreatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.GetSessionWithUser(ctx, "th1")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != u.ID || got.Disabled != false {
		t.Fatalf("session join = %+v", got)
	}
	if err := q.DeleteSession(ctx, DeleteSessionParams{ID: s.ID, UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetSessionWithUser(ctx, "th1"); err == nil {
		t.Fatal("deleted session still found")
	}

	inv, err := q.CreateInvite(ctx, CreateInviteParams{Code: "ABCD2345", CreatedBy: u.ID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.MarkInviteUsed(ctx, MarkInviteUsedParams{UsedBy: u.ID, UsedAt: now, Code: "ABCD2345"}); err != nil {
		t.Fatal(err)
	}
	gotInv, err := q.GetInvite(ctx, "ABCD2345")
	if err != nil || gotInv.UsedBy.Int64 != u.ID || inv.Code != "ABCD2345" {
		t.Fatalf("invite = %+v err=%v", gotInv, err)
	}
}
```

In this file, reference `newMigratedDB` from `open_test.go` via a one-line alias so the sqlc type matches:

```go
func newMigratedTestDB(t *testing.T) DBTX {
	t.Helper()
	return newMigratedDB(t)
}
```

(sqlc generates `type DBTX interface{...}` and `New(db DBTX) *Queries`.)

- [ ] **Step 3: Run to verify it fails**

Run: `go generate ./... 2>/dev/null; go tool sqlc generate; go test ./internal/db/`
Expected: compile FAIL — `New` undefined before generation; after generation PASS.

- [ ] **Step 4: Generate and verify**

Run: `go tool sqlc generate && go test ./internal/db/ -v`
Expected: PASS. Inspect `internal/db/models.go` for `User`, `Session`, `Invite` and `db.go` for `Queries`.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: sqlc setup with users, sessions, invites queries"
```

---

### Task 4: Auth core — Argon2id passwords, session tokens, cookies

**Files:**
- Create: `internal/auth/password.go`, `internal/auth/password_test.go`, `internal/auth/session.go`, `internal/auth/session_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `auth.HashPassword(password string) (string, error)`, `auth.VerifyPassword(hash, password string) bool`, `auth.NewSessionToken() (raw, hash string)`, `auth.HashToken(raw string) string`, `auth.CookieName` (`"stasher_session"`), `auth.SessionCookie(raw string) *http.Cookie`, `auth.ClearSessionCookie() *http.Cookie`.

- [ ] **Step 1: Write the failing tests**

`internal/auth/password_test.go`:

```go
package auth

import "testing"

func TestHashVerifyRoundtrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Fatal("correct password failed to verify")
	}
	if VerifyPassword(h, "wrong password") {
		t.Fatal("wrong password verified")
	}
}

func TestHashFormat(t *testing.T) {
	h, _ := HashPassword("x")
	want := "$argon2id$v=19$m=65536,t=3,p=4$"
	if len(h) < len(want) || h[:len(want)] != want {
		t.Fatalf("hash prefix = %q, want %q...", h, want)
	}
}

func TestVerifyMalformedHash(t *testing.T) {
	for _, h := range []string{"", "plaintext", "$argon2id$v=19$", "$argon2id$v=19$m=65536,t=3,p=4$notb64$notb64"} {
		if VerifyPassword(h, "x") {
			t.Errorf("malformed hash %q verified", h)
		}
	}
}
```

`internal/auth/session_test.go`:

```go
package auth

import (
	"net/http"
	"testing"
)

func TestNewSessionTokenRoundtrip(t *testing.T) {
	raw, hash := NewSessionToken()
	if raw == "" || hash == "" || raw == hash {
		t.Fatalf("raw=%q hash=%q", raw, hash)
	}
	if HashToken(raw) != hash {
		t.Fatal("HashToken(raw) != hash")
	}
	raw2, _ := NewSessionToken()
	if raw2 == raw {
		t.Fatal("tokens are not unique")
	}
}

func TestSessionCookieFields(t *testing.T) {
	c := SessionCookie("abc")
	if c.Name != CookieName || c.Value != "abc" || c.Path != "/" ||
		!c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 315360000 {
		t.Fatalf("cookie = %+v", c)
	}
	d := ClearSessionCookie()
	if d.MaxAge != -1 || d.Value != "" {
		t.Fatalf("clear cookie = %+v", d)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/auth/`
Expected: FAIL — functions undefined. Run `go get golang.org/x/crypto@latest` first.

- [ ] **Step 3: Implement**

`internal/auth/password.go`:

```go
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns an encoded Argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the encoded hash.
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != 19 {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
```

`internal/auth/session.go`:

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
)

// CookieName is the session cookie name.
const CookieName = "stasher_session"

// NewSessionToken returns the raw token (sent to the client in a cookie) and
// its SHA-256 hex hash (the only thing stored in the database).
func NewSessionToken() (raw, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw)
}

// HashToken maps a raw token to its stored hash.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// SessionCookie returns the 10-year persistent session cookie.
func SessionCookie(raw string) *http.Cookie {
	return &http.Cookie{
		Name: CookieName, Value: raw, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: 315360000,
	}
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name: CookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	}
}
```

Note: `Secure: true` works behind Caddy (HTTPS). Browsers also accept Secure cookies on `http://localhost`, so local dev is unaffected.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/auth/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: argon2id password hashing and session tokens"
```

---

### Task 5: File storage package

**Files:**
- Create: `internal/files/storage.go`, `internal/files/storage_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `files.New(root string) (*Storage, error)`, `(*Storage).Save(r io.Reader, ext string) (key string, size int64, err error)`, `(*Storage).Open(key string) (*os.File, error)`, `(*Storage).Copy(key string) (newKey string, err error)`, `(*Storage).Delete(key string) error`, `files.ValidKey(key string) bool`. Keys look like `2026/10/<22-char-base64url>.<ext>`.

- [ ] **Step 1: Write the failing test**

`internal/files/storage_test.go`:

```go
package files

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStorage(t *testing.T) *Storage {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveOpenRoundtrip(t *testing.T) {
	s := newStorage(t)
	key, size, err := s.Save(strings.NewReader("hello world"), "txt")
	if err != nil {
		t.Fatal(err)
	}
	if size != 11 || !ValidKey(key) {
		t.Fatalf("key=%q size=%d", key, size)
	}
	f, err := s.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "hello world" {
		t.Fatalf("read back %q", b)
	}
	// file must live under root
	abs, _ := filepath.Abs(f.Name())
	rootAbs, _ := filepath.Abs(s.root)
	if !strings.HasPrefix(abs, rootAbs+string(os.PathSeparator)) {
		t.Fatalf("file %s escaped root %s", abs, rootAbs)
	}
}

func TestOpenRejectsBadKeys(t *testing.T) {
	s := newStorage(t)
	for _, key := range []string{
		"../../etc/passwd", "../secrets.txt", "/abs/path.png",
		"2026/10/ok.png/../../../x", "noext", "2026/10/x.sh.exe.png.exe.exe",
		"", "2026/10/.png",
	} {
		if ValidKey(key) {
			t.Errorf("ValidKey(%q) = true", key)
		}
		if _, err := s.Open(key); err == nil {
			t.Errorf("Open(%q) succeeded, want error", key)
		}
	}
}

func TestCopyDuplicatesContent(t *testing.T) {
	s := newStorage(t)
	key, _, _ := s.Save(strings.NewReader("doc-bytes"), "pdf")
	key2, err := s.Copy(key)
	if err != nil {
		t.Fatal(err)
	}
	if key2 == key {
		t.Fatal("copy returned same key")
	}
	f, _ := s.Open(key2)
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "doc-bytes" {
		t.Fatalf("copy content %q", b)
	}
	s.Delete(key)
	f2, err := s.Open(key2)
	if err != nil {
		t.Fatal("copy died with original")
	}
	f2.Close()
}

func TestDeleteIdempotent(t *testing.T) {
	s := newStorage(t)
	key, _, _ := s.Save(strings.NewReader("x"), "png")
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if err := s.Delete("2026/10/nothere.png"); err != nil {
		t.Fatalf("delete of invalid-key missing file: %v", err)
	}
}
```

Delete nothing — the test above is final.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/files/`
Expected: FAIL — package has no `Storage`.

- [ ] **Step 3: Implement**

`internal/files/storage.go`:

```go
// Package files stores uploaded photos and documents on disk under a root
// directory, sharded yyyy/mm. Storage keys never trust client input.
package files

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var keyRe = regexp.MustCompile(`^\d{4}/\d{2}/[A-Za-z0-9_-]{20,30}\.[A-Za-z0-9]{1,8}$`)

// ValidKey reports whether key is a well-formed storage key.
func ValidKey(key string) bool { return keyRe.MatchString(key) }

type Storage struct{ root string }

// New creates the root directory and returns a Storage.
func New(root string) (*Storage, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Storage{root: root}, nil
}

// Save writes r to a fresh key with the given extension and returns the key
// and number of bytes written.
func (s *Storage) Save(r io.Reader, ext string) (string, int64, error) {
	key := s.newKey(ext)
	p := s.pathFor(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}
	f, err := os.Create(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		os.Remove(p)
		return "", 0, err
	}
	return key, n, nil
}

// Open opens the file for key; invalid or missing keys return an error.
func (s *Storage) Open(key string) (*os.File, error) {
	if !ValidKey(key) {
		return nil, os.ErrNotExist
	}
	return os.Open(s.pathFor(key))
}

// Copy duplicates the file at key under a new key.
func (s *Storage) Copy(key string) (string, error) {
	f, err := s.Open(key)
	if err != nil {
		return "", err
	}
	defer f.Close()
	newKey, _, err := s.Save(f, strings.TrimPrefix(path.Ext(key), "."))
	return newKey, err
}

// Delete removes the file at key; missing files are not an error.
func (s *Storage) Delete(key string) error {
	if !ValidKey(key) {
		return nil
	}
	err := os.Remove(s.pathFor(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Storage) newKey(ext string) string {
	ext = strings.TrimPrefix(path.Ext(ext), ".") // tolerate ".png" or "png"
	if ext == "" || len(ext) > 8 {
		ext = "bin"
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	now := time.Now().UTC()
	return fmt.Sprintf("%04d/%02d/%s.%s", now.Year(), int(now.Month()),
		base64.RawURLEncoding.EncodeToString(b), strings.ToLower(ext))
}

func (s *Storage) pathFor(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}
```

The `keyRe` length range `20,30` matches 16 random bytes in raw base64url (22 chars) — do not loosen it; traversal strings like `../x` cannot match.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/files/ -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: sharded file storage with key validation"
```

---

### Task 6: API scaffolding — server, responses, middleware, /auth/me

**Files:**
- Create: `internal/api/server.go`, `internal/api/respond.go`, `internal/api/middleware.go`, `internal/api/testutil_test.go`, `internal/api/server_test.go`
- Modify: `go.mod` (`go get github.com/go-chi/chi/v5@latest`)

**Interfaces:**
- Consumes: `config.Config`, `db.Queries` (Task 3), `files.Storage` (Task 5), `auth.CookieName/HashToken/NewSessionToken` (Task 4).
- Produces: `api.New(cfg *config.Config, q *db.Queries, st *files.Storage) *Server`; `(*Server).Router() chi.Router`; `writeJSON(w, status, v)`, `writeErr(w, status, code, msg)`, `decodeJSON(r, dst) error`, `listEnvelope{Data any, Total int64}`, `parsePage(r) pageParams` with `Limit()/Offset()`; `currentUser(ctx) (db.User, bool)`, `currentSessionID(ctx) (int64, bool)`; middleware `requireAuth`, `requireAdmin`, `logging`. Route `GET /api/v1/auth/me` → `{id, email, display_name, default_item_visibility, is_admin}`. Test helpers: `newTestServer(t, mutate func(*config.Config)) (*Server, *httptest.Server)`, `doJSON(t, ts, method, path, body, cookie) *http.Response`, `registerUser`, `loginUser`.

- [ ] **Step 1: Write the failing test**

`internal/api/server_test.go`:

```go
package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"stasher/internal/auth"
)

func TestHealthz(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp, _ := http.Get(ts.URL + "/healthz")
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}
}

func TestRequireAuthAndMe(t *testing.T) {
	s, ts := newTestServer(t, nil)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	u, err := s.q.CreateUser(ctx, createUserParams("me@test.dev", "display", now))
	if err != nil {
		t.Fatal(err)
	}
	raw, hash := auth.NewSessionToken()
	if _, err := s.q.CreateSession(ctx, sessionParams(hash, u.ID, now)); err != nil {
		t.Fatal(err)
	}

	// no cookie -> 401
	resp, _ := http.Get(ts.URL + "/api/v1/auth/me")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me without cookie = %d, want 401", resp.StatusCode)
	}

	// bad cookie -> 401
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "forged"})
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Fatalf("me with forged cookie = %d, want 401", resp.StatusCode)
	}

	// good cookie -> 200 with profile
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: raw})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var me struct {
		Email    string `json:"email"`
		IsAdmin  bool   `json:"is_admin"`
		Disabled bool   `json:"-"`
	}
	decodeBody(t, resp, &me)
	if me.Email != "me@test.dev" || me.IsAdmin {
		t.Fatalf("me = %+v", me)
	}
}
```

Add the small helpers this test uses to `testutil_test.go` in Step 3 (they are part of the test scaffolding, not production code):

```go
func createUserParams(email, name, now string) db.CreateUserParams {
	return db.CreateUserParams{
		Email: email, PasswordHash: "x", DisplayName: name,
		DefaultItemVisibility: "private", CreatedAt: now, UpdatedAt: now,
	}
}

func sessionParams(hash string, userID int64, now string) db.CreateSessionParams {
	return db.CreateSessionParams{
		TokenHash: hash, UserID: userID, UserAgent: "test", LastIP: "127.0.0.1",
		CreatedAt: now, LastSeenAt: now,
	}
}
```

and `decodeBody(t, resp, dst)`:

```go
func decodeBody(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/`
Expected: FAIL — `newTestServer` etc. undefined.

- [ ] **Step 3: Implement scaffolding**

`internal/api/respond.go`:

```go
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

type errDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errBody struct {
	Error errDetail `json:"error"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errBody{Error: errDetail{Code: code, Message: msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return false
	}
	return true
}

type listEnvelope struct {
	Data  any   `json:"data"`
	Total int64 `json:"total"`
}

type pageParams struct {
	Page     int64
	PageSize int64
}

func parsePage(r *http.Request) pageParams {
	p := pageParams{Page: 1, PageSize: 50}
	if v, err := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64); err == nil && v > 0 {
		p.Page = v
	}
	if v, err := strconv.ParseInt(r.URL.Query().Get("page_size"), 10, 64); err == nil && v > 0 {
		p.PageSize = v
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
	return p
}

func (p pageParams) Limit() int64  { return p.PageSize }
func (p pageParams) Offset() int64 { return (p.Page - 1) * p.PageSize }
```

`internal/api/middleware.go`:

```go
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"stasher/internal/auth"
	dbpkg "stasher/internal/db"
)

type ctxKey int

const (
	userKey ctxKey = iota + 1
	sessionIDKey
)

func currentUser(ctx context.Context) (dbpkg.User, bool) {
	u, ok := ctx.Value(userKey).(dbpkg.User)
	return u, ok
}

func currentSessionID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(sessionIDKey).(int64)
	return id, ok
}

// requireAuth loads the session cookie, validates it server-side, and stores
// the user + session id in the request context.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		row, err := s.q.GetSessionWithUser(r.Context(), auth.HashToken(c.Value))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid session")
			return
		}
		if row.Disabled {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "account disabled")
			return
		}
		if err := s.q.TouchSession(r.Context(), dbpkg.TouchSessionParams{
			LastSeenAt: time.Now().UTC().Format(time.RFC3339), ID: row.SessionID,
		}); err != nil {
			slog.Warn("touch session failed", "err", err)
		}
		u := dbpkg.User{
			ID: row.UserID, Email: row.Email, DisplayName: row.DisplayName,
			DefaultItemVisibility: row.DefaultItemVisibility,
			IsAdmin:               row.IsAdmin, Disabled: row.Disabled,
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, sessionIDKey, row.SessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin requires an authenticated admin user.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := currentUser(r.Context())
		if !ok || !u.IsAdmin {
			writeErr(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return middleware.Logger(next)
}
```

`internal/api/server.go`:

```go
package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"stasher/internal/config"
	dbpkg "stasher/internal/db"
	"stasher/internal/files"
)

// Server holds application dependencies for all handlers.
type Server struct {
	cfg *config.Config
	q   *dbpkg.Queries
	st  *files.Storage
	raw *sql.DB // for transactions and test queries; same pool as q
}

// New wires the server. This signature is final for the whole plan.
func New(cfg *config.Config, raw *sql.DB, st *files.Storage) *Server {
	return &Server{cfg: cfg, q: dbpkg.New(raw), st: st, raw: raw}
}

// SetSPAFallback installs the handler used for unmatched non-API routes
// (main passes the embedded SPA index handler in Task 18).
func (s *Server) SetSPAFallback(h http.HandlerFunc) { s.spaFallback = h }

// Router builds the complete application router: /healthz, /api/v1/*, /files/*.
func (s *Server) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(s.logging)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	r.Route("/api/v1", func(r chi.Router) {
		// Public auth endpoints (added in Task 7): register, login.

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/auth/me", s.handleMe)
			// Protected endpoints from later tasks register here.
		})
	})

	// /files/* route added in Task 15.

	if s.spaFallback != nil {
		r.NotFound(s.spaFallback)
	}
	return r
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	writeJSON(w, http.StatusOK, userJSONFrom(u))
}

func userJSONFrom(u dbpkg.User) map[string]any {
	return map[string]any{
		"id": u.ID, "email": u.Email, "display_name": u.DisplayName,
		"default_item_visibility": u.DefaultItemVisibility,
		"is_admin":                u.IsAdmin,
	}
}
```

(The `spaFallback` field is `spaFallback http.HandlerFunc` — add it to the `Server` struct.)

`internal/api/testutil_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"stasher/internal/config"
	"stasher/internal/db"
	"stasher/internal/files"
)

// newTestServer returns a Server backed by a fresh temp SQLite DB and a test
// HTTP server running the real router. mutate (optional) adjusts config before
// construction (e.g. switch Registration to "invite").
func newTestServer(t *testing.T, mutate func(*config.Config)) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}
	st, err := files.New(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		ListenAddr: "", BaseURL: tsBaseURL, DataDir: dir,
		Registration: "open", LogLevel: "warn",
	}
	if mutate != nil {
		mutate(cfg)
	}
	s := New(cfg, sqlDB, st)
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	return s, ts
}

const tsBaseURL = "http://test.invalid"

// doJSON performs a request with an optional JSON body and optional cookie.
func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any, cookie *http.Cookie) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// registerUser creates a user via POST /api/v1/auth/register and returns the cookie.
func registerUser(t *testing.T, ts *httptest.Server, email, password string) *http.Cookie {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": email, "password": password, "display_name": "Test"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d", resp.StatusCode)
	}
	return sessionCookieFrom(resp)
}

func sessionCookieFrom(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "stasher_session" {
			return c
		}
	}
	return nil
}

func decodeBody(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
```

`doJSON` needs `"bytes"` and `"io"` imports — add them.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go get github.com/go-chi/chi/v5@latest && go test ./internal/api/ -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: api server scaffolding with auth middleware"
```

---

### Task 7: Register, login, logout

**Files:**
- Create: `internal/api/auth_handlers.go`, `internal/api/auth_handlers_test.go`

**Interfaces:**
- Consumes: `db.Queries` user/session/invite methods (Task 3), `auth.HashPassword/VerifyPassword/NewSessionToken/SessionCookie/ClearSessionCookie` (Task 4), `Server.Router` groups (Task 6).
- Produces: routes `POST /api/v1/auth/register`, `POST /api/v1/auth/login`, `POST /api/v1/auth/logout` (protected). Register/login auto-create a session and set the cookie; responses are the user JSON from `userJSONFrom`.

- [ ] **Step 1: Write the failing tests**

The whole test file, `internal/api/auth_handlers_test.go`:

```go
package api

import (
	"context"
	"net/http"
	"testing"

	"stasher/internal/config"
	"stasher/internal/db"
)

func TestRegisterOpenMode(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "First@Test.Dev", "password": "longpassword1", "display_name": "First"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d, want 201", resp.StatusCode)
	}
	var body struct {
		Email   string `json:"email"`
		IsAdmin bool   `json:"is_admin"`
	}
	decodeBody(t, resp, &body)
	if body.Email != "first@test.dev" {
		t.Errorf("email not lowercased: %q", body.Email)
	}
	if !body.IsAdmin { // first user is admin (bootstrap rule)
		t.Errorf("first user should be admin")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	_, ts := newTestServer(t, nil)
	registerUser(t, ts, "dup@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "dup@test.dev", "password": "longpassword1", "display_name": "Dup"}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup register = %d, want 409", resp.StatusCode)
	}
}

func TestRegisterInviteMode(t *testing.T) {
	s, ts := newTestServer(t, func(c *config.Config) { c.Registration = "invite" })
	ctx, now := context.Background(), timeNow(t)

	admin, err := s.q.CreateUser(ctx, createUserParams("admin@test.dev", "Admin", now))
	if err != nil {
		t.Fatal(err)
	}
	invite, err := s.q.CreateInvite(ctx, db.CreateInviteParams{Code: "TESTCODE1", CreatedBy: admin.ID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = invite

	// no code -> 400
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "a@test.dev", "password": "longpassword1", "display_name": "A"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("register without invite = %d, want 400", resp.StatusCode)
	}
	// bad code -> 400
	resp = doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "a@test.dev", "password": "longpassword1", "display_name": "A", "invite_code": "WRONG"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("register wrong invite = %d, want 400", resp.StatusCode)
	}
	// good code -> 201, non-admin
	resp = doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "a@test.dev", "password": "longpassword1", "display_name": "A", "invite_code": "TESTCODE1"}, nil)
	var body struct {
		IsAdmin bool `json:"is_admin"`
	}
	decodeBody(t, resp, &body)
	if resp.StatusCode != http.StatusCreated || body.IsAdmin {
		t.Fatalf("register with invite = %d admin=%v, want 201 admin=false", resp.StatusCode, body.IsAdmin)
	}
	// code reuse -> 400
	resp = doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "b@test.dev", "password": "longpassword1", "display_name": "B", "invite_code": "TESTCODE1"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reuse invite = %d, want 400", resp.StatusCode)
	}

	// BUT: bootstrap rule — on an empty DB the first user needs no code. Covered
	// by TestRegisterOpenMode style: fresh server, invite mode, no users.
	_, ts2 := newTestServer(t, func(c *config.Config) { c.Registration = "invite" })
	resp = doJSON(t, ts2, "POST", "/api/v1/auth/register",
		map[string]any{"email": "first@test.dev", "password": "longpassword1", "display_name": "F"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap register in invite mode = %d, want 201", resp.StatusCode)
	}
}

func TestRegisterValidation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cases := []map[string]any{
		{"email": "notanemail", "password": "longpassword1", "display_name": "X"},
		{"email": "x@y.dev", "password": "short", "display_name": "X"},
		{"email": "x@y.dev", "password": "longpassword1", "display_name": ""},
	}
	for _, c := range cases {
		if resp := doJSON(t, ts, "POST", "/api/v1/auth/register", c, nil); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("register %v = %d, want 400", c, resp.StatusCode)
		}
	}
}

func TestLoginLogout(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "login@test.dev", "longpassword1")

	// login with right password
	resp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "login@test.dev", "password": "longpassword1"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	bad := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "login@test.dev", "password": "wrongpassword"}, nil)
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", bad.StatusCode)
	}

	// logout revokes server-side; replayed cookie must fail (Review Focus #3)
	logout := doJSON(t, ts, "POST", "/api/v1/auth/logout", nil, cookie)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d", logout.StatusCode)
	}
	replay := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, cookie)
	if replay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed cookie after logout = %d, want 401", replay.StatusCode)
	}
}

func TestLoginDisabledUser(t *testing.T) {
	s, ts := newTestServer(t, nil)
	registerUser(t, ts, "dis@test.dev", "longpassword1")
	s.q.SetUserDisabled(context.Background(), db.SetUserDisabledParams{Disabled: true, UpdatedAt: timeNow(t), ID: s.lastUserID(t)})
	resp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "dis@test.dev", "password": "longpassword1"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled login = %d, want 403", resp.StatusCode)
	}
}
```

Helpers used (add to `testutil_test.go`):

```go
func timeNow(t *testing.T) string {
	t.Helper()
	return time.Now().UTC().Format(time.RFC3339)
}

// lastUserID returns the highest user id (test convenience).
func (s *Server) lastUserID(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := s.raw.QueryRow("SELECT MAX(id) FROM users").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
```

(`lastUserID` uses the `raw *sql.DB` field from Task 6; `timeNow` needs the `time` import in `testutil_test.go`.)

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/api/`
Expected: FAIL — routes return chi 404.

- [ ] **Step 3: Implement handlers**

`internal/api/auth_handlers.go`:

```go
package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"stasher/internal/auth"
	dbpkg "stasher/internal/db"
)

type registerReq struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DisplayName string `json:"display_name"`
	InviteCode string `json:"invite_code"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if !strings.Contains(req.Email, "@") || len(req.Email) > 254 ||
		len(req.Password) < 10 || req.DisplayName == "" || len(req.DisplayName) > 100 {
		writeErr(w, http.StatusBadRequest, "validation_error",
			"email must be valid, password at least 10 chars, display name 1-100 chars")
		return
	}

	count, err := s.q.CountUsers(r.Context())
	if err != nil {
		s.internalErr(w, err)
		return
	}
	bootstrap := count == 0 // first user: admin, no invite needed

	if !bootstrap && s.cfg.Registration == "invite" {
		if req.InviteCode == "" {
			writeErr(w, http.StatusBadRequest, "invite_required", "an invite code is required")
			return
		}
		inv, err := s.q.GetInvite(r.Context(), req.InviteCode)
		if err != nil || inv.UsedBy.Valid {
			writeErr(w, http.StatusBadRequest, "invalid_invite", "invite code is invalid or already used")
			return
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u, err := s.q.CreateUser(r.Context(), dbpkg.CreateUserParams{
		Email: req.Email, PasswordHash: hash, DisplayName: req.DisplayName,
		DefaultItemVisibility: "private", IsAdmin: bootstrap, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
			writeErr(w, http.StatusConflict, "email_taken", "an account with this email already exists")
			return
		}
		s.internalErr(w, err)
		return
	}
	if !bootstrap && s.cfg.Registration == "invite" {
		if err := s.q.MarkInviteUsed(r.Context(), dbpkg.MarkInviteUsedParams{
			UsedBy: sqlNullInt64(u.ID), UsedAt: sqlNullString(now), Code: req.InviteCode,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	s.startSession(w, r, u)
	writeJSON(w, http.StatusCreated, userJSONFrom(u))
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	u, err := s.q.GetUserByEmail(r.Context(), req.Email)
	if err != nil || !auth.VerifyPassword(u.PasswordHash, req.Password) {
		// Same error for unknown email and bad password (no account probing).
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if u.Disabled {
		writeErr(w, http.StatusForbidden, "account_disabled", "this account is disabled")
		return
	}
	s.startSession(w, r, u)
	writeJSON(w, http.StatusOK, userJSONFrom(u))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sid, ok := currentSessionID(r.Context()); ok {
		if u, ok := currentUser(r.Context()); ok {
			if err := s.q.DeleteSession(r.Context(), dbpkg.DeleteSessionParams{ID: sid, UserID: u.ID}); err != nil {
				s.internalErr(w, err)
				return
			}
		}
	}
	http.SetCookie(w, auth.ClearSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

// startSession creates a session row and sets the cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u dbpkg.User) {
	raw, hash := auth.NewSessionToken()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.q.CreateSession(r.Context(), dbpkg.CreateSessionParams{
		TokenHash: hash, UserID: u.ID,
		UserAgent: r.UserAgent(), LastIP: clientIP(r),
		CreatedAt: now, LastSeenAt: now,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	http.SetCookie(w, auth.SessionCookie(raw))
}

func clientIP(r *http.Request) string {
	// Caddy adds X-Forwarded-For; take the first entry.
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	return r.RemoteAddr
}

func (s *Server) internalErr(w http.ResponseWriter, err error) {
	slogError("internal error", err)
	writeErr(w, http.StatusInternalServerError, "internal", "internal server error")
}
```

Small helpers referenced above — put in `respond.go`:

```go
func slogError(msg string, err error) { slog.Error(msg, "err", err) }

func sqlNullInt64(i int64) sql.NullInt64 { return sql.NullInt64{Int64: i, Valid: true} }
func sqlNullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
```

(with `"database/sql"`, `"log/slog"` imports added there). Register routes in `Server.Router()` inside the `/api/v1` block, before the authed group:

```go
r.Post("/auth/register", s.handleRegister)
r.Post("/auth/login", s.handleLogin)
```

and inside the authed group:

```go
r.Post("/auth/logout", s.handleLogout)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS (all auth + earlier tests).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: register, login, logout with invite codes"
```

---

### Task 8: Profile, password change, devices (session management)

**Files:**
- Create: `internal/api/profile_handlers.go`, `internal/api/profile_handlers_test.go`
- Modify: `internal/db/queries.sql` (+1 query), `internal/api/server.go` (routes)

**Interfaces:**
- Consumes: everything from Tasks 6–7.
- Produces: routes `PATCH /api/v1/auth/me`, `POST /api/v1/auth/password`, `GET /api/v1/auth/sessions`, `DELETE /api/v1/auth/sessions/{id}`, `POST /api/v1/auth/sessions/revoke-all`. New query `GetSessionByID(ctx, GetSessionByIDParams) (Session, error)`.

- [ ] **Step 1: Write the failing test**

`internal/api/profile_handlers_test.go`:

```go
package api

import (
	"net/http"
	"testing"
)

func TestProfileUpdate(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "prof@test.dev", "longpassword1")
	resp := doJSON(t, ts, "PATCH", "/api/v1/auth/me",
		map[string]any{"display_name": "NewName", "default_item_visibility": "shared"}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch me = %d", resp.StatusCode)
	}
	var me struct {
		DisplayName            string `json:"display_name"`
		DefaultItemVisibility string `json:"default_item_visibility"`
	}
	decodeBody(t, resp, &me)
	if me.DisplayName != "NewName" || me.DefaultItemVisibility != "shared" {
		t.Fatalf("me = %+v", me)
	}
}

func TestPasswordChangeRevokesOthers(t *testing.T) {
	_, ts := newTestServer(t, nil)
	registerUser(t, ts, "pw@test.dev", "longpassword1")
	other := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "pw@test.dev", "password": "longpassword1"}, nil)
	otherCookie := sessionCookieFrom(other)

	// current session cookie
	loginResp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "pw@test.dev", "password": "longpassword1"}, nil)
	current := sessionCookieFrom(loginResp)

	resp := doJSON(t, ts, "POST", "/api/v1/auth/password",
		map[string]any{"current_password": "longpassword1", "new_password": "newlongpassword", "revoke_other_sessions": true}, current)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("password change = %d", resp.StatusCode)
	}
	// other session dead, current alive
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, otherCookie); r.StatusCode != 401 {
		t.Fatalf("other session = %d, want 401", r.StatusCode)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, current); r.StatusCode != 200 {
		t.Fatalf("current session = %d, want 200", r.StatusCode)
	}
	// old password no longer works
	if r := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "pw@test.dev", "password": "longpassword1"}, nil); r.StatusCode != 401 {
		t.Fatalf("old password login = %d, want 401", r.StatusCode)
	}
	// wrong current password rejected
	if r := doJSON(t, ts, "POST", "/api/v1/auth/password",
		map[string]any{"current_password": "nope", "new_password": "anotherlongpass"}, current); r.StatusCode != 400 {
		t.Fatalf("wrong current pw = %d, want 400", r.StatusCode)
	}
}

func TestDeviceListAndRevoke(t *testing.T) {
	_, ts := newTestServer(t, nil)
	phone := registerUser(t, ts, "dev@test.dev", "longpassword1")
	desktop := sessionCookieFrom(doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "dev@test.dev", "password": "longpassword1"}, nil))

	// list from phone: two sessions, phone's is current
	resp := doJSON(t, ts, "GET", "/api/v1/auth/sessions", nil, phone)
	var list struct {
		Data []struct {
			ID      int64 `json:"id"`
			Current bool  `json:"current"`
		} `json:"data"`
	}
	decodeBody(t, resp, &list)
	if len(list.Data) != 2 {
		t.Fatalf("sessions = %d, want 2", len(list.Data))
	}
	currents := 0
	for _, s := range list.Data {
		if s.Current {
			currents++
		}
	}
	if currents != 1 {
		t.Fatalf("current flags = %d, want 1", currents)
	}

	// revoke desktop (its id is the non-current one)
	var desktopID int64
	for _, s := range list.Data {
		if !s.Current {
			desktopID = s.ID
		}
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/auth/sessions/"+fmtInt64(desktopID), nil, phone); r.StatusCode != 204 {
		t.Fatalf("revoke = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, desktop); r.StatusCode != 401 {
		t.Fatalf("revoked session = %d, want 401", r.StatusCode)
	}

	// revoke-all except current kills the rest (none left) and keeps phone
	if r := doJSON(t, ts, "POST", "/api/v1/auth/sessions/revoke-all",
		map[string]any{"except_current": true}, phone); r.StatusCode != 204 {
		t.Fatalf("revoke-all = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, phone); r.StatusCode != 200 {
		t.Fatalf("phone after revoke-all = %d, want 200", r.StatusCode)
	}
}
```

`fmtInt64` helper for `testutil_test.go`:

```go
func fmtInt64(i int64) string { return strconv.FormatInt(i, 10) }
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'Profile|Password|Device'`
Expected: FAIL — routes 404.

- [ ] **Step 3: Add query and implement**

Add to `internal/db/queries.sql`:

```sql
-- name: GetSessionByID :one
SELECT * FROM sessions WHERE id = ? AND user_id = ?;
```

Run `go tool sqlc generate`.

`internal/api/profile_handlers.go`:

```go
package api

import (
	"net/http"
	"time"

	"stasher/internal/auth"
	dbpkg "stasher/internal/db"
)

type profileReq struct {
	DisplayName            *string `json:"display_name"`
	DefaultItemVisibility *string `json:"default_item_visibility"`
}

func (s *Server) handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req profileReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DisplayName != nil {
		if len(*req.DisplayName) < 1 || len(*req.DisplayName) > 100 {
			writeErr(w, http.StatusBadRequest, "validation_error", "display name must be 1-100 chars")
			return
		}
		u.DisplayName = *req.DisplayName
	}
	if req.DefaultItemVisibility != nil {
		if *req.DefaultItemVisibility != "private" && *req.DefaultItemVisibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		u.DefaultItemVisibility = *req.DefaultItemVisibility
	}
	if err := s.q.UpdateUserProfile(r.Context(), dbpkg.UpdateUserProfileParams{
		DisplayName: u.DisplayName, DefaultItemVisibility: u.DefaultItemVisibility,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userJSONFrom(u))
}

type passwordReq struct {
	CurrentPassword    string `json:"current_password"`
	NewPassword        string `json:"new_password"`
	RevokeOtherSessions bool  `json:"revoke_other_sessions"`
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req passwordReq
	if !decodeJSON(w, r, &req) {
		return
	}
	full, err := s.q.GetUserByID(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if !auth.VerifyPassword(full.PasswordHash, req.CurrentPassword) {
		writeErr(w, http.StatusBadRequest, "invalid_current_password", "current password is incorrect")
		return
	}
	if len(req.NewPassword) < 10 {
		writeErr(w, http.StatusBadRequest, "validation_error", "new password must be at least 10 chars")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if err := s.q.UpdateUserPassword(r.Context(), dbpkg.UpdateUserPasswordParams{
		PasswordHash: hash, UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if req.RevokeOtherSessions {
		sid, _ := currentSessionID(r.Context())
		if err := s.q.DeleteOtherUserSessions(r.Context(), dbpkg.DeleteOtherUserSessionsParams{
			UserID: u.ID, ID: sid,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	sid, _ := currentSessionID(r.Context())
	rows, err := s.q.ListUserSessions(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	curSid := sid
	out := make([]map[string]any, 0, len(rows))
	for _, sess := range rows {
		out = append(out, map[string]any{
			"id": sess.ID, "user_agent": sess.UserAgent, "last_ip": sess.LastIP,
			"created_at": sess.CreatedAt, "last_seen_at": sess.LastSeenAt,
			"current": sess.ID == curSid,
		})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid session id")
		return
	}
	if _, err := s.q.GetSessionByID(r.Context(), dbpkg.GetSessionByIDParams{ID: id, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if err := s.q.DeleteSession(r.Context(), dbpkg.DeleteSessionParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type revokeAllReq struct {
	ExceptCurrent bool `json:"except_current"`
}

func (s *Server) handleRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req revokeAllReq
	if !decodeJSON(w, r, &req) {
		return
	}
	var err error
	if req.ExceptCurrent {
		sid, _ := currentSessionID(r.Context())
		err = s.q.DeleteOtherUserSessions(r.Context(), dbpkg.DeleteOtherUserSessionsParams{UserID: u.ID, ID: sid})
	} else {
		err = s.q.DeleteAllUserSessions(r.Context(), u.ID)
		http.SetCookie(w, auth.ClearSessionCookie())
	}
	if err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Routes in the authed group:

```go
r.Patch("/auth/me", s.handleProfileUpdate)
r.Post("/auth/password", s.handlePasswordChange)
r.Get("/auth/sessions", s.handleListSessions)
r.Delete("/auth/sessions/{id}", s.handleRevokeSession)
r.Post("/auth/sessions/revoke-all", s.handleRevokeAllSessions)
```

Add `"strconv"` and `"github.com/go-chi/chi/v5"` imports as needed.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: profile, password change, and device management"
```

---

### Task 9: Login rate limiting

**Files:**
- Create: `internal/auth/limiter.go`, `internal/auth/limiter_test.go`
- Modify: `internal/api/server.go` (limiter fields + init), `internal/api/auth_handlers.go` (gate in `handleLogin`)

**Interfaces:**
- Consumes: `handleLogin` (Task 7).
- Produces: `auth.NewLimiter(perMinute float64, burst int) *Limiter`, `(*Limiter).Allow(key string) bool`; `Server.loginIP`, `Server.loginAcct` (`*auth.Limiter`). Every login attempt (success or failure) consumes one token per key: `ip:<addr>` (10/min) and `acct:<email>` (5/min); exhausted → HTTP 429 `rate_limited`.

- [ ] **Step 1: Write the failing test**

`internal/auth/limiter_test.go`:

```go
package auth

import "testing"

func TestLimiterAllowsUpToBurst(t *testing.T) {
	l := NewLimiter(60, 3) // 1/sec, burst 3
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("attempt %d denied within burst", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("4th immediate attempt allowed, want denied")
	}
	if !l.Allow("other") {
		t.Fatal("different key denied") // keys are independent
	}
}
```

Add to `internal/api/auth_handlers_test.go`:

```go
func TestLoginRateLimited(t *testing.T) {
	_, ts := newTestServer(t, nil)
	registerUser(t, ts, "rl@test.dev", "longpassword1")
	// account limiter is 5/min: 5 attempts (any result), 6th is 429
	for i := 0; i < 5; i++ {
		doJSON(t, ts, "POST", "/api/v1/auth/login",
			map[string]any{"email": "rl@test.dev", "password": "wrongpassword"}, nil)
	}
	resp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "rl@test.dev", "password": "wrongpassword"}, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th attempt = %d, want 429", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/auth/ ./internal/api/ -run 'Limiter|RateLimited'`
Expected: FAIL — `NewLimiter` undefined; login not rate-limited.

- [ ] **Step 3: Implement**

`internal/auth/limiter.go`:

```go
package auth

import (
	"sync"

	"golang.org/x/time/rate"
)

// Limiter is a keyed, in-memory token-bucket rate limiter. Good enough for a
// single-instance deployment; not distributed (fine — one binary, one host).
type Limiter struct {
	mu      sync.Mutex
	r       rate.Limit
	burst   int
	entries map[string]*rate.Limiter
}

// NewLimiter builds a limiter allowing perMinute events per key on average,
// with an immediate burst.
func NewLimiter(perMinute float64, burst int) *Limiter {
	return &Limiter{
		r:       rate.Limit(perMinute / 60.0),
		burst:   burst,
		entries: make(map[string]*rate.Limiter),
	}
}

// Allow reports whether one event for key is permitted.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	rl, ok := l.entries[key]
	if !ok {
		if len(l.entries) > 10_000 { // bound memory under key spray
			l.entries = make(map[string]*rate.Limiter)
		}
		rl = rate.NewLimiter(l.r, l.burst)
		l.entries[key] = rl
	}
	return rl.Allow()
}
```

Run `go get golang.org/x/time@latest`.

`server.go` — add fields and init:

```go
type Server struct {
	cfg *config.Config
	q   *dbpkg.Queries
	st  *files.Storage
	raw *sql.DB

	loginIP   *auth.Limiter // 10 attempts/min per IP
	loginAcct *auth.Limiter // 5 attempts/min per account
}

func New(cfg *config.Config, raw *sql.DB, st *files.Storage) *Server {
	return &Server{
		cfg: cfg, q: dbpkg.New(raw), st: st, raw: raw,
		loginIP:   auth.NewLimiter(10, 10),
		loginAcct: auth.NewLimiter(5, 5),
	}
}
```

`auth_handlers.go` — at the top of `handleLogin`, after normalizing the email:

```go
if !s.loginIP.Allow("ip:" + clientIP(r)) {
	writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again later")
	return
}
if req.Email != "" && !s.loginAcct.Allow("acct:"+req.Email) {
	writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again later")
	return
}
```

Note: successful logins also consume tokens — that is intended (5 logins/min per account is plenty for humans, hostile for credential stuffers).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/auth/ ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: rate-limited login"
```

---

### Task 10: Admin — users and invites

**Files:**
- Create: `internal/api/admin_handlers.go`, `internal/api/admin_handlers_test.go`
- Modify: `internal/api/server.go` (admin routes)

**Interfaces:**
- Consumes: `ListUsers`, `SetUserDisabled`, `SetUserAdmin`, invite queries (Task 3), `DeleteAllUserSessions` (Task 3).
- Produces: routes `GET /api/v1/admin/users`, `PATCH /api/v1/admin/users/{id}`, `GET /api/v1/admin/invites`, `POST /api/v1/admin/invites`, `DELETE /api/v1/admin/invites/{id}` (all `requireAuth` + `requireAdmin`). Test helper `loginUser(t, ts, email, password) *http.Cookie`.

- [ ] **Step 1: Write the failing test**

`internal/api/admin_handlers_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"stasher/internal/config"
)

func loginUser(t *testing.T, ts *httptest.Server, email, password string) *http.Cookie {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": email, "password": password}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s = %d", email, resp.StatusCode)
	}
	return sessionCookieFrom(resp)
}
```

```go
func adminSetup(t *testing.T) (admin, pleb *http.Cookie, ts *httptest.Server, s *Server) {
	s, ts = newTestServer(t, func(c *config.Config) { c.Registration = "invite" })
	// open-mode first user would also be admin; keep invite mode realistic:
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "admin@test.dev", "password": "longpassword1", "display_name": "Admin"}, nil)
	if resp.StatusCode != 201 { // bootstrap admin, no invite needed
		t.Fatalf("bootstrap register = %d", resp.StatusCode)
	}
	admin = sessionCookieFrom(resp)

	// admin creates an invite; pleb uses it
	inv := doJSON(t, ts, "POST", "/api/v1/admin/invites", map[string]any{}, admin)
	var invBody struct {
		Code string `json:"code"`
	}
	decodeBody(t, inv, &invBody)
	if len(invBody.Code) != 10 {
		t.Fatalf("invite code = %q", invBody.Code)
	}
	pleb = sessionCookieFrom(doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "pleb@test.dev", "password": "longpassword1", "display_name": "Pleb",
			"invite_code": invBody.Code}, nil))
	return admin, pleb, ts, s
}

func TestAdminRequiresAdmin(t *testing.T) {
	_, pleb, ts, _ := adminSetup(t)
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/admin/users"},
		{"POST", "/api/v1/admin/invites"},
	} {
		if resp := doJSON(t, ts, tc.m, tc.p, map[string]any{}, pleb); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s as pleb = %d, want 403", tc.m, tc.p, resp.StatusCode)
		}
	}
}

func TestAdminDisableUserRevokesSessions(t *testing.T) {
	admin, pleb, ts, _ := adminSetup(t)
	list := doJSON(t, ts, "GET", "/api/v1/admin/users", nil, admin)
	var users struct {
		Data []struct {
			ID    int64 `json:"id"`
			Email string `json:"email"`
		} `json:"data"`
	}
	decodeBody(t, list, &users)
	var plebID int64
	for _, u := range users.Data {
		if u.Email == "pleb@test.dev" {
			plebID = u.ID
		}
	}
	if plebID == 0 {
		t.Fatal("pleb not in user list")
	}
	resp := doJSON(t, ts, "PATCH", "/api/v1/admin/users/"+fmtInt64(plebID),
		map[string]any{"disabled": true}, admin)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable = %d", resp.StatusCode)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, pleb); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled user session = %d, want 401", r.StatusCode)
	}
}

func TestAdminCannotDisableSelf(t *testing.T) {
	admin, _, ts, _ := adminSetup(t)
	resp := doJSON(t, ts, "PATCH", "/api/v1/admin/users/1", map[string]any{"disabled": true}, admin)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self-disable = %d, want 400", resp.StatusCode)
	}
}

func TestInviteLifecycle(t *testing.T) {
	admin, _, ts, _ := adminSetup(t)
	inv := doJSON(t, ts, "POST", "/api/v1/admin/invites", map[string]any{}, admin)
	var created struct {
		ID   int64 `json:"id"`
		Code string `json:"code"`
	}
	decodeBody(t, inv, &created)

	// list shows it unused
	var list struct {
		Data []struct {
			Code        string `json:"code"`
			UsedByEmail string `json:"used_by_email"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/admin/invites", nil, admin), &list)
	found := false
	for _, i := range list.Data {
		if i.Code == created.Code {
			found = true
			if i.UsedByEmail != "" {
				t.Fatalf("new invite already used by %q", i.UsedByEmail)
			}
		}
	}
	if !found {
		t.Fatal("created invite not listed")
	}
	// delete unused invite
	if r := doJSON(t, ts, "DELETE", "/api/v1/admin/invites/"+fmtInt64(created.ID), nil, admin); r.StatusCode != 204 {
		t.Fatalf("delete invite = %d", r.StatusCode)
	}
	// deleted code no longer registers
	r := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "x@test.dev", "password": "longpassword1", "display_name": "X",
			"invite_code": created.Code}, nil)
	if r.StatusCode != 400 {
		t.Fatalf("register with deleted invite = %d, want 400", r.StatusCode)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run Admin`
Expected: FAIL — routes 404.

- [ ] **Step 3: Implement**

`internal/api/admin_handlers.go`:

```go
package api

import (
	"crypto/rand"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.q.ListUsers(r.Context())
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "email": u.Email, "display_name": u.DisplayName,
			"default_item_visibility": u.DefaultItemVisibility,
			"is_admin": u.IsAdmin, "disabled": u.Disabled,
			"created_at": u.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

type adminUserPatch struct {
	Disabled *bool `json:"disabled"`
	IsAdmin  *bool `json:"is_admin"`
}

func (s *Server) handleAdminPatchUser(w http.ResponseWriter, r *http.Request) {
	me, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid user id")
		return
	}
	var req adminUserPatch
	if !decodeJSON(w, r, &req) {
		return
	}
	if id == me.ID && req.Disabled != nil && *req.Disabled {
		writeErr(w, http.StatusBadRequest, "cannot_disable_self", "you cannot disable your own account")
		return
	}
	target, err := s.q.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if req.Disabled != nil {
		if err := s.q.SetUserDisabled(r.Context(), dbpkg.SetUserDisabledParams{
			Disabled: *req.Disabled, UpdatedAt: now, ID: id,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
		if *req.Disabled {
			if err := s.q.DeleteAllUserSessions(r.Context(), id); err != nil {
				s.internalErr(w, err)
				return
			}
		}
	}
	if req.IsAdmin != nil {
		if err := s.q.SetUserAdmin(r.Context(), dbpkg.SetUserAdminParams{
			IsAdmin: *req.IsAdmin, UpdatedAt: now, ID: id,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	target, _ = s.q.GetUserByID(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": target.ID, "email": target.Email, "display_name": target.DisplayName,
		"is_admin": target.IsAdmin, "disabled": target.Disabled,
	})
}

func newInviteCode() string {
	const alphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ" // no I L O U 0 1
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func (s *Server) handleAdminCreateInvite(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	inv, err := s.q.CreateInvite(r.Context(), dbpkg.CreateInviteParams{
		Code: newInviteCode(), CreatedBy: u.ID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": inv.ID, "code": inv.Code, "created_at": inv.CreatedAt})
}

func (s *Server) handleAdminListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.q.ListInvites(r.Context())
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, i := range rows {
		usedBy := ""
		if i.UsedBy.Valid {
			usedBy = i.UsedByEmail
		}
		out = append(out, map[string]any{
			"id": i.ID, "code": i.Code, "created_at": i.CreatedAt,
			"used_by_email": usedBy, "used_at": i.UsedAt,
		})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

func (s *Server) handleAdminDeleteInvite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid invite id")
		return
	}
	if err := s.q.DeleteInvite(r.Context(), id); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

(`ListInvitesRow.UsedAt` is nullable → use `i.UsedAt.String` when valid, or marshal as-is; keep `used_at` via `i.UsedAt` and let encoding/json render `{Valid:...}` — no: render cleanly:)

```go
var usedAt any
if i.UsedAt.Valid {
	usedAt = i.UsedAt.String
}
```

and put `"used_at": usedAt` in the map. Add `"strconv"` import. Routes (inside the authed group):

```go
r.Route("/admin", func(r chi.Router) {
	r.Use(s.requireAdmin)
	r.Get("/users", s.handleAdminListUsers)
	r.Patch("/users/{id}", s.handleAdminPatchUser)
	r.Get("/invites", s.handleAdminListInvites)
	r.Post("/invites", s.handleAdminCreateInvite)
	r.Delete("/invites/{id}", s.handleAdminDeleteInvite)
})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: admin user management and invite codes"
```

---

### Task 11: Locations

**Files:**
- Create: `internal/api/locations_handlers.go`, `internal/api/locations_handlers_test.go`
- Modify: `internal/db/queries.sql` (+8 queries), `internal/api/server.go` (routes)

**Interfaces:**
- Consumes: Server scaffolding, `requireAuth`.
- Produces: routes `GET/POST /api/v1/locations`, `GET/PATCH/DELETE /api/v1/locations/{id}`. `GET /locations` returns the user's flat location list (default `page_size` 200 — locations are few); `GET /locations/{id}` returns `{"location": {...}, "breadcrumbs": [{"id","name"}, ... root-first]}`. `(user_id, parent_id, name)` uniqueness enforced in handlers → 409 `location_exists`. Queries: `CreateLocation`, `GetLocation`, `GetLocationByName`, `ListLocations`, `UpdateLocation`, `DeleteLocation`, `ReparentChildren`, `ClearItemsLocation`, `LocationDescendants`, `LocationBreadcrumbs`.

- [ ] **Step 1: Add queries**

Append to `internal/db/queries.sql`, then `go tool sqlc generate`:

```sql
-- name: CreateLocation :one
INSERT INTO locations (user_id, name, parent_id, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetLocation :one
SELECT * FROM locations WHERE id = ? AND user_id = ?;

-- name: ListLocations :many
SELECT * FROM locations WHERE user_id = ? ORDER BY name;

-- name: UpdateLocation :exec
UPDATE locations SET name = ?, notes = ?, parent_id = ?, updated_at = ? WHERE id = ? AND user_id = ?;

-- name: DeleteLocation :exec
DELETE FROM locations WHERE id = ? AND user_id = ?;

-- name: ReparentChildren :exec
UPDATE locations SET parent_id = ? WHERE parent_id = ? AND user_id = ?;

-- name: ClearItemsLocation :exec
UPDATE items SET location_id = NULL, updated_at = ? WHERE location_id = ? AND user_id = ?;

-- name: LocationDescendants :many
WITH RECURSIVE desc(id) AS (
    SELECT id FROM locations WHERE id = ? AND user_id = ?
    UNION ALL
    SELECT l.id FROM locations l JOIN desc ON l.parent_id = desc.id WHERE l.user_id = ?
) SELECT id FROM desc;

-- name: LocationBreadcrumbs :many
WITH RECURSIVE up(id, name, parent_id, depth) AS (
    SELECT id, name, parent_id, 0 FROM locations WHERE id = ? AND user_id = ?
    UNION ALL
    SELECT l.id, l.name, l.parent_id, up.depth + 1
    FROM locations l JOIN up ON l.id = up.parent_id WHERE l.user_id = ?
) SELECT id, name, depth FROM up ORDER BY depth DESC;

-- name: GetLocationByName :one
-- NULL-safe parent comparison ("IS ?") so duplicates under the same parent
-- (including the root, NULL) are detectable — spec: app-logic uniqueness.
SELECT * FROM locations WHERE user_id = ? AND name = ? AND parent_id IS ?;
```

- [ ] **Step 2: Write the failing tests**

`internal/api/locations_handlers_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// createLocation helper returns the new location's id.
func createLocation(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string, parentID any) int64 {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/locations",
		map[string]any{"name": name, "parent_id": parentID}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create location %q = %d", name, resp.StatusCode)
	}
	var body struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &body)
	return body.ID
}

func TestLocationTreeAndBreadcrumbs(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "loc@test.dev", "longpassword1")
	building := createLocation(t, ts, cookie, "House", nil)
	cabinet := createLocation(t, ts, cookie, "Cabinet", building)
	bin := createLocation(t, ts, cookie, "Parts Bin 1", cabinet)

	var got struct {
		Location struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"location"`
		Breadcrumbs []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"breadcrumbs"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations/"+fmtInt64(bin), nil, cookie), &got)
	if got.Location.ID != bin {
		t.Fatalf("location = %+v", got.Location)
	}
	if len(got.Breadcrumbs) != 3 ||
		got.Breadcrumbs[0].ID != building || got.Breadcrumbs[1].ID != cabinet ||
		got.Breadcrumbs[2].ID != bin {
		t.Fatalf("breadcrumbs = %+v", got.Breadcrumbs)
	}

	var list struct {
		Data []struct {
			ID       int64 `json:"id"`
			ParentID *int64 `json:"parent_id"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations", nil, cookie), &list)
	if list.Total != 3 || len(list.Data) != 3 {
		t.Fatalf("list = %+v", list)
	}
}

func TestLocationNameUniquePerParent(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "uniq@test.dev", "longpassword1")
	createLocation(t, ts, cookie, "Shelf", nil)
	// same name under same parent -> 409
	if resp := doJSON(t, ts, "POST", "/api/v1/locations",
		map[string]any{"name": "Shelf"}, cookie); resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup location = %d, want 409", resp.StatusCode)
	}
	// same name under a different parent -> ok
	parent := createLocation(t, ts, cookie, "Cabinet", nil)
	if resp := doJSON(t, ts, "POST", "/api/v1/locations",
		map[string]any{"name": "Shelf", "parent_id": parent}, cookie); resp.StatusCode != http.StatusCreated {
		t.Fatalf("same name other parent = %d, want 201", resp.StatusCode)
	}
}

func TestLocationCycleRejected(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "cyc@test.dev", "longpassword1")
	a := createLocation(t, ts, cookie, "A", nil)
	b := createLocation(t, ts, cookie, "B", a)
	// moving A under B would create a cycle
	resp := doJSON(t, ts, "PATCH", "/api/v1/locations/"+fmtInt64(a),
		map[string]any{"parent_id": b}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cycle move = %d, want 400", resp.StatusCode)
	}
	// moving A under itself also rejected
	resp = doJSON(t, ts, "PATCH", "/api/v1/locations/"+fmtInt64(a),
		map[string]any{"parent_id": a}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self move = %d, want 400", resp.StatusCode)
	}
}

func TestLocationIsolation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "alice-loc@test.dev", "longpassword1")
	bob := registerUser(t, ts, "bob-loc@test.dev", "longpassword1")
	id := createLocation(t, ts, alice, "Alice's shelf", nil)
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/locations/" + fmtInt64(id)},
		{"PATCH", "/api/v1/locations/" + fmtInt64(id)},
		{"DELETE", "/api/v1/locations/" + fmtInt64(id)},
	} {
		if resp := doJSON(t, ts, tc.m, tc.p, map[string]any{"name": "X"}, bob); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s other user's location = %d, want 404", tc.m, resp.StatusCode)
		}
	}
	// bob creating under alice's parent is rejected
	resp := doJSON(t, ts, "POST", "/api/v1/locations",
		map[string]any{"name": "B-child", "parent_id": id}, bob)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("create under foreign parent = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteLocationReparentsAndClearsItems(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "del@test.dev", "longpassword1")
	building := createLocation(t, ts, cookie, "B", nil)
	cabinet := createLocation(t, ts, cookie, "C", building)
	bin := createLocation(t, ts, cookie, "D", cabinet)

	// an item in the cabinet (created via raw SQL; items API comes in Task 13)
	mustExec(t, s, `INSERT INTO items (user_id, name, qty, location_id, visibility, created_at, updated_at)
		SELECT id, 'widget', 1, ?, 'private', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z' FROM users LIMIT 1`, cabinet)

	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(cabinet), nil, cookie)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	var binBody struct {
		Location struct {
			ParentID *int64 `json:"parent_id"`
		} `json:"location"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations/"+fmtInt64(bin), nil, cookie), &binBody)
	if binBody.Location.ParentID == nil || *binBody.Location.ParentID != building {
		t.Fatalf("bin not reparented to building: %+v", binBody.Location)
	}
	var n int64
	mustQueryRow(t, s, `SELECT COUNT(*) FROM items WHERE location_id IS NULL AND name='widget'`).Scan(&n)
	if n != 1 {
		t.Fatalf("item not detached from deleted location: n=%d", n)
	}
}
```

Helpers for `testutil_test.go`:

```go
func mustExec(t *testing.T, s *Server, query string, args ...any) {
	t.Helper()
	if _, err := s.raw.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func mustQueryRow(t *testing.T, s *Server, query string) *sql.Row {
	t.Helper()
	return s.raw.QueryRow(query)
}
```

(`database/sql` import.)

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/api/ -run Location`
Expected: FAIL — routes 404.

- [ ] **Step 4: Implement**

`internal/api/locations_handlers.go`:

```go
package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func locationJSON(l dbpkg.Location) map[string]any {
	var parent any
	if l.ParentID.Valid {
		parent = l.ParentID.Int64
	}
	return map[string]any{
		"id": l.ID, "name": l.Name, "parent_id": parent, "notes": l.Notes,
		"created_at": l.CreatedAt, "updated_at": l.UpdatedAt,
	}
}

type locationReq struct {
	Name     string  `json:"name"`
	Notes    string  `json:"notes"`
	ParentID *int64  `json:"parent_id"`
}

// checkParent verifies parent is owned by u (or nil). Returns param for SQL.
func (s *Server) checkParent(w http.ResponseWriter, r *http.Request, u dbpkg.User, parentID *int64) (sql.NullInt64, bool) {
	if parentID == nil {
		return sql.NullInt64{}, true
	}
	if _, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: *parentID, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "parent location not found")
		return sql.NullInt64{}, false
	}
	return sqlNullInt64(*parentID), true
}

func (s *Server) handleCreateLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req locationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	parent, ok := s.checkParent(w, r, u, req.ParentID)
	if !ok {
		return
	}
	// app-logic uniqueness: (user_id, parent, name)
	if existing, err := s.q.GetLocationByName(r.Context(), dbpkg.GetLocationByNameParams{
		UserID: u.ID, Name: req.Name, ParentID: parent,
	}); err == nil && existing.ID != 0 {
		writeErr(w, http.StatusConflict, "location_exists",
			"a location with this name already exists under this parent")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	loc, err := s.q.CreateLocation(r.Context(), dbpkg.CreateLocationParams{
		UserID: u.ID, Name: req.Name, ParentID: parent, Notes: req.Notes,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, locationJSON(loc))
}

func (s *Server) handleGetLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	crumbs, err := s.q.LocationBreadcrumbs(r.Context(), dbpkg.LocationBreadcrumbsParams{
		ID: id, UserID: u.ID, UserID_2: u.ID,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(crumbs))
	for _, c := range crumbs {
		out = append(out, map[string]any{"id": c.ID, "name": c.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"location": locationJSON(loc), "breadcrumbs": out})
}

func (s *Server) handleListLocations(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	locs, err := s.q.ListLocations(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(locs))
	for _, l := range locs {
		out = append(out, locationJSON(l))
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

func (s *Server) handleUpdateLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	var req locationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != "" { // empty name = keep current
		if len(req.Name) > 200 {
			writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
			return
		}
		loc.Name = req.Name
	}
	loc.Notes = req.Notes

	var parent sql.NullInt64
	if req.ParentID != nil { // null explicitly clears the parent
		if *req.ParentID == id {
			writeErr(w, http.StatusBadRequest, "cycle", "a location cannot be its own parent")
			return
		}
		p, ok := s.checkParent(w, r, u, req.ParentID)
		if !ok {
			return
		}
		// cycle check: new parent must not be a descendant of id
		desc, err := s.q.LocationDescendants(r.Context(), dbpkg.LocationDescendantsParams{
			ID: id, UserID: u.ID, UserID_2: u.ID,
		})
		if err != nil {
			s.internalErr(w, err)
			return
		}
		for _, d := range desc {
			if d == *req.ParentID {
				writeErr(w, http.StatusBadRequest, "cycle", "cannot move a location under its own descendant")
				return
			}
		}
		parent = p
	} else {
		parent = loc.ParentID
	}

	// app-logic uniqueness check against the new (name, parent) combo
	if existing, err := s.q.GetLocationByName(r.Context(), dbpkg.GetLocationByNameParams{
		UserID: u.ID, Name: loc.Name, ParentID: parent,
	}); err == nil && existing.ID != id {
		writeErr(w, http.StatusConflict, "location_exists",
			"a location with this name already exists under this parent")
		return
	}

	if err := s.q.UpdateLocation(r.Context(), dbpkg.UpdateLocationParams{
		Name: loc.Name, Notes: loc.Notes, ParentID: parent,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: id, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	loc, _ = s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	writeJSON(w, http.StatusOK, locationJSON(loc))
}

func (s *Server) handleDeleteLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := qtx.ReparentChildren(r.Context(), dbpkg.ReparentChildrenParams{
		ParentID: loc.ParentID, ParentID_2: id, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := qtx.ClearItemsLocation(r.Context(), dbpkg.ClearItemsLocationParams{
		UpdatedAt: now, LocationID: id, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := qtx.DeleteLocation(r.Context(), dbpkg.DeleteLocationParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

**Note on sqlc param names:** when a query repeats a column name (e.g. `parent_id = ?` twice), sqlc appends `_2`. The exact generated names for multi-use columns may differ — after `go tool sqlc generate`, open `internal/db/models.go` and fix the struct-literal field names above to match (the *logic* stays). Expect e.g. `ReparentChildrenParams{ParentID, ParentID_2, UserID}` and `LocationDescendantsParams{ID, UserID, UserID_2}`.

Routes (authed group):

```go
r.Get("/locations", s.handleListLocations)
r.Post("/locations", s.handleCreateLocation)
r.Get("/locations/{id}", s.handleGetLocation)
r.Patch("/locations/{id}", s.handleUpdateLocation)
r.Delete("/locations/{id}", s.handleDeleteLocation)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run Location -v`
Expected: PASS (5 tests).

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: locations with nesting, breadcrumbs, cycle prevention"
```

---

### Task 12: Tags

**Files:**
- Create: `internal/api/tags_handlers.go`, `internal/api/tags_handlers_test.go`
- Modify: `internal/db/queries.sql` (+5 queries), `internal/api/server.go` (routes)

**Interfaces:**
- Consumes: Server scaffolding.
- Produces: routes `GET/POST /api/v1/tags`, `PATCH/DELETE /api/v1/tags/{id}`. `GET /tags` rows: `{id, name, item_count}`. Queries: `CreateTag`, `GetTagByName`, `ListTagsWithCounts`, `UpdateTag`, `DeleteTag`.

- [ ] **Step 1: Add queries**

```sql
-- name: CreateTag :one
INSERT INTO tags (user_id, name) VALUES (?, ?) RETURNING *;

-- name: GetTagByName :one
SELECT * FROM tags WHERE user_id = ? AND name = ?;

-- name: ListTagsWithCounts :many
SELECT t.id, t.name, COUNT(it.item_id) AS item_count
FROM tags t LEFT JOIN item_tags it ON it.tag_id = t.id
WHERE t.user_id = ?
GROUP BY t.id, t.name
ORDER BY t.name;

-- name: UpdateTag :exec
UPDATE tags SET name = ? WHERE id = ? AND user_id = ?;

-- name: DeleteTag :exec
DELETE FROM tags WHERE id = ? AND user_id = ?;
```

- [ ] **Step 2: Write the failing tests**

`internal/api/tags_handlers_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func createTag(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string) {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/tags", map[string]any{"name": name}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create tag %q = %d", name, resp.StatusCode)
	}
}

func TestTagCRUDAndDuplication(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "tag@test.dev", "longpassword1")
	createTag(t, ts, cookie, "electronics")

	// duplicate name -> 409
	if resp := doJSON(t, ts, "POST", "/api/v1/tags", map[string]any{"name": "electronics"}, cookie); resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup tag = %d, want 409", resp.StatusCode)
	}
	// list shows it
	var list struct {
		Data []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/tags", nil, cookie), &list)
	if len(list.Data) != 1 || list.Data[0].Name != "electronics" {
		t.Fatalf("tags = %+v", list.Data)
	}
	id := list.Data[0].ID

	// rename
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+fmtInt64(id), map[string]any{"name": "electronics-parts"}, cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename = %d", resp.StatusCode)
	}
	// rename to an existing name -> 409 (create the collision first)
	createTag(t, ts, cookie, "other")
	if resp := doJSON(t, ts, "PATCH", "/api/v1/tags/"+fmtInt64(id), map[string]any{"name": "other"}, cookie); resp.StatusCode != http.StatusConflict {
		t.Fatalf("colliding rename = %d, want 409", resp.StatusCode)
	}
	// delete
	if resp := doJSON(t, ts, "DELETE", "/api/v1/tags/"+fmtInt64(id), nil, cookie); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete tag = %d", resp.StatusCode)
	}
}

func TestTagNamesScopedPerUser(t *testing.T) {
	_, ts := newTestServer(t, nil)
	a := registerUser(t, ts, "tag-a@test.dev", "longpassword1")
	b := registerUser(t, ts, "tag-b@test.dev", "longpassword1")
	createTag(t, ts, a, "shared-name")
	createTag(t, ts, b, "shared-name") // same name, different user: 201
	var listB struct {
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/tags", nil, b), &listB)
	if listB.Total != 1 {
		t.Fatalf("tag bleed between users: %+v", listB)
	}
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/api/ -run Tag`
Expected: FAIL — routes 404.

- [ ] **Step 4: Implement**

`internal/api/tags_handlers.go`:

```go
package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	rows, err := s.q.ListTagsWithCounts(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, tg := range rows {
		out = append(out, map[string]any{"id": tg.ID, "name": tg.Name, "item_count": tg.ItemCount})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

type tagReq struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req tagReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 100 {
		writeErr(w, http.StatusBadRequest, "validation_error", "tag name must be 1-100 chars")
		return
	}
	if _, err := s.q.GetTagByName(r.Context(), dbpkg.GetTagByNameParams{UserID: u.ID, Name: req.Name}); err == nil {
		writeErr(w, http.StatusConflict, "tag_exists", "tag already exists")
		return
	}
	tg, err := s.q.CreateTag(r.Context(), dbpkg.CreateTagParams{UserID: u.ID, Name: req.Name})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": tg.ID, "name": tg.Name})
}

func (s *Server) handleUpdateTag(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid tag id")
		return
	}
	var req tagReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 100 {
		writeErr(w, http.StatusBadRequest, "validation_error", "tag name must be 1-100 chars")
		return
	}
	if existing, err := s.q.GetTagByName(r.Context(), dbpkg.GetTagByNameParams{UserID: u.ID, Name: req.Name}); err == nil && existing.ID != id {
		writeErr(w, http.StatusConflict, "tag_exists", "another tag already has this name")
		return
	}
	if err := s.q.UpdateTag(r.Context(), dbpkg.UpdateTagParams{Name: req.Name, ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": req.Name})
}

func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid tag id")
		return
	}
	if err := s.q.DeleteTag(r.Context(), dbpkg.DeleteTagParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Routes (authed group):

```go
r.Get("/tags", s.handleListTags)
r.Post("/tags", s.handleCreateTag)
r.Patch("/tags/{id}", s.handleUpdateTag)
r.Delete("/tags/{id}", s.handleDeleteTag)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run Tag -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: tags crud"
```

---

### Task 13: Items CRUD

**Files:**
- Create: `internal/api/items_handlers.go`, `internal/api/items_handlers_test.go`
- Modify: `internal/db/queries.sql` (+10 queries), `internal/api/server.go` (routes)

**Interfaces:**
- Consumes: Server scaffolding, location queries (Task 11), tag queries (Task 12).
- Produces: routes `POST /api/v1/items`, `GET /api/v1/items/{id}`, `PATCH /api/v1/items/{id}`, `DELETE /api/v1/items/{id}` (listing comes in Task 14). Item JSON: `{id, name, description, qty, unit, location_id, visibility, created_at, updated_at, tags, photos, documents, projects}` — `tags` replaced via `tags: ["name", ...]` on create/patch (set semantics); `photos`/`documents` are empty until Task 15, `projects` until Task 16. Queries: `CreateItem`, `GetItem`, `GetItemAnyOwner`, `UpdateItem`, `DeleteItem`, `GetItemTags`, `ClearItemTags`, `AttachItemTag`, `ListItemPhotos`, `ListItemDocuments`. Helper `s.setItemTags(r, u, itemID, names []string)`.

- [ ] **Step 1: Add queries**

```sql
-- name: CreateItem :one
INSERT INTO items (user_id, name, description, qty, unit, location_id, visibility, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetItem :one
SELECT * FROM items WHERE id = ? AND user_id = ?;

-- name: GetItemAnyOwner :one
SELECT * FROM items WHERE id = ?;

-- name: UpdateItem :exec
UPDATE items SET name = ?, description = ?, qty = ?, unit = ?, location_id = ?, visibility = ?, updated_at = ?
WHERE id = ? AND user_id = ?;

-- name: DeleteItem :exec
DELETE FROM items WHERE id = ? AND user_id = ?;

-- name: GetItemTags :many
SELECT t.id, t.name FROM tags t JOIN item_tags it ON it.tag_id = t.id WHERE it.item_id = ?;

-- name: ClearItemTags :exec
DELETE FROM item_tags WHERE item_id = ?;

-- name: AttachItemTag :exec
INSERT OR IGNORE INTO item_tags (item_id, tag_id) VALUES (?, ?);

-- name: ListItemPhotos :many
SELECT * FROM item_photos WHERE item_id = ? ORDER BY sort_order, id;

-- name: ListItemDocuments :many
SELECT * FROM item_documents WHERE item_id = ? ORDER BY filename;
```

- [ ] **Step 2: Write the failing tests**

`internal/api/items_handlers_test.go`:

```go
package api

import (
	"net/http"
	"testing"
)

func TestItemCreateGetUpdateDelete(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "item@test.dev", "longpassword1")

	// create with tag names (auto-created), qty numeric, shared visibility
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
		"name": "M3 screws", "description": "stainless", "qty": 250.5, "unit": "pcs",
		"visibility": "shared", "tags": []string{"hardware", "fasteners"},
	}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create item = %d", resp.StatusCode)
	}
	var created struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Qty        float64 `json:"qty"`
		Unit       string  `json:"unit"`
		Visibility string  `json:"visibility"`
		Tags       []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Photos    []any `json:"photos"`
		Documents []any `json:"documents"`
		Projects  []any `json:"projects"`
	}
	decodeBody(t, resp, &created)
	if created.Qty != 250.5 || created.Visibility != "shared" || len(created.Tags) != 2 ||
		len(created.Photos) != 0 || len(created.Projects) != 0 {
		t.Fatalf("created = %+v", created)
	}

	// get detail
	var got struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie), &got)
	if len(got.Tags) != 2 {
		t.Fatalf("detail tags = %+v", got.Tags)
	}

	// patch: qty + replace tags
	resp = doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID),
		map[string]any{"qty": 100, "tags": []string{"hardware"}}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch item = %d", resp.StatusCode)
	}
	var patched struct {
		Qty  float64 `json:"qty"`
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	decodeBody(t, resp, &patched)
	if patched.Qty != 100 || len(patched.Tags) != 1 || patched.Tags[0].Name != "hardware" {
		t.Fatalf("patched = %+v", patched)
	}

	// invalid qty rejected
	if resp := doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID),
		map[string]any{"qty": -5}, cookie); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative qty = %d, want 400", resp.StatusCode)
	}
	// foreign location rejected
	if resp := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "X", "location_id": 99999}, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign location = %d, want 404", resp.StatusCode)
	}

	// delete
	if resp := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp := doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted = %d, want 404", resp.StatusCode)
	}
}

func TestItemVisibilityDefaultsToUserSetting(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "vis@test.dev", "longpassword1")
	// flip account default to shared
	doJSON(t, ts, "PATCH", "/api/v1/auth/me",
		map[string]any{"default_item_visibility": "shared"}, cookie)
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "auto-shared"}, cookie)
	var created struct {
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &created)
	if created.Visibility != "shared" {
		t.Fatalf("default visibility = %q, want shared", created.Visibility)
	}
}

func TestItemIsolation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "alice-item@test.dev", "longpassword1")
	bob := registerUser(t, ts, "bob-item@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "private thing"}, alice)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &created)
	// bob cannot see, patch, or delete alice's PRIVATE item — 404 each time
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/items/" + fmtInt64(created.ID)},
		{"PATCH", "/api/v1/items/" + fmtInt64(created.ID)},
		{"DELETE", "/api/v1/items/" + fmtInt64(created.ID)},
	} {
		if resp := doJSON(t, ts, tc.m, tc.p, map[string]any{"name": "stolen"}, bob); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s private item = %d, want 404", tc.m, resp.StatusCode)
		}
	}
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/api/ -run Item`
Expected: FAIL — routes 404.

- [ ] **Step 4: Implement**

`internal/api/items_handlers.go`:

```go
package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

type itemReq struct {
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Qty         *float64 `json:"qty"`
	Unit        *string  `json:"unit"`
	LocationID  *int64   `json:"location_id"`
	Visibility  *string  `json:"visibility"`
	Tags        []string `json:"tags"`
}

func itemJSON(i dbpkg.Item) map[string]any {
	var loc any
	if i.LocationID.Valid {
		loc = i.LocationID.Int64
	}
	return map[string]any{
		"id": i.ID, "name": i.Name, "description": i.Description,
		"qty": i.Qty, "unit": i.Unit, "location_id": loc,
		"visibility": i.Visibility, "created_at": i.CreatedAt, "updated_at": i.UpdatedAt,
	}
}

// itemDetailJSON assembles the full item response (tags, photos, docs, projects).
func (s *Server) itemDetailJSON(r *http.Request, i dbpkg.Item) (map[string]any, error) {
	out := itemJSON(i)
	tags, err := s.q.GetItemTags(r.Context(), i.ID)
	if err != nil {
		return nil, err
	}
	tagList := make([]map[string]any, 0, len(tags))
	for _, tg := range tags {
		tagList = append(tagList, map[string]any{"id": tg.ID, "name": tg.Name})
	}
	photos, err := s.q.ListItemPhotos(r.Context(), i.ID)
	if err != nil {
		return nil, err
	}
	photoList := make([]map[string]any, 0, len(photos))
	for _, p := range photos {
		photoList = append(photoList, map[string]any{
			"id": p.ID, "storage_key": p.StorageKey, "caption": p.Caption,
			"sort_order": p.SortOrder, "created_at": p.CreatedAt,
		})
	}
	docs, err := s.q.ListItemDocuments(r.Context(), i.ID)
	if err != nil {
		return nil, err
	}
	docList := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		docList = append(docList, map[string]any{
			"id": d.ID, "storage_key": d.StorageKey, "filename": d.Filename,
			"mime_type": d.MimeType, "size_bytes": d.SizeBytes, "created_at": d.CreatedAt,
		})
	}
	out["tags"] = tagList
	out["photos"] = photoList
	out["documents"] = docList
	out["projects"] = []map[string]any{} // populated by Task 16
	return out, nil
}

// setItemTags replaces the item's tags with the given names (creating tags as needed).
func (s *Server) setItemTags(r *http.Request, u dbpkg.User, itemID int64, names []string) error {
	if len(names) > 50 {
		return errTooManyTags
	}
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	if err := qtx.ClearItemTags(r.Context(), itemID); err != nil {
		return err
	}
	for _, name := range names {
		if name == "" || len(name) > 100 {
			return errBadTagName
		}
		tg, err := qtx.GetTagByName(r.Context(), dbpkg.GetTagByNameParams{UserID: u.ID, Name: name})
		if err != nil {
			tg, err = qtx.CreateTag(r.Context(), dbpkg.CreateTagParams{UserID: u.ID, Name: name})
			if err != nil {
				return err
			}
		}
		if err := qtx.AttachItemTag(r.Context(), dbpkg.AttachItemTagParams{ItemID: itemID, TagID: tg.ID}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var errTooManyTags = errors.New("too many tags")
var errBadTagName = errors.New("bad tag name")

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req itemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	qty := 1.0
	if req.Qty != nil {
		if *req.Qty < 0 || *req.Qty > 1e12 {
			writeErr(w, http.StatusBadRequest, "validation_error", "qty must be between 0 and 1e12")
			return
		}
		qty = *req.Qty
	}
	visibility := u.DefaultItemVisibility
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		visibility = *req.Visibility
	}
	var loc sql.NullInt64
	if req.LocationID != nil {
		if _, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: *req.LocationID, UserID: u.ID}); err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "location not found")
			return
		}
		loc = sqlNullInt64(*req.LocationID)
	}
	desc := ""
	if req.Description != nil {
		desc = *req.Description
	}
	unit := ""
	if req.Unit != nil {
		unit = *req.Unit
	}
	now := time.Now().UTC().Format(time.RFC3339)
	item, err := s.q.CreateItem(r.Context(), dbpkg.CreateItemParams{
		UserID: u.ID, Name: req.Name, Description: desc, Qty: qty, Unit: unit,
		LocationID: loc, Visibility: visibility, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if req.Tags != nil {
		if err := s.setItemTags(r, u, item.ID, req.Tags); err != nil {
			s.tagErr(w, err)
			return
		}
	}
	detail, err := s.itemDetailJSON(r, item)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

func (s *Server) tagErr(w http.ResponseWriter, err error) {
	switch err {
	case errTooManyTags:
		writeErr(w, http.StatusBadRequest, "validation_error", "at most 50 tags per item")
	case errBadTagName:
		writeErr(w, http.StatusBadRequest, "validation_error", "tag names must be 1-100 chars")
	default:
		s.internalErr(w, err)
	}
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	item, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	detail, err := s.itemDetailJSON(r, item)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	item, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	var req itemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != "" { // absent = keep
		if len(req.Name) > 200 {
			writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
			return
		}
		item.Name = req.Name
	}
	if req.Description != nil {
		item.Description = *req.Description
	}
	if req.Qty != nil {
		if *req.Qty < 0 || *req.Qty > 1e12 {
			writeErr(w, http.StatusBadRequest, "validation_error", "qty must be between 0 and 1e12")
			return
		}
		item.Qty = *req.Qty
	}
	if req.Unit != nil {
		item.Unit = *req.Unit
	}
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		item.Visibility = *req.Visibility
	}
	if req.LocationID != nil { // explicit null clears; absent keeps
		if *req.LocationID == 0 {
			item.LocationID = sql.NullInt64{}
		} else {
			if _, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: *req.LocationID, UserID: u.ID}); err != nil {
				writeErr(w, http.StatusNotFound, "not_found", "location not found")
				return
			}
			item.LocationID = sqlNullInt64(*req.LocationID)
		}
	}
	if err := s.q.UpdateItem(r.Context(), dbpkg.UpdateItemParams{
		Name: item.Name, Description: item.Description, Qty: item.Qty, Unit: item.Unit,
		LocationID: item.LocationID, Visibility: item.Visibility,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: id, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if req.Tags != nil {
		if err := s.setItemTags(r, u, id, req.Tags); err != nil {
			s.tagErr(w, err)
			return
		}
	}
	item, _ = s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	detail, err := s.itemDetailJSON(r, item)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	if _, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	// collect file keys BEFORE the row delete (FK cascade removes them)
	photos, _ := s.q.ListItemPhotos(r.Context(), id)
	docs, _ := s.q.ListItemDocuments(r.Context(), id)
	if err := s.q.DeleteItem(r.Context(), dbpkg.DeleteItemParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	for _, p := range photos {
		s.st.Delete(p.StorageKey)
	}
	for _, d := range docs {
		s.st.Delete(d.StorageKey)
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Add `"errors"` import. Routes (authed group):

```go
r.Post("/items", s.handleCreateItem)
r.Get("/items/{id}", s.handleGetItem)
r.Patch("/items/{id}", s.handleUpdateItem)
r.Delete("/items/{id}", s.handleDeleteItem)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run Item -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: items crud with tags"
```

---

### Task 14: Item listing — full-text search and filters

**Files:**
- Create: `internal/api/search.go`, `internal/api/search_test.go`, `internal/api/list_handlers_test.go`
- Modify: `internal/db/queries.sql` (+1 query), `internal/api/items_handlers.go` (`handleListItems`), `internal/api/server.go` (route)

**Interfaces:**
- Consumes: item/tag/location queries.
- Produces: route `GET /api/v1/items` with query params `q`, `location_id` (includes descendants), `tag`, `project_id`, `visibility`, `page`, `page_size`. Response rows: item fields + `photo_count`; envelope `{"data": [...], "total": N}`. Helper `ftsQuery(raw string) string`. Query `ListItems`.

- [ ] **Step 1: Add the query**

```sql
-- name: ListItems :many
SELECT i.id, i.user_id, i.name, i.description, i.qty, i.unit, i.location_id,
       i.visibility, i.created_at, i.updated_at,
       (SELECT COUNT(*) FROM item_photos p WHERE p.item_id = i.id) AS photo_count,
       count(*) OVER () AS total_count
FROM items i
WHERE i.user_id = @user_id
  AND (sqlc.narg('q') IS NULL OR i.id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH @q))
  AND (sqlc.narg('loc_id') IS NULL OR i.location_id IN (
      WITH RECURSIVE desc(id) AS (
          SELECT @loc_id
          UNION ALL
          SELECT l.id FROM locations l JOIN desc ON l.parent_id = desc.id
      ) SELECT id FROM desc))
  AND (sqlc.narg('tag') IS NULL OR i.id IN (
      SELECT it.item_id FROM item_tags it JOIN tags t ON t.id = it.tag_id
      WHERE t.name = @tag AND t.user_id = @user_id))
  AND (sqlc.narg('project_id') IS NULL OR i.id IN (
      SELECT pi.item_id FROM project_items pi WHERE pi.project_id = @project_id))
  AND (sqlc.narg('visibility') IS NULL OR i.visibility = @visibility)
ORDER BY i.updated_at DESC
LIMIT @lim OFFSET @off;
```

Run `go tool sqlc generate`. Params struct: `ListItemsParams{UserID int64, Q, Tag, Visibility sql.NullString, LocID, ProjectID sql.NullInt64, Lim, Off int64}` (exact names per generated code — verify in `internal/db/queries.sql.go` and adjust handler literals to match).

- [ ] **Step 2: Write the failing tests**

`internal/api/search_test.go`:

```go
package api

import "testing"

func TestFTSQueryQuotesTokens(t *testing.T) {
	got := ftsQuery(`raspberry "pi" 5 AND* OR`)
	want := `"raspberry" "pi" "5" "AND*" "OR"`
	if got != want {
		t.Fatalf("ftsQuery = %q, want %q", got, want)
	}
	if ftsQuery("") != "" {
		t.Fatal("empty query should stay empty")
	}
}
```

`internal/api/list_handlers_test.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func listItems(t *testing.T, ts *httptest.Server, cookie *http.Cookie, query string) (names []string, total int64) {
	t.Helper()
	resp := doJSON(t, ts, "GET", "/api/v1/items"+query, nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %s = %d", query, resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, resp, &body)
	for _, d := range body.Data {
		names = append(names, d.Name)
	}
	return names, body.Total
}

func TestItemListSearchAndFilters(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "search@test.dev", "longpassword1")

	// locations: House > Cabinet
	house := createLocation(t, ts, cookie, "House", nil)
	cabinet := createLocation(t, ts, cookie, "Cabinet", house)

	mk := func(name, desc string, loc int64, tags ...string) {
		doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
			"name": name, "description": desc, "location_id": loc, "tags": tags,
		}, cookie)
	}
	mk("Raspberry Pi 5", "8GB single board computer", cabinet, "electronics", "sbcs")
	mk("M3 screws", "stainless machine screws", house, "hardware")
	mk("Filament spool", "PLA 1kg for 3D printing", house, "electronics")

	// full-text: name and description both indexed
	if names, _ := listItems(t, ts, cookie, "?q=raspberry"); len(names) != 1 || names[0] != "Raspberry Pi 5" {
		t.Fatalf("q=raspberry -> %v", names)
	}
	if names, _ := listItems(t, ts, cookie, "?q=stainless"); len(names) != 1 || names[0] != "M3 screws" {
		t.Fatalf("q=stainless (description) -> %v", names)
	}
	// FTS special characters must not 500 (Review Focus #2)
	if names, _ := listItems(t, ts, cookie, `?q="quoted" AND* (ne` + fmt.Sprint("ar)")); names == nil {
		t.Fatal("special-char query errored")
	}
	// location filter includes descendants: House matches Cabinet's item too
	if names, total := listItems(t, ts, cookie, "?location_id="+fmtInt64(house)); total != 3 {
		t.Fatalf("location with descendants -> %+v", names)
	}
	if _, total := listItems(t, ts, cookie, "?location_id="+fmtInt64(cabinet)); total != 1 {
		t.Fatal("cabinet should match exactly the one item")
	}
	// tag filter
	if _, total := listItems(t, ts, cookie, "?tag=electronics"); total != 2 {
		t.Fatal("tag=electronics should match 2")
	}
	// visibility filter
	if _, total := listItems(t, ts, cookie, "?visibility=shared"); total != 0 {
		t.Fatal("nothing shared yet")
	}
}

func TestItemListPaginationClamps(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "page@test.dev", "longpassword1")
	for i := 0; i < 3; i++ {
		doJSON(t, ts, "POST", "/api/v1/items",
			map[string]any{"name": fmt.Sprintf("item %d", i)}, cookie)
	}
	// page=0 and page_size=99999 clamp to 1 and 200 (Review Focus #4)
	if _, total := listItems(t, ts, cookie, "?page=0&page_size=99999"); total != 3 {
		t.Fatal("clamped pagination lost items")
	}
	if names, _ := listItems(t, ts, cookie, "?page=2&page_size=2"); len(names) != 1 {
		t.Fatalf("page 2 of 3 items -> %v", names)
	}
	if names, _ := listItems(t, ts, cookie, "?page=9&page_size=2"); names != nil {
		t.Fatalf("past-the-end page should be empty, got %v", names)
	}
	// non-numeric params fall back to defaults
	if _, total := listItems(t, ts, cookie, "?page=abc&page_size=-1"); total != 3 {
		t.Fatal("garbage pagination params broke the list")
	}
}

func TestItemListIsolated(t *testing.T) {
	_, ts := newTestServer(t, nil)
	a := registerUser(t, ts, "iso-a@test.dev", "longpassword1")
	b := registerUser(t, ts, "iso-b@test.dev", "longpassword1")
	doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "a's thing"}, a)
	if _, total := listItems(t, ts, b, ""); total != 0 {
		t.Fatal("b sees a's items")
	}
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/api/ -run 'FTSQuery|ItemList'`
Expected: FAIL — `ftsQuery` and route missing.

- [ ] **Step 4: Implement**

`internal/api/search.go`:

```go
package api

import "strings"

// ftsQuery converts raw user input into a safe FTS5 query: each whitespace-
// separated token is double-quoted (doubling embedded quotes), tokens ANDed.
// This makes FTS5 syntax errors impossible and prevents operator injection.
func ftsQuery(raw string) string {
	fields := strings.Fields(raw)
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(parts, " ")
}
```

Add to `internal/api/items_handlers.go`:

```go
func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	q := r.URL.Query()
	p := parsePage(r)

	params := dbpkg.ListItemsParams{
		UserID: u.ID, Lim: p.Limit(), Off: p.Offset(),
	}
	if raw := q.Get("q"); raw != "" {
		params.Q = sqlNullString(ftsQuery(raw))
	}
	if v := q.Get("location_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			params.LocID = sqlNullInt64(id)
		}
	}
	if v := q.Get("tag"); v != "" {
		params.Tag = sqlNullString(v)
	}
	if v := q.Get("project_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			params.ProjectID = sqlNullInt64(id)
		}
	}
	if v := q.Get("visibility"); v == "private" || v == "shared" {
		params.Visibility = sqlNullString(v)
	}

	rows, err := s.q.ListItems(r.Context(), params)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	total := int64(len(rows))
	for _, row := range rows {
		var loc any
		if row.LocationID.Valid {
			loc = row.LocationID.Int64
		}
		out = append(out, map[string]any{
			"id": row.ID, "name": row.Name, "description": row.Description,
			"qty": row.Qty, "unit": row.Unit, "location_id": loc,
			"visibility": row.Visibility, "photo_count": row.PhotoCount,
			"created_at": row.CreatedAt, "updated_at": row.UpdatedAt,
		})
	}
	if len(rows) > 0 {
		total = rows[0].TotalCount
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: total})
}
```

Route (authed group, BEFORE `/items/{id}` so chi matches the literal path first — chi handles this correctly either way, but keep the convention):

```go
r.Get("/items", s.handleListItems)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run 'FTSQuery|ItemList' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: item listing with fts search and filters"
```

---

### Task 15: Photos, documents, and gated file serving

**Files:**
- Create: `internal/api/files_handlers.go`, `internal/api/files_handlers_test.go`
- Modify: `internal/db/queries.sql` (+7 queries), `internal/api/server.go` (routes)

**Interfaces:**
- Consumes: `files.Storage` (Task 5), item queries.
- Produces: routes `POST /api/v1/items/{id}/photos`, `PATCH /api/v1/items/{id}/photos/{photoId}`, `DELETE /api/v1/items/{id}/photos/{photoId}`, `POST /api/v1/items/{id}/documents`, `DELETE /api/v1/items/{id}/documents/{docId}` (all authed), and `GET /files/{key}` (root-level, authed, owner-or-shared). Queries: `AddItemPhoto`, `UpdateItemPhoto`, `DeleteItemPhoto`, `GetPhotoByKey`, `AddItemDocument`, `DeleteItemDocument`, `GetDocumentByKey`.

- [ ] **Step 1: Add queries**

```sql
-- name: AddItemPhoto :one
INSERT INTO item_photos (item_id, storage_key, caption, sort_order, created_at)
VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateItemPhoto :exec
UPDATE item_photos SET caption = ?, sort_order = ? WHERE id = ? AND item_id = ?;

-- name: DeleteItemPhoto :exec
DELETE FROM item_photos WHERE id = ? AND item_id = ?;

-- name: GetPhotoByKey :one
SELECT p.id AS photo_id, p.storage_key, p.caption, p.sort_order,
       i.user_id, i.visibility
FROM item_photos p JOIN items i ON i.id = p.item_id
WHERE p.storage_key = ?;

-- name: AddItemDocument :one
INSERT INTO item_documents (item_id, storage_key, filename, mime_type, size_bytes, created_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: DeleteItemDocument :exec
DELETE FROM item_documents WHERE id = ? AND item_id = ?;

-- name: GetDocumentByKey :one
SELECT d.id AS document_id, d.storage_key, d.filename, d.mime_type,
       i.user_id, i.visibility
FROM item_documents d JOIN items i ON i.id = d.item_id
WHERE d.storage_key = ?;
```

- [ ] **Step 2: Write the failing tests**

`internal/api/files_handlers_test.go`:

```go
package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// png1x1 is a minimal valid PNG (sniffs as image/png).
var png1x1 = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func multipartBody(t *testing.T, field, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func postFile(t *testing.T, ts *httptest.Server, path, field, filename string, content []byte, cookie *http.Cookie) *http.Response {
	t.Helper()
	body, ctype := multipartBody(t, field, filename, content)
	req, _ := http.NewRequest("POST", ts.URL+path, body)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPhotoUploadAndServe(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "photo@test.dev", "longpassword1")
	other := registerUser(t, ts, "other@test.dev", "longpassword1")

	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "photogenic"}, owner)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "pic.png", png1x1, owner)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("photo upload = %d", resp.StatusCode)
	}
	var photo struct {
		ID         int64  `json:"id"`
		StorageKey string `json:"storage_key"`
	}
	decodeBody(t, resp, &photo)
	if photo.StorageKey == "" {
		t.Fatal("no storage key returned")
	}

	// GET file as owner
	getURL := func(key string, cookie *http.Cookie) *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+"/files/"+key, nil)
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := getURL(photo.StorageKey, owner); r.StatusCode != 200 {
		t.Fatalf("owner file get = %d", r.StatusCode)
	} else {
		b, _ := io.ReadAll(r.Body)
		if !bytes.Equal(b, png1x1) {
			t.Fatal("file content mismatch")
		}
	}
	// unauthenticated -> 401
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+photo.StorageKey, nil)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 401 {
		t.Fatalf("anon file get = %d, want 401", r.StatusCode)
	}
	// private item: other user must not read the file (404, not 403)
	if r := getURL(photo.StorageKey, other); r.StatusCode != 404 {
		t.Fatalf("other user private file = %d, want 404", r.StatusCode)
	}
	// share the item -> other user can read
	doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID),
		map[string]any{"visibility": "shared"}, owner)
	if r := getURL(photo.StorageKey, other); r.StatusCode != 200 {
		t.Fatalf("other user shared file = %d, want 200", r.StatusCode)
	}

	// path traversal rejected (Review Focus #1)
	req, _ = http.NewRequest("GET", ts.URL+"/files/../../etc/passwd", nil)
	req.AddCookie(owner)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 404 {
		t.Fatalf("traversal = %v %d, want 404", err, r.StatusCode)
	}

	// caption/sort patch
	if r := doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/"+fmtInt64(photo.ID),
		map[string]any{"caption": "front view", "sort_order": 5}, owner); r.StatusCode != 200 {
		t.Fatalf("photo patch = %d", r.StatusCode)
	}

	// photo delete removes row and file
	if r := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/"+fmtInt64(photo.ID),
		nil, owner); r.StatusCode != 204 {
		t.Fatalf("photo delete = %d", r.StatusCode)
	}
	if r := getURL(photo.StorageKey, owner); r.StatusCode != 404 {
		t.Fatalf("deleted photo file get = %d, want 404", r.StatusCode)
	}
}

func TestPhotoRejectsNonImage(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "notimg@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "x"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)
	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "evil.sh", []byte("#!/bin/sh\nrm -rf /\n"), cookie)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("non-image photo = %d, want 415", resp.StatusCode)
	}
	// empty file rejected
	resp = postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "empty.png", nil, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty photo = %d, want 400", resp.StatusCode)
	}
}

func TestDocumentUploadAndDelete(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "doc@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "doc item"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/documents",
		"file", "datasheet.pdf", []byte("%PDF-1.4 fake pdf bytes"), cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("doc upload = %d", resp.StatusCode)
	}
	var doc struct {
		ID         int64  `json:"id"`
		StorageKey string `json:"storage_key"`
		Filename   string `json:"filename"`
	}
	decodeBody(t, resp, &doc)
	if doc.Filename != "datasheet.pdf" {
		t.Fatalf("filename = %q", doc.Filename)
	}
	// file exists on disk
	f, err := s.st.Open(doc.StorageKey)
	if err != nil {
		t.Fatalf("stored doc not readable: %v", err)
	}
	f.Close()
	// path component stripped
	resp = postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/documents",
		"file", "../../etc/cron", []byte("junk"), cookie)
	var doc2 struct {
		Filename string `json:"filename"`
	}
	decodeBody(t, resp, &doc2)
	if doc2.Filename == "../../etc/cron" || doc2.Filename != "cron" {
		t.Fatalf("unsanitized filename: %q", doc2.Filename)
	}
	// delete
	if r := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(created.ID)+"/documents/"+fmtInt64(doc.ID),
		nil, cookie); r.StatusCode != 204 {
		t.Fatalf("doc delete = %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+doc.StorageKey, nil)
	req.AddCookie(cookie)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 404 {
		t.Fatalf("deleted doc file get = %d, want 404", r.StatusCode)
	}
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/api/ -run 'Photo|Document'`
Expected: FAIL — routes 404.

- [ ] **Step 4: Implement**

`internal/api/files_handlers.go`:

```go
package api

import (
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"stasher/internal/files"

	dbpkg "stasher/internal/db"
)

var allowedPhotoTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
}

// extForMime returns a safe extension for sniffed photo types.
func extForMime(ct string) string {
	switch ct {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return "bin"
}

// requireOwnedItem writes 404 unless the item belongs to the user; returns it.
func (s *Server) requireOwnedItem(w http.ResponseWriter, r *http.Request, u dbpkg.User) (dbpkg.Item, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return dbpkg.Item{}, false
	}
	item, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return dbpkg.Item{}, false
	}
	return item, true
}

const maxPhotoBytes = 20 << 20
const maxDocBytes = 50 << 20

func (s *Server) handleUploadPhoto(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes)
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

	head := make([]byte, 512)
	n, _ := f.Read(head)
	head = head[:n]
	ct := http.DetectContentType(head)
	if !allowedPhotoTypes[ct] {
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported_type",
			"photos must be jpeg, png, webp, or gif")
		return
	}
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "empty_file", "file is empty")
		return
	}
	// prepend what we already read, then save the rest
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.internalErr(w, err)
		return
	}
	key, _, err := s.st.Save(io.MultiReader(bytes.NewReader(head), f), extForMime(ct))
	if err != nil {
		s.internalErr(w, err)
		return
	}
	photo, err := s.q.AddItemPhoto(r.Context(), dbpkg.AddItemPhotoParams{
		ItemID: item.ID, StorageKey: key, Caption: "", SortOrder: 0,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.st.Delete(key)
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": photo.ID, "storage_key": photo.StorageKey, "caption": photo.Caption,
		"sort_order": photo.SortOrder, "created_at": photo.CreatedAt,
	})
}

type photoPatchReq struct {
	Caption   *string `json:"caption"`
	SortOrder *int64  `json:"sort_order"`
}

func (s *Server) handlePatchPhoto(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	photoID, err := strconv.ParseInt(chi.URLParam(r, "photoId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid photo id")
		return
	}
	photos, err := s.q.ListItemPhotos(r.Context(), item.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	var target *dbpkg.ItemPhoto
	for i := range photos {
		if photos[i].ID == photoID {
			target = &photos[i]
			break
		}
	}
	if target == nil {
		writeErr(w, http.StatusNotFound, "not_found", "photo not found")
		return
	}
	var req photoPatchReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Caption != nil {
		target.Caption = *req.Caption
	}
	if req.SortOrder != nil {
		target.SortOrder = *req.SortOrder
	}
	if err := s.q.UpdateItemPhoto(r.Context(), dbpkg.UpdateItemPhotoParams{
		Caption: target.Caption, SortOrder: target.SortOrder, ID: photoID, ItemID: item.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": target.ID, "storage_key": target.StorageKey, "caption": target.Caption,
		"sort_order": target.SortOrder,
	})
}

func (s *Server) handleDeletePhoto(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	photoID, err := strconv.ParseInt(chi.URLParam(r, "photoId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid photo id")
		return
	}
	photos, _ := s.q.ListItemPhotos(r.Context(), item.ID)
	var key string
	for _, p := range photos {
		if p.ID == photoID {
			key = p.StorageKey
			break
		}
	}
	if key == "" {
		writeErr(w, http.StatusNotFound, "not_found", "photo not found")
		return
	}
	if err := s.q.DeleteItemPhoto(r.Context(), dbpkg.DeleteItemPhotoParams{ID: photoID, ItemID: item.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	s.st.Delete(key)
	w.WriteHeader(http.StatusNoContent)
}

// sanitizeFilename keeps only the base name and safe characters.
func sanitizeFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		return "file"
	}
	return name[:min(len(name), 255)]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
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
	n, _ := f.Read(head)
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "empty_file", "file is empty")
		return
	}
	head = head[:n]
	ct := http.DetectContentType(head)
	if strings.HasPrefix(ct, "text/plain") && header.Header.Get("Content-Type") != "" {
		// keep client hint for text-ish files the sniffer can't distinguish
		if mt, _, err := mime.ParseMediaType(header.Header.Get("Content-Type")); err == nil {
			ct = mt
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.internalErr(w, err)
		return
	}
	key, size, err := s.st.Save(io.MultiReader(bytes.NewReader(head), f), ext)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	doc, err := s.q.AddItemDocument(r.Context(), dbpkg.AddItemDocumentParams{
		ItemID: item.ID, StorageKey: key, Filename: filename,
		MimeType: ct, SizeBytes: size,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.st.Delete(key)
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": doc.ID, "storage_key": doc.StorageKey, "filename": doc.Filename,
		"mime_type": doc.MimeType, "size_bytes": doc.SizeBytes, "created_at": doc.CreatedAt,
	})
}

func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	docID, err := strconv.ParseInt(chi.URLParam(r, "docId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid document id")
		return
	}
	docs, _ := s.q.ListItemDocuments(r.Context(), item.ID)
	var key string
	for _, d := range docs {
		if d.ID == docID {
			key = d.StorageKey
			break
		}
	}
	if key == "" {
		writeErr(w, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err := s.q.DeleteItemDocument(r.Context(), dbpkg.DeleteItemDocumentParams{ID: docID, ItemID: item.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	s.st.Delete(key)
	w.WriteHeader(http.StatusNoContent)
}

// handleFileGet serves /files/{key} to the owner, or to any user if the
// owning item is shared.
func (s *Server) handleFileGet(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	key := chi.URLParam(r, "*")
	if !files.ValidKey(key) {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	ownerID, visibility, ct, filename := int64(0), "", "", ""
	if row, err := s.q.GetPhotoByKey(r.Context(), key); err == nil {
		ownerID, visibility = row.UserID, row.Visibility // ct sniffed at serve time below
	} else if row, err := s.q.GetDocumentByKey(r.Context(), key); err == nil {
		ownerID, visibility, ct, filename = row.UserID, row.Visibility, row.MimeType, row.Filename
	} else {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if ownerID != u.ID && visibility != "shared" {
		writeErr(w, http.StatusNotFound, "not_found", "file not found") // 404, not 403: no existence leak
		return
	}
	f, err := s.st.Open(key)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	defer f.Close()
	if ct == "" || strings.HasPrefix(ct, "application/octet-stream") {
		head := make([]byte, 512)
		n, _ := f.Read(head)
		ct = http.DetectContentType(head[:n])
		f.Seek(0, io.SeekStart)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if filename != "" {
		w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)
	}
	io.Copy(w, f)
}
```

(Note: Go 1.21+ has a builtin `min` — delete the hand-written one at the bottom of the file if it conflicts.)

Routes — authed group:

```go
r.Post("/items/{id}/photos", s.handleUploadPhoto)
r.Patch("/items/{id}/photos/{photoId}", s.handlePatchPhoto)
r.Delete("/items/{id}/photos/{photoId}", s.handleDeletePhoto)
r.Post("/items/{id}/documents", s.handleUploadDocument)
r.Delete("/items/{id}/documents/{docId}", s.handleDeleteDocument)
```

and in `Router()` outside the `/api/v1` block (root level, still authed):

```go
r.With(s.requireAuth).Get("/files/*", s.handleFileGet)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run 'Photo|Document' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: photo/document uploads with gated file serving"
```

---

### Task 16: Projects and project items

**Files:**
- Create: `internal/api/projects_handlers.go`, `internal/api/projects_handlers_test.go`
- Modify: `internal/db/queries.sql` (+10 queries), `internal/api/items_handlers.go` (item detail `projects`), `internal/api/server.go` (routes)

**Interfaces:**
- Consumes: item queries.
- Produces: routes `GET/POST /api/v1/projects`, `GET/PATCH/DELETE /api/v1/projects/{id}`, `POST /api/v1/projects/{id}/items`, `PATCH/DELETE /api/v1/projects/{id}/items/{itemId}`. Item detail now returns `projects: [{id, name, status, qty_used}]`. Queries listed below.

- [ ] **Step 1: Add queries**

```sql
-- name: CreateProject :one
INSERT INTO projects (user_id, name, status, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ? AND user_id = ?;

-- name: ListProjects :many
SELECT * FROM projects WHERE user_id = ? ORDER BY updated_at DESC;

-- name: UpdateProject :exec
UPDATE projects SET name = ?, status = ?, notes = ?, updated_at = ? WHERE id = ? AND user_id = ?;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ? AND user_id = ?;

-- name: UpsertProjectItem :exec
INSERT INTO project_items (project_id, item_id, qty_used, notes, added_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id, item_id) DO UPDATE SET qty_used = excluded.qty_used, notes = excluded.notes;

-- name: DeleteProjectItem :exec
DELETE FROM project_items WHERE project_id = ? AND item_id = ?;

-- name: ListProjectItems :many
SELECT pi.project_id, pi.item_id, pi.qty_used, pi.notes, pi.added_at,
       i.name AS item_name, i.qty AS item_qty, i.unit AS item_unit
FROM project_items pi JOIN items i ON i.id = pi.item_id
WHERE pi.project_id = ?;

-- name: GetItemProjects :many
SELECT p.id, p.name, p.status, pi.qty_used
FROM project_items pi JOIN projects p ON p.id = pi.project_id
WHERE pi.item_id = ?;
```

- [ ] **Step 2: Write the failing tests**

`internal/api/projects_handlers_test.go`:

```go
package api

import (
	"net/http"
	"testing"
)

func TestProjectLifecycle(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "proj@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/projects",
		map[string]any{"name": "Home NAS", "notes": "bulk storage"}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project = %d", resp.StatusCode)
	}
	var proj struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	decodeBody(t, resp, &proj)
	if proj.Status != "planned" {
		t.Fatalf("default status = %q", proj.Status)
	}

	// invalid status rejected
	if r := doJSON(t, ts, "POST", "/api/v1/projects",
		map[string]any{"name": "bad", "status": "wip"}, cookie); r.StatusCode != 400 {
		t.Fatalf("bad status = %d", r.StatusCode)
	}

	// add an item with qty_used
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "8TB HDD"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/items",
		map[string]any{"item_id": created.ID, "qty_used": 4}, cookie); r.StatusCode != 201 {
		t.Fatalf("add project item = %d", r.StatusCode)
	}

	// project detail lists it
	var detail struct {
		Items []struct {
			ItemID  int64   `json:"item_id"`
			ItemName string `json:"item_name"`
			QtyUsed float64 `json:"qty_used"`
			ItemQty float64 `json:"item_qty"`
		} `json:"items"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(proj.ID), nil, cookie), &detail)
	if len(detail.Items) != 1 || detail.Items[0].ItemName != "8TB HDD" || detail.Items[0].QtyUsed != 4 {
		t.Fatalf("project items = %+v", detail.Items)
	}

	// item detail shows the project
	var itemDetail struct {
		Projects []struct {
			ID     int64   `json:"id"`
			Name   string  `json:"name"`
			QtyUsed float64 `json:"qty_used"`
		} `json:"projects"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie), &itemDetail)
	if len(itemDetail.Projects) != 1 || itemDetail.Projects[0].Name != "Home NAS" ||
		itemDetail.Projects[0].QtyUsed != 4 {
		t.Fatalf("item projects = %+v", itemDetail.Projects)
	}

	// update qty_used
	if r := doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(proj.ID)+"/items/"+fmtInt64(created.ID),
		map[string]any{"qty_used": 6}, cookie); r.StatusCode != 204 {
		t.Fatalf("patch project item = %d", r.StatusCode)
	}
	// status update
	if r := doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(proj.ID),
		map[string]any{"status": "active"}, cookie); r.StatusCode != 200 {
		t.Fatalf("patch project = %d", r.StatusCode)
	}

	// list
	var list struct {
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects", nil, cookie), &list)
	if list.Total != 1 {
		t.Fatalf("projects total = %d", list.Total)
	}

	// deleting the project keeps the item, severs the link
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+fmtInt64(proj.ID), nil, cookie); r.StatusCode != 204 {
		t.Fatalf("delete project = %d", r.StatusCode)
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie), &itemDetail)
	if len(itemDetail.Projects) != 0 {
		t.Fatalf("item still linked after project delete: %+v", itemDetail.Projects)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie); r.StatusCode != 200 {
		t.Fatal("item died with project")
	}
}

func TestProjectIsolationAndForeignItem(t *testing.T) {
	_, ts := newTestServer(t, nil)
	a := registerUser(t, ts, "proj-a@test.dev", "longpassword1")
	b := registerUser(t, ts, "proj-b@test.dev", "longpassword1")
	projA := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "A proj"}, a)
	var pa struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, projA, &pa)
	itemB := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "b item"}, b)
	var ib struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, itemB, &ib)

	// a cannot add b's item to a's project
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(pa.ID)+"/items",
		map[string]any{"item_id": ib.ID}, a); r.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign item add = %d, want 404", r.StatusCode)
	}
	// b cannot see a's project
	if r := doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(pa.ID), nil, b); r.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign project = %d, want 404", r.StatusCode)
	}
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/api/ -run Project`
Expected: FAIL — routes 404.

- [ ] **Step 4: Implement**

`internal/api/projects_handlers.go`:

```go
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func validProjectStatus(s string) bool {
	return s == "planned" || s == "active" || s == "done" || s == "archived"
}

func projectJSON(p dbpkg.Project) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "status": p.Status, "notes": p.Notes,
		"created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

type projectReq struct {
	Name   string  `json:"name"`
	Status *string `json:"status"`
	Notes  *string `json:"notes"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req projectReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	status := "planned"
	if req.Status != nil {
		if !validProjectStatus(*req.Status) {
			writeErr(w, http.StatusBadRequest, "validation_error",
				"status must be planned, active, done, or archived")
			return
		}
		status = *req.Status
	}
	notes := ""
	if req.Notes != nil {
		notes = *req.Notes
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p, err := s.q.CreateProject(r.Context(), dbpkg.CreateProjectParams{
		UserID: u.ID, Name: req.Name, Status: status, Notes: notes,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectJSON(p))
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	rows, err := s.q.ListProjects(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, p := range rows {
		out = append(out, projectJSON(p))
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

// requireOwnedProject writes 404 unless the project belongs to the user.
func (s *Server) requireOwnedProject(w http.ResponseWriter, r *http.Request, u dbpkg.User) (dbpkg.Project, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid project id")
		return dbpkg.Project{}, false
	}
	p, err := s.q.GetProject(r.Context(), dbpkg.GetProjectParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "project not found")
		return dbpkg.Project{}, false
	}
	return p, true
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
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
		itemList = append(itemList, map[string]any{
			"item_id": pi.ItemID, "item_name": pi.ItemName,
			"item_qty": pi.ItemQty, "item_unit": pi.ItemUnit,
			"qty_used": qtyUsed, "notes": pi.Notes, "added_at": pi.AddedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": projectJSON(p), "items": itemList,
	})
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	var req projectReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != "" {
		if len(req.Name) > 200 {
			writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
			return
		}
		p.Name = req.Name
	}
	if req.Status != nil {
		if !validProjectStatus(*req.Status) {
			writeErr(w, http.StatusBadRequest, "validation_error",
				"status must be planned, active, done, or archived")
			return
		}
		p.Status = *req.Status
	}
	if req.Notes != nil {
		p.Notes = *req.Notes
	}
	if err := s.q.UpdateProject(r.Context(), dbpkg.UpdateProjectParams{
		Name: p.Name, Status: p.Status, Notes: p.Notes,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: p.ID, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	p, _ = s.q.GetProject(r.Context(), dbpkg.GetProjectParams{ID: p.ID, UserID: u.ID})
	writeJSON(w, http.StatusOK, projectJSON(p))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	if err := s.q.DeleteProject(r.Context(), dbpkg.DeleteProjectParams{ID: p.ID, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type projectItemReq struct {
	ItemID  int64    `json:"item_id"`
	QtyUsed *float64 `json:"qty_used"`
	Notes   string   `json:"notes"`
}

func (s *Server) handleAddProjectItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	var req projectItemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// the item must belong to the same user
	if _, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: req.ItemID, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	var qtyUsed dbpkg.NullFloat64
	if req.QtyUsed != nil {
		qtyUsed = sqlNullFloat64(*req.QtyUsed)
	}
	if err := s.q.UpsertProjectItem(r.Context(), dbpkg.UpsertProjectItemParams{
		ProjectID: p.ID, ItemID: req.ItemID, QtyUsed: qtyUsed, Notes: req.Notes,
		AddedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handlePatchProjectItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "itemId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	var req projectItemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// read current row to merge patch fields
	rows, err := s.q.ListProjectItems(r.Context(), p.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	var cur *dbpkg.ListProjectItemsRow
	for i := range rows {
		if rows[i].ItemID == itemID {
			cur = &rows[i]
			break
		}
	}
	if cur == nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not in project")
		return
	}
	qtyUsed := cur.QtyUsed
	if req.QtyUsed != nil {
		qtyUsed = sqlNullFloat64(*req.QtyUsed)
	}
	notes := cur.Notes
	if req.Notes != "" {
		notes = req.Notes
	}
	if err := s.q.UpsertProjectItem(r.Context(), dbpkg.UpsertProjectItemParams{
		ProjectID: p.ID, ItemID: itemID, QtyUsed: qtyUsed, Notes: notes,
		AddedAt: cur.AddedAt,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteProjectItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "itemId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	if err := s.q.DeleteProjectItem(r.Context(), dbpkg.DeleteProjectItemParams{
		ProjectID: p.ID, ItemID: itemID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Add helper to `respond.go`:

```go
func sqlNullFloat64(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: true} }
```

Update `itemDetailJSON` in `items_handlers.go` — replace `out["projects"] = []map[string]any{}` with:

```go
projects, err := s.q.GetItemProjects(r.Context(), i.ID)
if err != nil {
	return nil, err
}
projList := make([]map[string]any, 0, len(projects))
for _, pj := range projects {
	var qtyUsed any
	if pj.QtyUsed.Valid {
		qtyUsed = pj.QtyUsed.Float64
	}
	projList = append(projList, map[string]any{
		"id": pj.ID, "name": pj.Name, "status": pj.Status, "qty_used": qtyUsed,
	})
}
out["projects"] = projList
```

Routes (authed group):

```go
r.Get("/projects", s.handleListProjects)
r.Post("/projects", s.handleCreateProject)
r.Get("/projects/{id}", s.handleGetProject)
r.Patch("/projects/{id}", s.handleUpdateProject)
r.Delete("/projects/{id}", s.handleDeleteProject)
r.Post("/projects/{id}/items", s.handleAddProjectItem)
r.Patch("/projects/{id}/items/{itemId}", s.handlePatchProjectItem)
r.Delete("/projects/{id}/items/{itemId}", s.handleDeleteProjectItem)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run Project -v && go test ./internal/api/ -v`
Expected: PASS (full suite too — item detail change touches earlier tests).

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: projects with linked items"
```

---

### Task 17: Sharing — browse shared items, copy

**Files:**
- Create: `internal/api/shared_handlers.go`, `internal/api/shared_handlers_test.go`
- Modify: `internal/db/queries.sql` (+1 query), `internal/api/server.go` (routes)

**Interfaces:**
- Consumes: `GetItemAnyOwner` (Task 13), storage copy (Task 5), `setItemTags` (Task 13).
- Produces: routes `GET /api/v1/shared`, `POST /api/v1/items/{id}/copy` (registered before `/items/{id}` routes is fine — chi matches by method too). Copy semantics per spec §6: duplicates row + tags + photo/document **files** (new keys), location dropped (locations are user-scoped), visibility = copier's default, project links NOT copied. Query `ListSharedItemsFromOthers`.

- [ ] **Step 1: Add query**

```sql
-- name: ListSharedItemsFromOthers :many
SELECT i.id, i.name, i.description, i.qty, i.unit, i.location_id, i.visibility,
       i.created_at, i.updated_at, u.display_name AS owner_name,
       (SELECT COUNT(*) FROM item_photos p WHERE p.item_id = i.id) AS photo_count,
       count(*) OVER () AS total_count
FROM items i JOIN users u ON u.id = i.user_id
WHERE i.user_id != @user_id AND i.visibility = 'shared'
  AND (sqlc.narg('q') IS NULL OR i.id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH @q))
ORDER BY i.updated_at DESC
LIMIT @lim OFFSET @off;
```

- [ ] **Step 2: Write the failing tests**

`internal/api/shared_handlers_test.go`:

```go
package api

import (
	"net/http"
	"testing"
)

func TestSharedBrowseAndCopy(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "share-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "share-b@test.dev", "longpassword1")

	// alice: private item + shared item with a tag and a photo
	priv := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "secret"}, alice)
	var privItem struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, priv, &privItem)

	shared := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "soldering iron tips", "qty": 8, "tags": []string{"electronics"},
			"visibility": "shared"}, alice)
	var sharedItem struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, shared, &sharedItem)
	photoResp := postFile(t, ts, "/api/v1/items/"+fmtInt64(sharedItem.ID)+"/photos",
		"file", "tip.png", png1x1, alice)
	var photo struct {
		StorageKey string `json:"storage_key"`
	}
	decodeBody(t, photoResp, &photo)

	// bob browses shared: only the shared item, with owner name
	var list struct {
		Data []struct {
			Name      string `json:"name"`
			OwnerName string `json:"owner_name"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/shared", nil, bob), &list)
	if list.Total != 1 || list.Data[0].Name != "soldering iron tips" ||
		list.Data[0].OwnerName != "Test" {
		t.Fatalf("shared list = %+v", list)
	}

	// copying a PRIVATE item of another user -> 404 (Review Focus #5)
	if r := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(privItem.ID)+"/copy", nil, bob); r.StatusCode != http.StatusNotFound {
		t.Fatalf("copy private = %d, want 404", r.StatusCode)
	}
	// nonexistent -> 404
	if r := doJSON(t, ts, "POST", "/api/v1/items/999999/copy", nil, bob); r.StatusCode != http.StatusNotFound {
		t.Fatalf("copy missing = %d, want 404", r.StatusCode)
	}

	// bob copies the shared item
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(sharedItem.ID)+"/copy", nil, bob)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("copy = %d", resp.StatusCode)
	}
	var copy struct {
		ID int64 `json:"id"`
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Photos []struct {
			StorageKey string `json:"storage_key"`
		} `json:"photos"`
		LocationID any    `json:"location_id"`
		Visibility string `json:"visibility"`
		Qty        float64 `json:"qty"`
	}
	decodeBody(t, resp, &copy)
	if copy.ID == sharedItem.ID {
		t.Fatal("copy reuses item id")
	}
	if len(copy.Tags) != 1 || copy.Tags[0].Name != "electronics" {
		t.Fatalf("copy tags = %+v", copy.Tags)
	}
	if len(copy.Photos) != 1 || copy.Photos[0].StorageKey == photo.StorageKey {
		t.Fatalf("copy photos = %+v (orig key %q)", copy.Photos, photo.StorageKey)
	}
	if copy.LocationID != nil {
		t.Fatalf("copy kept foreign location: %v", copy.LocationID)
	}
	if copy.Visibility != "private" || copy.Qty != 8 {
		t.Fatalf("copy = %+v", copy)
	}
	// copied photo file is really there and independent
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+copy.Photos[0].StorageKey, nil)
	req.AddCookie(bob)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("copied photo fetch: %v %d", err, r.StatusCode)
	}
	// bob owns the copy in his list
	if _, total := listItems(t, ts, bob, ""); total != 1 {
		t.Fatal("copy not in bob's items")
	}

	// alice deletes her original; bob's copy survives with its file
	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(sharedItem.ID), nil, alice)
	req, _ = http.NewRequest("GET", ts.URL+"/files/"+copy.Photos[0].StorageKey, nil)
	req.AddCookie(bob)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("copied photo died with original: %v %d", err, r.StatusCode)
	}
}

func TestCopyOwnItem(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "dup@test.dev", "longpassword1")
	created := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "dup me", "tags": []string{"t"}}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, created, &item)
	if r := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(item.ID)+"/copy", nil, cookie); r.StatusCode != http.StatusCreated {
		t.Fatalf("copy own = %d", r.StatusCode)
	}
	if _, total := listItems(t, ts, cookie, ""); total != 2 {
		t.Fatal("duplicate missing")
	}
}
```

(`postFile`, `png1x1`, `listItems` come from earlier test files in the same package.)

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/api/ -run 'Shared|CopyOwn'`
Expected: FAIL — routes 404.

- [ ] **Step 4: Implement**

`internal/api/shared_handlers.go`:

```go
package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func (s *Server) handleListShared(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p := parsePage(r)
	params := dbpkg.ListSharedItemsFromOthersParams{
		UserID: u.ID, Lim: p.Limit(), Off: p.Offset(),
	}
	if raw := r.URL.Query().Get("q"); raw != "" {
		params.Q = sqlNullString(ftsQuery(raw))
	}
	rows, err := s.q.ListSharedItemsFromOthers(r.Context(), params)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	total := int64(len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id": row.ID, "name": row.Name, "description": row.Description,
			"qty": row.Qty, "unit": row.Unit, "visibility": row.Visibility,
			"photo_count": row.PhotoCount, "owner_name": row.OwnerName,
			"created_at": row.CreatedAt, "updated_at": row.UpdatedAt,
		})
	}
	if len(rows) > 0 {
		total = rows[0].TotalCount
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: total})
}

// handleCopyItem copies an owned item (duplicate) or another user's shared
// item into the caller's inventory.
func (s *Server) handleCopyItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	src, err := s.q.GetItemAnyOwner(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	if src.UserID != u.ID && src.Visibility != "shared" {
		writeErr(w, http.StatusNotFound, "not_found", "item not found") // 404: no existence leak
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	// location is NOT copied: locations are user-scoped
	newItem, err := s.q.CreateItem(r.Context(), dbpkg.CreateItemParams{
		UserID: u.ID, Name: src.Name, Description: src.Description,
		Qty: src.Qty, Unit: src.Unit,
		LocationID: sql.NullInt64{}, Visibility: u.DefaultItemVisibility,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}

	// tags by name (into the copier's tag space)
	if tags, err := s.q.GetItemTags(r.Context(), src.ID); err == nil {
		names := make([]string, 0, len(tags))
		for _, tg := range tags {
			names = append(names, tg.Name)
		}
		if len(names) > 0 {
			if err := s.setItemTags(r, u, newItem.ID, names); err != nil {
				s.tagErr(w, err)
				return
			}
		}
	} else {
		s.internalErr(w, err)
		return
	}

	// photos and documents: physically copy the files
	photos, err := s.q.ListItemPhotos(r.Context(), src.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	for _, ph := range photos {
		newKey, err := s.st.Copy(ph.StorageKey)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		if _, err := s.q.AddItemPhoto(r.Context(), dbpkg.AddItemPhotoParams{
			ItemID: newItem.ID, StorageKey: newKey, Caption: ph.Caption,
			SortOrder: ph.SortOrder, CreatedAt: now,
		}); err != nil {
			s.st.Delete(newKey)
			s.internalErr(w, err)
			return
		}
	}
	docs, err := s.q.ListItemDocuments(r.Context(), src.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	for _, d := range docs {
		newKey, err := s.st.Copy(d.StorageKey)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		if _, err := s.q.AddItemDocument(r.Context(), dbpkg.AddItemDocumentParams{
			ItemID: newItem.ID, StorageKey: newKey, Filename: d.Filename,
			MimeType: d.MimeType, SizeBytes: d.SizeBytes, CreatedAt: now,
		}); err != nil {
			s.st.Delete(newKey)
			s.internalErr(w, err)
			return
		}
	}
	// project links are personal and intentionally NOT copied.

	detail, err := s.itemDetailJSON(r, newItem)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}
```

Routes (authed group):

```go
r.Get("/shared", s.handleListShared)
r.Post("/items/{id}/copy", s.handleCopyItem)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS (full suite).

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: shared items browse and copy"
```

---

### Task 18: main wiring, CLI, SPA serving, Makefile, README

**Files:**
- Modify: `cmd/stasher/main.go` (full rewrite), `Makefile`, create `README.md`

**Interfaces:**
- Consumes: everything above; `internal/webfs` (Task 1).
- Produces: `stasher serve` (default) and `stasher migrate` subcommands; SPA fallback serving `internal/webfs/dist`; graceful shutdown; `-config` flag.

- [ ] **Step 1: Write main.go**

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"stasher/internal/api"
	"stasher/internal/config"
	"stasher/internal/db"
	"stasher/internal/files"
	"stasher/internal/webfs"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file")
	flag.Parse()
	cmd := flag.Arg(0) // "" or "serve" runs the server; "migrate" only migrates

	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}
	setupLogging(cfg.LogLevel)

	dbPath := filepath.Join(cfg.DataDir, "stasher.db")
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		fatal(err)
	}
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(sqlDB); err != nil {
		fatal(err)
	}
	slog.Info("database ready", "path", dbPath)
	if cmd == "migrate" {
		return
	}

	storage, err := files.New(filepath.Join(cfg.DataDir, "uploads"))
	if err != nil {
		fatal(err)
	}
	srv := api.New(&cfg, sqlDB, storage)

	dist, err := fs.Sub(webfs.FS, "dist")
	if err != nil {
		fatal(err)
	}
	fileServer := http.FileServer(http.FS(dist))

	// SPA history-mode fallback: unmatched non-API paths serve index.html.
	// (chi's Mount would swallow an outer NotFound, so the fallback is
	// registered inside the api router itself.)
	srv.SetSPAFallback(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") || strings.HasPrefix(req.URL.Path, "/files/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"code":"not_found","message":"route not found"}}`))
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		req.URL.Path = "/"
		fileServer.ServeHTTP(w, req)
	})

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
}

func fatal(err error) {
	slog.Error(err.Error())
	os.Exit(1)
}

func setupLogging(level string) {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv})))
}
```

Real asset paths (`/assets/...`) match the `http.FileServer` routes inside `webfs.FS`; the fallback only fires for unmatched paths, returning index.html so history-mode routing works. Note `main.go` no longer imports `chi` — remove it from the import block (keep `strings`, `fs`, `webfs`, etc.).

- [ ] **Step 2: Makefile**

```make
.PHONY: build test vet generate run clean

generate:
	go tool sqlc generate

build:
	go build -o stasher ./cmd/stasher

test:
	go vet ./...
	go test ./...

run:
	go run ./cmd/stasher

clean:
	rm -f stasher
```

- [ ] **Step 3: README.md**

```markdown
# Stasher

Self-hosted home inventory: know what you own and where it is.

- Single Go binary + SQLite (WAL) + files on disk — back up one data dir
- Items with quantity, notes, photos, documents (datasheets), tags
- Nested locations (building > cabinet > parts bin), breadcrumbs
- Projects that link items and track quantity used
- Full-text search (SQLite FTS5)
- Multi-user with separate inventories; per-item sharing (view/copy)
- Invite-code registration, admin user management, device session control
- Mobile-friendly (frontend plan); camera photo upload via the browser

## Run

    go build -o stasher ./cmd/stasher
    ./stasher                  # serves on 127.0.0.1:8080, data in ./data
    ./stasher -config stasher.yaml
    ./stasher migrate          # apply migrations and exit

Config: YAML file (`-config path`), env overrides `STASHER_*`
(`LISTEN_ADDR`, `BASE_URL`, `DATA_DIR`, `REGISTRATION`, `LOG_LEVEL`).

The first registered user becomes admin. In `registration: invite` mode the
first user needs no invite code; afterwards admins create codes in the admin UI.

Put Caddy (or any reverse proxy) in front for TLS:

    your.domain.com {
        reverse_proxy 127.0.0.1:8080
    }

## Development

    make generate   # regenerate sqlc code after editing queries.sql
    make test
    make run        # API on :8080; frontend plan adds `npm run dev` proxy
```

- [ ] **Step 4: Verify everything**

Run:

```bash
go vet ./... && go test ./... && go build -o stasher ./cmd/stasher
STASHER_DATA_DIR=/tmp/opencode/stasher-smoke STASHER_LISTEN_ADDR=127.0.0.1:18080 ./stasher &
sleep 1
curl -sf http://127.0.0.1:18080/healthz && curl -sf http://127.0.0.1:18080/api/v1/healthz || true
kill %1
```

Expected: `ok` from `/healthz`, full test suite green, binary builds. (`/api/v1/healthz` may 404 — healthz lives at root by design.)

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: server wiring, cli, spa serving, readme"
```

---

## Post-plan: frontend

The Vue SPA is a separate plan (`docs/superpowers/plans/<date>-stasher-frontend.md`), written after this backend lands, against the API contract in spec §7. This backend is complete, working, tested software on its own.
