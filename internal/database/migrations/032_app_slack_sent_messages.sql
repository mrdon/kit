-- +goose Up

-- Idempotency log for post_to_channel / dm_user when gated via
-- require_approval. The card approve path passes an approval.Token whose
-- ResolveToken is the dedupe key: handler writes a claim row BEFORE
-- calling Slack, so a stuck-resolving retry finds the prior slack_ts and
-- skips re-posting. Primary key on resolve_token gives us upsert-probe
-- semantics for free. Shared by both send tools since the side effect
-- (a Slack chat.postMessage call) is identical in shape.
CREATE TABLE app_slack_sent_messages (
    resolve_token UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    tool_name     TEXT NOT NULL,
    channel_id    TEXT NOT NULL,
    slack_ts      TEXT NOT NULL DEFAULT '',
    sent_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_slack_sent_tenant ON app_slack_sent_messages(tenant_id);

-- +goose Down

DROP TABLE IF EXISTS app_slack_sent_messages;
