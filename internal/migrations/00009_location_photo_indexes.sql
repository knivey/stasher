-- +goose Up
CREATE INDEX idx_locations_photo_key ON locations(photo_key);
CREATE INDEX idx_locations_photo_thumb_key ON locations(photo_thumb_key);

-- +goose Down
DROP INDEX idx_locations_photo_thumb_key;
DROP INDEX idx_locations_photo_key;
