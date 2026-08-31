-- +goose Up

-- Previous URLs a kiosk board pointed at, newest first.
--
-- This exists for one specific accident: you paste a new URL over a board's
-- old one, the new page turns out to be wrong, and the old URL is gone
-- because it only ever lived in that field. Nothing else in the system
-- remembers it -- the kiosk machine holds a resolved target, not a history,
-- and last_seen_at says a screen polled, not what it saw. So the swap itself
-- records what it replaced.
--
-- Deliberately NOT a full audit log. It answers "put it back" and nothing
-- else: no actor, no reason, no retention policy beyond a hard cap of the few
-- most recent entries (see kiosk.HistoryDepth), trimmed on write. A board
-- that gets repointed weekly should not accumulate rows forever to serve a
-- rollback that only ever reaches for the last one or two.
CREATE TABLE app_kiosk_board_urls (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    board_id    UUID NOT NULL REFERENCES app_kiosk_boards(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    replaced_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The listing: one board's history, newest first.
CREATE INDEX idx_app_kiosk_board_urls_board
    ON app_kiosk_board_urls (tenant_id, board_id, replaced_at DESC);

-- +goose Down
DROP TABLE IF EXISTS app_kiosk_board_urls;
