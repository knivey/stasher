-- +goose Up
ALTER TABLE locations ADD COLUMN photo_key TEXT;
ALTER TABLE locations ADD COLUMN photo_thumb_key TEXT;

-- +goose Down
ALTER TABLE locations DROP COLUMN photo_thumb_key;
ALTER TABLE locations DROP COLUMN photo_key;
