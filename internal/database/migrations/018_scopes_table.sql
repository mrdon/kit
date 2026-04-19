-- +goose Up

-- Canonical scope rows shared across every scoped entity (skills, rules,
-- memories, tasks, todos, cards, channels, calendars). Exactly one row per
-- (tenant, role) or (tenant, user) or (tenant, tenant-wide), enforced by the
-- partial unique indexes below. getOrCreateScope() in models/scope.go relies
-- on those indexes for idempotent inserts.
CREATE TABLE scopes (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role_id   UUID REFERENCES roles(id) ON DELETE CASCADE,
    user_id   UUID REFERENCES users(id) ON DELETE CASCADE,
    CHECK ((role_id IS NOT NULL)::int + (user_id IS NOT NULL)::int <= 1)
);

CREATE UNIQUE INDEX scopes_tenant_role_idx
    ON scopes(tenant_id, role_id) WHERE role_id IS NOT NULL;
CREATE UNIQUE INDEX scopes_tenant_user_idx
    ON scopes(tenant_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX scopes_tenant_wide_idx
    ON scopes(tenant_id) WHERE role_id IS NULL AND user_id IS NULL;

-- +goose Down
DROP TABLE IF EXISTS scopes;
