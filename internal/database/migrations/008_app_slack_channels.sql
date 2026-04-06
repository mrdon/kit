-- +goose Up

-- app_slack_channels: channels configured for message search
CREATE TABLE app_slack_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    slack_channel_id TEXT NOT NULL,
    channel_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, slack_channel_id)
);

-- app_slack_channel_scopes: role-based access (reuses standard scope pattern)
CREATE TABLE app_slack_channel_scopes (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES app_slack_channels(id) ON DELETE CASCADE,
    scope_type TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    PRIMARY KEY(tenant_id, channel_id, scope_type, scope_value)
);

-- +goose Down
DROP TABLE IF EXISTS app_slack_channel_scopes;
DROP TABLE IF EXISTS app_slack_channels;
