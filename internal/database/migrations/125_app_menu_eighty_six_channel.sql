-- +goose Up

-- The Slack channel told when a beer is 86'd. Empty means nowhere, which is
-- where every workspace starts. channel_name is for display.
ALTER TABLE app_menu_eighty_six
    ADD COLUMN channel_id   TEXT NOT NULL DEFAULT '',
    ADD COLUMN channel_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app_menu_eighty_six
    DROP COLUMN IF EXISTS channel_id,
    DROP COLUMN IF EXISTS channel_name;
