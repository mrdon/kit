-- +goose Up
ALTER TABLE tasks ADD COLUMN model text NOT NULL DEFAULT 'haiku';

-- +goose Down
ALTER TABLE tasks DROP COLUMN IF EXISTS model;
