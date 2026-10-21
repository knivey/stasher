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
