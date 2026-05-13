-- +goose Up

-- Per-tenant tokens used to authenticate a website chat widget embed
-- (e.g. a Wix custom-HTML snippet). The plaintext token is shown to
-- the admin once at creation time; only the SHA-256 hash is stored.
-- The widget HTTP surface looks up the row by hash, derives tenant_id
-- from it, and enforces the allowed_origins list as a hard 403 gate
-- before rate-limiting.
CREATE TABLE widget_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    token_hash      BYTEA NOT NULL UNIQUE,
                                       -- SHA-256 of the plaintext token
    allowed_origins TEXT[] NOT NULL DEFAULT '{}',
                                       -- exact-match Origin header allowlist
    created_by      UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);

CREATE INDEX idx_widget_tokens_active
    ON widget_tokens (tenant_id)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS widget_tokens;
