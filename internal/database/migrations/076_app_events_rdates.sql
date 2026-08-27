-- +goose Up

-- Explicit repeat dates, for series a rule cannot express.
--
-- The weekly rule in migration 070 covers trivia and nothing else. The cases
-- it cannot reach are the ordinary ones: a supper club on dates chosen around
-- the chef's availability, a five-week beer school with a gap over a holiday,
-- a market that moves whenever the square is booked. Each was previously
-- authored as N unrelated events, which is how one page becomes five URLs and
-- a poster gets uploaded five times.
--
-- rdates holds the ADDITIONAL dates. starts_at remains the first occurrence,
-- exactly as RFC 5545 treats DTSTART, so every existing query that orders or
-- filters on starts_at keeps working untouched. The service layer normalises
-- on write -- the combined set is sorted, deduped, and the earliest becomes
-- starts_at -- so `rdates` is always strictly after starts_at no matter what
-- order a caller supplied.
--
-- An array rather than a child table. The usual rule here is that a
-- tenant-scoped child table carries its own tenant_id; this sidesteps that
-- because there is no child entity -- a date carries no attributes of its own,
-- has no identity worth referencing, and is never queried independently of its
-- event. When a date needs its own title or poster (the December market that
-- becomes a holiday market), that is a per-occurrence OVERRIDE table, which is
-- a different thing from this list and can be added alongside it.
ALTER TABLE app_events
    ADD COLUMN rdates TIMESTAMPTZ[] NOT NULL DEFAULT '{}';

-- Mirrors app_events_no_recurring_all_day. RFC 5545 requires RDATE values to
-- match DTSTART's type -- DATE for an all-day event, DATE-TIME for a timed one
-- -- and supporting both shapes doubles the validation surface for a case that
-- does not exist here. Relaxing this later is additive.
ALTER TABLE app_events
    ADD CONSTRAINT app_events_no_all_day_rdates
        CHECK (cardinality(rdates) = 0 OR all_day = false);

-- The partial index from 070 found recurring rows by `rrule IS NOT NULL`. A
-- date-list event is equally exempt from an ordinary starts_at lower bound --
-- its first date may be months past while later ones are still ahead -- so it
-- has to be findable the same way.
DROP INDEX IF EXISTS app_events_recurring;
CREATE INDEX app_events_recurring
    ON app_events (tenant_id)
    WHERE rrule IS NOT NULL OR cardinality(rdates) > 0;

-- +goose Down

DROP INDEX IF EXISTS app_events_recurring;
CREATE INDEX app_events_recurring
    ON app_events (tenant_id) WHERE rrule IS NOT NULL;

ALTER TABLE app_events DROP CONSTRAINT IF EXISTS app_events_no_all_day_rdates;
ALTER TABLE app_events DROP COLUMN IF EXISTS rdates;
