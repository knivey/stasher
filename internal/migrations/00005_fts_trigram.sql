-- +goose Up
-- Switch the items search index to the trigram tokenizer so q=<substring>
-- matches inside words (q=raspb -> "raspberry pi"). Trigram needs >= 3
-- characters per query token; shorter queries match nothing (documented in
-- docs/api-notes.md). External-content table + sync triggers keep the index
-- in sync with items, so the DDL is identical to 00001 apart from the
-- tokenizer, followed by a full rebuild for existing rows.
DROP TRIGGER items_au;
DROP TRIGGER items_ad;
DROP TRIGGER items_ai;
DROP TABLE items_fts;

CREATE VIRTUAL TABLE items_fts USING fts5(
    name, description, content='items', content_rowid='id', tokenize='trigram'
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

INSERT INTO items_fts(rowid, name, description) SELECT id, name, description FROM items;

-- +goose Down
-- Mirror of Up: restore the default (unicode61) tokenizer table and repopulate.
DROP TRIGGER items_au;
DROP TRIGGER items_ad;
DROP TRIGGER items_ai;
DROP TABLE items_fts;

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

INSERT INTO items_fts(rowid, name, description) SELECT id, name, description FROM items;
