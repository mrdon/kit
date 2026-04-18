-- +goose Up
-- Add tenant_id to card child tables so every query can include
-- `WHERE tenant_id = ?` literally, matching the pattern used by every other
-- child/scope table in the schema. Defense in depth against cross-tenant
-- leaks on the cards subtree.

ALTER TABLE app_card_decisions
    ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE;
UPDATE app_card_decisions d
    SET tenant_id = c.tenant_id
    FROM app_cards c
    WHERE c.id = d.card_id;
ALTER TABLE app_card_decisions
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE app_card_briefings
    ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE;
UPDATE app_card_briefings b
    SET tenant_id = c.tenant_id
    FROM app_cards c
    WHERE c.id = b.card_id;
ALTER TABLE app_card_briefings
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE app_card_decision_options
    ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE;
UPDATE app_card_decision_options o
    SET tenant_id = c.tenant_id
    FROM app_cards c
    WHERE c.id = o.card_id;
ALTER TABLE app_card_decision_options
    ALTER COLUMN tenant_id SET NOT NULL;

CREATE INDEX idx_app_card_decisions_tenant        ON app_card_decisions(tenant_id);
CREATE INDEX idx_app_card_briefings_tenant        ON app_card_briefings(tenant_id);
CREATE INDEX idx_app_card_decision_options_tenant ON app_card_decision_options(tenant_id);

-- +goose Down
DROP INDEX IF EXISTS idx_app_card_decision_options_tenant;
DROP INDEX IF EXISTS idx_app_card_briefings_tenant;
DROP INDEX IF EXISTS idx_app_card_decisions_tenant;

ALTER TABLE app_card_decision_options DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE app_card_briefings DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE app_card_decisions DROP COLUMN IF EXISTS tenant_id;
