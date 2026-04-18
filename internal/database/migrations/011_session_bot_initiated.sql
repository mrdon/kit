-- +goose Up
ALTER TABLE sessions ADD COLUMN bot_initiated BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE sessions DROP COLUMN IF EXISTS bot_initiated;
