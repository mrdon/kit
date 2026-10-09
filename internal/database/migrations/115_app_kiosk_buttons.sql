-- +goose Up

-- A workspace's own buttons for repointing a kiosk screen.
--
-- Kit's screen pages (menu, events, trivia) are offered as buttons out of the
-- box; this table holds the rest -- an outside page a venue switches to often
-- enough that typing its address each time is the wrong interface. A button is
-- just a name and a URL. Pressing it writes that URL into the board like a
-- typed address would, so deleting a button never changes what a screen shows.
CREATE TABLE app_kiosk_buttons (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    label      TEXT NOT NULL,
    url        TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_app_kiosk_buttons_tenant ON app_kiosk_buttons (tenant_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS app_kiosk_buttons;
