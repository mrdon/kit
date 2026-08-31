-- +goose Up

-- Where a menu board's tap list comes from, and how the last pull went.
--
-- Kit scrapes the workspace's Untappd board and rewrites `taps` on a
-- schedule. Staff already keep that board current -- it is what feeds the
-- Untappd app -- so this reads their existing work instead of asking anyone
-- to maintain the same seventeen beers in two places.
--
-- `venue` and `panels` in the payload are NOT touched by a sync. Those are
-- presentation (the wordmark, the footer rules, the rotating panels) and have
-- no upstream; only the tap list is pulled.
--
-- synced_at and sync_error exist because this is a scraper against a page
-- nobody promised us. When Untappd reskins the board the parse will fail, and
-- the only way anyone finds out before a customer does is a visible last
-- error next to the board.
ALTER TABLE app_menu_boards
    ADD COLUMN source_kind TEXT NOT NULL DEFAULT '',
    ADD COLUMN source_id   TEXT NOT NULL DEFAULT '',
    ADD COLUMN synced_at   TIMESTAMPTZ,
    ADD COLUMN sync_error  TEXT NOT NULL DEFAULT '',
    -- Hash of the upstream page as last seen. Untappd serves the board with
    -- cache-control: no-cache and no ETag, so there is no conditional request
    -- to make -- the bytes come down every minute either way. Comparing a
    -- hash is what makes the unchanged case free: it skips the parse, the
    -- validate, the encode and the UPDATE, which is all the work but the
    -- transfer.
    ADD COLUMN source_hash TEXT NOT NULL DEFAULT '';

-- The scheduled pass asks "which boards have a source?" once per tick.
CREATE INDEX idx_app_menu_boards_source
    ON app_menu_boards (tenant_id) WHERE source_kind <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_app_menu_boards_source;
ALTER TABLE app_menu_boards
    DROP COLUMN IF EXISTS source_kind,
    DROP COLUMN IF EXISTS source_id,
    DROP COLUMN IF EXISTS synced_at,
    DROP COLUMN IF EXISTS sync_error;
