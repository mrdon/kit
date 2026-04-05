-- +goose Up
ALTER TABLE tasks ADD COLUMN run_once BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE tasks DROP COLUMN IF EXISTS run_once;
