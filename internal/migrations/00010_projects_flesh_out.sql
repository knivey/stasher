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
