-- +goose Up

-- A fourth prominence, below background: the food partner's standing offer.
--
-- Migration 079 widened this axis to three and said the default was the
-- interesting part. It was right, and this does not reopen the top of the
-- scale. What 079 could not see is that `background` was carrying two
-- different things that only look alike from a distance.
--
-- Both are standing offers that must never headline a day with a real event
-- on it, so 079 filed them together. But on a day where there is no real
-- event, one of them still has to headline, and then the difference matters:
--
--   MON  BOGO PIZZA                  <- Double D's runs this
--        - Also: Monday Night Football
--
-- Monday Night Football is ours. The pizza deal is our food partner's, sold
-- in our room. The card was choosing between them on door time alone -- the
-- pizza opens at 4pm, the football at 6pm -- which is not a judgement about
-- what the taproom is for, it is an accident of the clock.
--
-- So the axis gets a floor below its floor:
--
--   featured   -- the website leads with this
--   normal     -- a real event. Headlines its day. The default.
--   background -- OUR standing thing: NFL Sundays, happy hour, a cask tapping
--   amenity    -- a PARTNER's standing offer: the pizza deal, the food truck
--
-- Still not a rank, and still opt-down-only, which is what keeps it from
-- becoming the priority number 072 refused and 079 declined to revisit. The
-- question a caller answers is "whose thing is this, and is it a happening?"
-- -- which has an obvious answer per event and never needs revisiting. A
-- number would ask "how important, out of how many?" on every event forever.
--
-- Nothing is backfilled. Every existing row keeps the value it has, including
-- the food deals: `amenity` is a judgement someone makes per event, and
-- guessing it from a title is exactly the kind of inference that leaves a
-- column nobody trusts.

ALTER TABLE app_events
    DROP CONSTRAINT IF EXISTS app_events_prominence_check;

ALTER TABLE app_events
    ADD CONSTRAINT app_events_prominence_check
        CHECK (prominence IN ('featured', 'normal', 'background', 'amenity'));

-- +goose Down

-- Amenity collapses back into background, which is where 079 had it: still a
-- standing offer, still never the headline of a day with a real event on it.
-- The only thing lost is which side of the bar it came from.
UPDATE app_events SET prominence = 'background' WHERE prominence = 'amenity';

ALTER TABLE app_events
    DROP CONSTRAINT IF EXISTS app_events_prominence_check;

ALTER TABLE app_events
    ADD CONSTRAINT app_events_prominence_check
        CHECK (prominence IN ('featured', 'normal', 'background'));
