-- +goose Up
ALTER TABLE items ADD COLUMN product_url TEXT;

-- +goose Down
ALTER TABLE items DROP COLUMN product_url;
