-- +goose Up

-- Website publishing state for the events app.
--
-- The website is a static site: it only shows what it was built with, so an
-- event edited in Kit stays invisible on the web until a build runs. These
-- columns record when that last happened.
--
-- What changed since then is NOT stored here. It is derived from audit_events,
-- which already exists and is already namespaced per app -- so the "pending
-- changes" list is a query over the real history rather than a second copy of
-- it that can drift. It also means the list survives as an audit trail after
-- the build, instead of being consumed by it.

ALTER TABLE app_event_settings
    -- Netlify build hook, pasted by an admin (Site configuration → Build &
    -- deploy → Build hooks). Empty means publishing is not wired up yet, which
    -- the UI reports rather than failing on.
    ADD COLUMN site_build_hook_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN site_built_at       TIMESTAMPTZ,
    -- Who asked for the last build, for the history line.
    ADD COLUMN site_built_by       TEXT NOT NULL DEFAULT '';

-- Pending-changes queries hit audit_events filtered by tenant, action prefix
-- and time. The existing index leads with tenant_id; this one makes the
-- action+time half cheap as the log grows.
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_action_time
    ON audit_events (tenant_id, action, created_at DESC);

-- +goose Down

DROP INDEX IF EXISTS idx_audit_events_tenant_action_time;

ALTER TABLE app_event_settings
    DROP COLUMN IF EXISTS site_build_hook_url,
    DROP COLUMN IF EXISTS site_built_at,
    DROP COLUMN IF EXISTS site_built_by;
