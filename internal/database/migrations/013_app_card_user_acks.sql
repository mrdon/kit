-- +goose Up

-- Per-user briefing acknowledgments. Briefings scoped to a role should
-- remain visible to every role member until each of them individually
-- dismisses. Recording the ack per user (card_id, user_id) lets the
-- stack query filter out cards the caller has already handled while
-- leaving them visible to teammates. Decisions still use the card-level
-- state column because they're first-wins by design.
CREATE TABLE app_card_user_acks (
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    card_id     UUID NOT NULL REFERENCES app_cards(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ack_kind    TEXT NOT NULL
                CHECK (ack_kind IN ('archived', 'dismissed', 'saved')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (card_id, user_id)
);

CREATE INDEX idx_app_card_user_acks_user ON app_card_user_acks(tenant_id, user_id);

-- +goose Down

DROP TABLE IF EXISTS app_card_user_acks;
