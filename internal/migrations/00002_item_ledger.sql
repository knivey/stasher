-- +goose Up
CREATE TABLE item_ledger (
    id INTEGER PRIMARY KEY,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delta NUMERIC NOT NULL,
    resulting_qty NUMERIC NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('used','sold','lost','found','restock','disposed','adjustment')),
    project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX idx_item_ledger_item ON item_ledger(item_id);
CREATE INDEX idx_item_ledger_user ON item_ledger(user_id);
ALTER TABLE items ADD COLUMN disposed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
DROP TABLE item_ledger;
ALTER TABLE items DROP COLUMN disposed;
