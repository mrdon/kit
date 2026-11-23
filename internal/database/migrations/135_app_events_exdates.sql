-- +goose Up

-- exdates are the dates a repeating event is SKIPPED: trivia is off on
-- Christmas Eve, the taproom is closed on New Year's Day. RFC 5545 calls them
-- EXDATE, and they are the mirror of rdates (migration 076): rdates adds
-- dates no rule expresses, exdates removes dates a rule would otherwise hit.
--
-- Stored as the occurrence's start instant, but MATCHED by local calendar
-- date in the event's zone. That way moving trivia from 6:30 to 7pm keeps
-- Christmas Eve skipped instead of quietly bringing it back.
ALTER TABLE app_events
    ADD COLUMN exdates TIMESTAMPTZ[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE app_events DROP COLUMN IF EXISTS exdates;
