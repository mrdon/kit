-- +goose Up
ALTER TABLE tasks ADD COLUMN task_type TEXT NOT NULL DEFAULT 'agent';

-- +goose Down
ALTER TABLE tasks DROP COLUMN IF EXISTS task_type;
