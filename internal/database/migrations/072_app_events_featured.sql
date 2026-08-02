-- +goose Up

-- Editorial prominence, separate from every other axis on an event.
--
-- The website leads with one event. Picking the soonest is right most of the
-- time and wrong exactly when it matters: a weekly quiz night on Wednesday
-- outranks the anniversary party in September, even though the anniversary is
-- the thing worth putting in front of a visitor.
--
-- No existing column can carry this. status/visibility are about exposure,
-- space_impact and expected_attendance are operational facts for staff, and
-- price is an attribute. This is a judgement call by whoever runs the events,
-- so it gets its own flag rather than being inferred from any of those.
--
-- A boolean, not a priority number: "does this deserve the top slot?" is the
-- only question being asked, and ranks invite fiddling without answering it
-- any better. Several may be flagged at once -- the site leads with the NEXT
-- featured event -- so a season of highlights can be marked up front and left
-- alone rather than reshuffled as each one passes.

ALTER TABLE app_events
    ADD COLUMN featured BOOLEAN NOT NULL DEFAULT false;

-- The website asks "is there a featured event coming up?" on every build, and
-- the answer is almost always a handful of rows out of the whole table.
CREATE INDEX idx_app_events_featured
    ON app_events (tenant_id, starts_at)
    WHERE featured;

-- +goose Down

DROP INDEX IF EXISTS idx_app_events_featured;
ALTER TABLE app_events DROP COLUMN IF EXISTS featured;
