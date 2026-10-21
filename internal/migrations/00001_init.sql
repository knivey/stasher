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

-- +goose StatementBegin
CREATE TRIGGER items_ai AFTER INSERT ON items BEGIN
    INSERT INTO items_fts(rowid, name, description) VALUES (new.id, new.name, new.description);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER items_ad AFTER DELETE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, name, description)
        VALUES ('delete', old.id, old.name, old.description);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER items_au AFTER UPDATE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, name, description)
        VALUES ('delete', old.id, old.name, old.description);
    INSERT INTO items_fts(rowid, name, description) VALUES (new.id, new.name, new.description);
END;
-- +goose StatementEnd

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
