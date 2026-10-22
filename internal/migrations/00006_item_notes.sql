-- +goose Up
CREATE TABLE item_notes (
    id INTEGER PRIMARY KEY,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_item_notes_item ON item_notes(item_id);

-- +goose Down
DROP TABLE item_notes;
