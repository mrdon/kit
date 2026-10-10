-- +goose Up
-- The hourly Drive sync is opt-in. Most workspaces index photos from a
-- harness over MCP, which lists the folder itself with sync_poster_photos,
-- so Kit has no reason to poll Drive on its own unless an admin asks.
ALTER TABLE app_poster_settings ADD COLUMN auto_sync BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE app_poster_settings DROP COLUMN auto_sync;
