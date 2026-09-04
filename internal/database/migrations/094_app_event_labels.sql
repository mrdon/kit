-- +goose Up

-- Labels: what KIND of thing an event is.
--
-- The events model already carries three classification axes, and none of them
-- can answer this question:
--
--   visibility  -- who may see it (public / private)
--   venue       -- whose room it is in (onsite / offsite)
--   prominence  -- how loudly it speaks (featured / normal / background)
--
-- All three are closed sets with exactly one value, because each answers a
-- question the system itself has to act on. "What kind of thing is this" is not
-- one of those. It is open-ended, an event can be several at once, and nothing
-- in Kit needs to branch on it -- but a downstream reader does. The website
-- wants the Give Back nights on their own page, the table topper may one day
-- want to group the food offers, and neither is expressible today without
-- abusing one of the three axes above.
--
-- That abuse is the thing this migration exists to prevent. The alternative on
-- offer was to overload `prominence`, or add a fourth enum with a value per
-- programme, and both end with someone marking a real event `background`
-- purely to keep it off a list -- which would silently strip its schema.org
-- Event markup, because that gate reads the same column. Prominence must keep
-- meaning exactly one thing.
--
-- TEXT[] rather than a join table. There is no label entity to speak of: no
-- name, no colour, no per-label settings, nothing to rename in one place. A
-- join table would buy referential integrity over a vocabulary nobody has
-- agreed on yet, at the cost of a table, two more queries and a migration the
-- next time someone wants a label that does not exist. Normalisation happens in
-- Go on write (lowercased, hyphenated, deduped) so the array stays a clean set.
--
-- NOT NULL DEFAULT '{}' rather than nullable, so every read gets a slice and no
-- caller has to distinguish "no labels" from "labels unknown". Existing rows
-- backfill to empty, which is correct: nothing was labelled before now.
--
-- No index. Every query that could filter on labels today fetches a tenant's
-- events anyway and filters in the consumer -- the website does it in its
-- template, and a tenant's event table is small. A GIN index here would be
-- indexing a column nothing yet queries. Add one alongside the first query
-- that needs it, the way 072 added its partial index for a real question.

ALTER TABLE app_events
    ADD COLUMN labels TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down

ALTER TABLE app_events DROP COLUMN labels;
