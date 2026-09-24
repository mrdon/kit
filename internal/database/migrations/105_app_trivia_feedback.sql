-- +goose Up

-- Where end-of-night trivia ratings are posted, per workspace. The ratings
-- themselves are not stored: Slack is the record.
--
-- There is no default: which channel the people running the night read is
-- the workspace's business, so until an admin picks one nothing is posted.
-- The name is kept alongside the id for display only.
CREATE TABLE app_trivia_settings (
    tenant_id             UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    feedback_channel_id   TEXT NOT NULL DEFAULT '',
    feedback_channel_name TEXT NOT NULL DEFAULT '',
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE app_trivia_settings;
