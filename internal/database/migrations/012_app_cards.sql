-- +goose Up

-- Parent table: anything shared between decisions and briefings.
CREATE TABLE app_cards (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL
                    CHECK (kind IN ('decision', 'briefing')),
    title           TEXT NOT NULL,
    body            TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'resolved', 'archived', 'dismissed', 'saved', 'cancelled')),
    terminal_at     TIMESTAMPTZ,
    terminal_by     UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_cards_tenant_state_created ON app_cards(tenant_id, state, created_at);
CREATE INDEX idx_app_cards_tenant_kind         ON app_cards(tenant_id, kind);

-- Decision-specific columns (1:1 with app_cards when kind='decision').
CREATE TABLE app_card_decisions (
    card_id                UUID PRIMARY KEY REFERENCES app_cards(id) ON DELETE CASCADE,
    priority               TEXT NOT NULL DEFAULT 'medium'
                           CHECK (priority IN ('low', 'medium', 'high')),
    recommended_option_id  TEXT,
    resolved_option_id     TEXT,
    resolved_task_id       UUID REFERENCES tasks(id) ON DELETE SET NULL
);

-- Decision options (1:N). option_id is a stable string identifier supplied
-- by the creator; sort_order controls display order. prompt is the markdown
-- handed to the agent when this option is chosen; NULL means noop.
CREATE TABLE app_card_decision_options (
    card_id     UUID NOT NULL REFERENCES app_cards(id) ON DELETE CASCADE,
    option_id   TEXT NOT NULL,
    sort_order  INT  NOT NULL,
    label       TEXT NOT NULL,
    prompt      TEXT,
    PRIMARY KEY (card_id, option_id)
);

CREATE INDEX idx_app_card_decision_options_sort ON app_card_decision_options(card_id, sort_order);

-- Briefing-specific columns (1:1 with app_cards when kind='briefing').
CREATE TABLE app_card_briefings (
    card_id   UUID PRIMARY KEY REFERENCES app_cards(id) ON DELETE CASCADE,
    severity  TEXT NOT NULL DEFAULT 'info'
              CHECK (severity IN ('info', 'notable', 'important'))
);

-- Role/user/tenant scope rows — same pattern as task_scopes, rule_scopes.
CREATE TABLE app_card_scopes (
    tenant_id    UUID NOT NULL,
    card_id      UUID NOT NULL REFERENCES app_cards(id) ON DELETE CASCADE,
    scope_type   TEXT NOT NULL
                 CHECK (scope_type IN ('tenant', 'role', 'user')),
    scope_value  TEXT NOT NULL,
    PRIMARY KEY (card_id, scope_type, scope_value)
);

CREATE INDEX idx_app_card_scopes_lookup ON app_card_scopes(tenant_id, scope_type, scope_value);

-- +goose Down

DROP TABLE IF EXISTS app_card_scopes;
DROP TABLE IF EXISTS app_card_briefings;
DROP TABLE IF EXISTS app_card_decision_options;
DROP TABLE IF EXISTS app_card_decisions;
DROP TABLE IF EXISTS app_cards;
