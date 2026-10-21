-- +goose Up
CREATE TABLE trash (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    snapshot TEXT NOT NULL,
    trashed_at TEXT NOT NULL,
    purges_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_trash_user ON trash(user_id);

-- +goose Down
DROP TABLE trash;
