-- +goose Up
-- Each version remembers the event facts its copy was written from, so
-- setting an older option on the event compares those facts, not the
-- live ones, and a stale pick is flagged at once.
ALTER TABLE app_poster_versions
    ADD COLUMN facts      JSONB,
    ADD COLUMN facts_hash TEXT NOT NULL DEFAULT '';
-- The attachment Posters put on the event. When the event's poster image
-- becomes something else (removed, or uploaded by hand), the link clears.
ALTER TABLE app_posters ADD COLUMN set_attachment_id UUID;

-- +goose Down
ALTER TABLE app_posters DROP COLUMN set_attachment_id;
ALTER TABLE app_poster_versions DROP COLUMN facts, DROP COLUMN facts_hash;
