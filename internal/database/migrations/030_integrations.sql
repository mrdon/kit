-- +goose Up

-- Integrations are configurable connections to external services (email,
-- calendar, payments, etc.). Secret values (API keys, OAuth tokens) are
-- encrypted at rest and live in dedicated token columns — never in the
-- non-secret `config` JSONB — so the LLM-facing read path can't leak them
-- even by accident.
--
-- Identity is (provider, auth_type): github-with-a-PAT and github-via-OAuth
-- can coexist as different rows for the same user.
--
-- Scope: user_id = NULL for tenant-scoped integrations (one row per
-- workspace, admin-only), set for user-scoped (one row per user, per type).

-- pending_integrations: in-flight configs awaiting user secret entry via a
-- short-lived signed URL. Status flips pending -> consumed on successful
-- submit. Lazy expiry: rows with expires_at < now() are invalid.
CREATE TABLE pending_integrations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    created_by        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider          TEXT NOT NULL,
    auth_type         TEXT NOT NULL,
    -- NULL for tenant-scoped types; set for user-scoped types. Matches the
    -- target row's user_id on completion.
    target_user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
    status            TEXT NOT NULL DEFAULT 'pending',
    username          TEXT,
    primary_token     TEXT,
    secondary_token   TEXT,
    config            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL,
    completed_at      TIMESTAMPTZ
);

CREATE INDEX pending_integrations_tenant_status_idx
    ON pending_integrations (tenant_id, status);
CREATE INDEX pending_integrations_expires_idx
    ON pending_integrations (expires_at) WHERE status = 'pending';

-- integrations: live configured integrations. user_id = NULL for
-- tenant-scoped, set for user-scoped.
CREATE TABLE integrations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id           UUID REFERENCES users(id) ON DELETE CASCADE,
    provider          TEXT NOT NULL,
    auth_type         TEXT NOT NULL,
    username          TEXT,
    primary_token     TEXT,
    secondary_token   TEXT,
    config            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per (tenant, provider, auth_type) tenant-scoped; one per
-- (tenant, provider, auth_type, user) user-scoped. NULLS NOT DISTINCT
-- (Postgres 15+) so NULL user_id still collides on re-config.
CREATE UNIQUE INDEX integrations_unique_idx
    ON integrations (tenant_id, provider, auth_type, user_id) NULLS NOT DISTINCT;

CREATE INDEX integrations_tenant_provider_idx
    ON integrations (tenant_id, provider, auth_type);

-- +goose Down
DROP TABLE IF EXISTS integrations;
DROP TABLE IF EXISTS pending_integrations;
