-- +goose Up

-- A paired device's chat session has no user either: it runs as the device,
-- which actor_kind/actor_label record. Widget sessions stay as they were.
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_user_or_widget;
ALTER TABLE sessions ADD CONSTRAINT sessions_user_or_widget
    CHECK (user_id IS NOT NULL OR slack_channel_id = 'web:widget' OR actor_kind = 'device');

-- +goose Down
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_user_or_widget;
ALTER TABLE sessions ADD CONSTRAINT sessions_user_or_widget
    CHECK (user_id IS NOT NULL OR slack_channel_id = 'web:widget');
