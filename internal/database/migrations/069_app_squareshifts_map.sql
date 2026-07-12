-- +goose Up

-- Mapping from a Square published scheduled shift to the Google Calendar
-- event Kit wrote for it. Event ids are deterministic (base32hex of the
-- shift id), so this table isn't strictly required for idempotent upserts —
-- but it makes "which shifts have I synced" and "which shifts vanished from
-- the published window" cheap without scanning the calendar, and it records
-- the content hash so an unchanged shift is skipped rather than re-patched.
--
-- start_at is the shift's start instant; delete-detection only considers
-- mappings whose start_at falls in the current rolling sync window, so past
-- events age out on their own and are never deleted as "vanished".
CREATE TABLE app_squareshifts_map (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    shift_id        TEXT NOT NULL,
    google_event_id TEXT NOT NULL,
    start_at        TIMESTAMPTZ NOT NULL,
    version         INT NOT NULL DEFAULT 0,
    content_hash    TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, shift_id)
);

CREATE INDEX app_squareshifts_map_window
    ON app_squareshifts_map (tenant_id, start_at);

-- +goose Down

DROP TABLE IF EXISTS app_squareshifts_map;
