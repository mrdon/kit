-- +goose Up

-- Idempotency log for the send_email PolicyGate tool handler. The card
-- approve path passes an approval.Token whose ResolveToken is the dedupe
-- key: handler writes a claim row BEFORE calling SMTP, so a stuck-resolving
-- retry finds the prior message_id and skips re-sending. Primary key on
-- resolve_token gives us the upsert-probe semantics for free.
CREATE TABLE app_email_sent_messages (
    resolve_token UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    message_id    TEXT NOT NULL,
    sent_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_email_sent_tenant ON app_email_sent_messages(tenant_id);

-- +goose Down

DROP TABLE IF EXISTS app_email_sent_messages;
