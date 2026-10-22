-- +goose Up
ALTER TABLE item_photos ADD COLUMN thumb_key TEXT;

-- +goose Down
ALTER TABLE item_photos DROP COLUMN thumb_key;
