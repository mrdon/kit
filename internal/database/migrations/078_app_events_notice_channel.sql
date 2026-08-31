-- +goose Up

-- Where the daily shift notice gets posted.
--
-- Notices started as a DM to each person working. That delivered the
-- information but left nowhere to answer a question: "do we have the second
-- mic?" got asked in a 1:1 and the answer reached one person. A channel post
-- puts the day somewhere the whole team can talk about it, and threads keep
-- that conversation off the channel's top level.
--
-- Empty means notices are off. There is no sensible default channel to guess
-- at, and posting a private booking into the wrong room is not a mistake worth
-- risking to save one dropdown.
ALTER TABLE app_event_settings
    ADD COLUMN notice_channel_id   TEXT NOT NULL DEFAULT '',
    ADD COLUMN notice_channel_name TEXT NOT NULL DEFAULT '';

-- The notice log moves from per-person to per-day: one post now covers
-- everyone, so "has today already gone out" is a question about the day rather
-- than about a reader.
--
-- Safe to drop the column outright -- notices are keyed on a staff mapping
-- that no workspace had configured yet, so nothing has been recorded.
ALTER TABLE app_events_shift_notices
    DROP CONSTRAINT IF EXISTS app_events_shift_notices_tenant_id_user_id_notice_date_key;

ALTER TABLE app_events_shift_notices
    DROP COLUMN IF EXISTS user_id;

-- channel_message_id is the top-level post's ts, which is also the thread
-- anchor the detail replies hang from.
ALTER TABLE app_events_shift_notices
    ADD COLUMN IF NOT EXISTS channel_message_id TEXT NOT NULL DEFAULT '';

-- Clear the table before the new constraint goes on. Under the old scheme a
-- day could hold one row per person, so any surviving rows would collide on
-- (tenant_id, notice_date) and abort the migration mid-deploy.
--
-- Deleting rather than de-duplicating is right on the merits, not just
-- convenient: a per-person row records "this reader was told", which says
-- nothing about whether the channel has been posted to. The worst case is one
-- notice re-posted on the day of the deploy.
DELETE FROM app_events_shift_notices;

ALTER TABLE app_events_shift_notices
    ADD CONSTRAINT app_events_shift_notices_tenant_date_key
    UNIQUE (tenant_id, notice_date);

-- +goose Down

ALTER TABLE app_events_shift_notices
    DROP CONSTRAINT IF EXISTS app_events_shift_notices_tenant_date_key;

ALTER TABLE app_events_shift_notices
    DROP COLUMN IF EXISTS channel_message_id;

ALTER TABLE app_events_shift_notices
    ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE app_event_settings
    DROP COLUMN IF EXISTS notice_channel_id,
    DROP COLUMN IF EXISTS notice_channel_name;
