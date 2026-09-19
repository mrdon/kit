-- +goose Up

-- The other half of migration 102.
--
-- 102 widened app_events.prominence to take `amenity` and stopped there,
-- because the Go side was read for every place it branched on the value. It
-- was not read for every place the SCHEMA constrains it, and there was a
-- second one: a promotion channel's floor is the same vocabulary, written out
-- again in migration 087.
--
-- The failure was quiet in the right way and loud in the wrong one -- nothing
-- was mis-stored, the insert simply refused -- but it refused at the point
-- someone was setting up a destination rather than at deploy, which is the
-- worst place to find out that half a value exists.
--
-- A floor of `amenity` means "absolutely everything, the food partner's deals
-- included". That is what a channel sitting at the old `background` floor was
-- already receiving, so anywhere that wants to keep what it has moves down to
-- this one.

ALTER TABLE app_event_channels
    DROP CONSTRAINT IF EXISTS app_event_channels_min_prominence_check;

ALTER TABLE app_event_channels
    ADD CONSTRAINT app_event_channels_min_prominence_check
        CHECK (min_prominence IN ('amenity', 'background', 'normal', 'featured'));

-- +goose Down

-- A floor of amenity becomes background on the way down: still the lowest bar
-- the column can express, so the channel keeps taking everything it can.
UPDATE app_event_channels SET min_prominence = 'background' WHERE min_prominence = 'amenity';

ALTER TABLE app_event_channels
    DROP CONSTRAINT IF EXISTS app_event_channels_min_prominence_check;

ALTER TABLE app_event_channels
    ADD CONSTRAINT app_event_channels_min_prominence_check
        CHECK (min_prominence IN ('background', 'normal', 'featured'));
