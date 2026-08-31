-- +goose Up

-- Widen editorial prominence from a boolean to three values.
--
-- Migration 072 added `featured` and argued for a boolean over a rank: "does
-- this deserve the top slot?" was the only question being asked. That is still
-- the only question at the TOP of the scale, and this migration does not
-- reopen it. What 072 could not see is the bottom of the same scale.
--
-- A standing offer -- Double D's half-price calzone on Wednesday, happy hour,
-- kids eat free -- is a real, public, recurring event that belongs on the
-- printed table topper and on the website. But it must never be the headline
-- of its day. If a bike night and a pizza deal both fall on Friday, the card
-- says BIKE NIGHT and mentions the pizza; it must not say PIZZA and mention
-- the bike night. No existing axis can express that: the offer is published,
-- public, onsite and occupies no space, exactly like the bike night.
--
-- Crucially this is NOT a rank. It is three named intents, and the default is
-- the interesting part: a normal public event is ALREADY headline-worthy on
-- its own day. Being featured is a superlative most real events never earn --
-- the anniversary party is featured, the bike night is not, and both headline
-- their day. So callers only ever opt DOWN to background, or UP to featured,
-- and the middle needs no thought. That is what keeps this from becoming the
-- fiddly priority number 072 rightly refused.
--
--   featured   -- the website leads with this. Exactly 072's meaning.
--   normal     -- a real event. Headlines its day. The default.
--   background -- a standing offer. Never headlines.
--
-- The backfill is a pure widening: every existing row keeps its meaning, and
-- the feed still derives its `featured` boolean from prominence = 'featured',
-- so the website's build sees byte-identical output.

ALTER TABLE app_events
    ADD COLUMN prominence TEXT NOT NULL DEFAULT 'normal'
        CHECK (prominence IN ('featured', 'normal', 'background'));

UPDATE app_events SET prominence = 'featured' WHERE featured;

-- The website asks "is there a featured event coming up?" on every build, and
-- the answer is almost always a handful of rows out of the whole table. Same
-- index as 072, repointed at the new column.
DROP INDEX IF EXISTS idx_app_events_featured;
CREATE INDEX idx_app_events_featured
    ON app_events (tenant_id, starts_at)
    WHERE prominence = 'featured';

-- One source of truth. Keeping the boolean alongside would let the two drift,
-- and every read would have to decide which one it believed.
ALTER TABLE app_events DROP COLUMN featured;

-- +goose Down

ALTER TABLE app_events
    ADD COLUMN featured BOOLEAN NOT NULL DEFAULT false;

UPDATE app_events SET featured = true WHERE prominence = 'featured';

DROP INDEX IF EXISTS idx_app_events_featured;
CREATE INDEX idx_app_events_featured
    ON app_events (tenant_id, starts_at)
    WHERE featured;

-- Background collapses into a normal event on the way down. Nothing else can
-- carry it, and the topper is the only reader that cares.
ALTER TABLE app_events DROP COLUMN prominence;
