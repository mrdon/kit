-- +goose Up

-- Maps a Square team member to the Kit user Kit should DM about the events
-- happening on that person's shift.
--
-- Set by hand on the admin page rather than derived. The two systems share no
-- joinable key: Square carries payroll identities and Kit carries Slack ones,
-- and matching on email would need users:read.email on the Slack app (a
-- reinstall) while still failing for anyone whose payroll address differs from
-- their work one. A taproom has a handful of staff, so one dropdown per person
-- is cheaper than either and never silently misdelivers.
--
-- Keyed on square_team_member_id, not the member's name: names are mutable and
-- not unique, and a rename must not quietly redirect someone's shift notices.
--
-- Unique in BOTH directions. Two Square members pointing at one Kit user would
-- DM that person twice for the same day; one member pointing at two users
-- would leak a shift to someone who is not working it.
CREATE TABLE app_events_staff_map (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    square_team_member_id TEXT NOT NULL,
    user_id               UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, square_team_member_id),
    UNIQUE (tenant_id, user_id)
);

-- Records one delivered shift notice so a retry, a redeploy, or a second cron
-- tick cannot DM the same person about the same day twice.
--
-- content_hash digests what the message said. An event added or moved after
-- the morning send changes the hash, which is what lets a later run decide to
-- follow up rather than staying silent on a day that has genuinely changed.
CREATE TABLE app_events_shift_notices (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notice_date  DATE NOT NULL,
    content_hash TEXT NOT NULL,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, user_id, notice_date)
);

CREATE INDEX app_events_shift_notices_date
    ON app_events_shift_notices (tenant_id, notice_date);

-- +goose Down

DROP TABLE IF EXISTS app_events_shift_notices;
DROP TABLE IF EXISTS app_events_staff_map;
