-- +goose Up

-- The website chat widget runs anonymous conversations — no Slack user
-- is associated with the visitor. Rather than synthesise a fake user
-- row, allow sessions.user_id to be NULL but only for widget sessions.
ALTER TABLE sessions ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE sessions ADD CONSTRAINT sessions_user_or_widget
    CHECK (user_id IS NOT NULL OR slack_channel_id = 'web:widget');

-- +goose Down
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_user_or_widget;
-- Note: re-adding NOT NULL would fail if any widget sessions exist.
ALTER TABLE sessions ALTER COLUMN user_id SET NOT NULL;
