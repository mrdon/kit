-- +goose Up

-- Per-user card snooze. Like app_card_user_acks but non-terminal: hides a
-- card from this user's feed until snoozed_until passes, then it reappears.
-- Per-user (rather than per-card) because Kit's surfaces are personal —
-- Alice deferring a decision shouldn't hide it from Bob who shares the
-- role-scope. Same shape works for both decisions and briefings; the
-- scope is "any card", so this lives next to app_card_user_acks not on
-- the kind-specific child tables.
--
-- When Slack DMs / email digests ship later, those dispatchers consult
-- this table too — the action is "user has deferred this card", not "the
-- swipe stack should hide this card", and every notification surface
-- should respect it.
CREATE TABLE app_user_card_snoozes (
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    card_id       UUID NOT NULL REFERENCES app_cards(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    snoozed_until TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (card_id, user_id)
);

CREATE INDEX idx_app_user_card_snoozes_active
    ON app_user_card_snoozes(tenant_id, user_id, snoozed_until);

-- +goose Down

DROP TABLE IF EXISTS app_user_card_snoozes;
