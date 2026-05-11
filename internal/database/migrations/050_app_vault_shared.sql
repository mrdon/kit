-- +goose Up

-- Shared-password vault model. Replaces per-user master passwords with
-- one shared master password per tenant (shared out-of-band among the
-- team, similar to how small orgs share a website members-area password).
--
-- Crypto model:
--   shared_password   [out-of-band, typed by every user]
--   master_key       = PBKDF2-SHA256(shared_password, salt, 600k)   -- browser
--   enc_key          = HKDF(master_key, info="kit-vault-v1-enc")   -- browser
--   auth_hash        = HKDF(master_key, info="kit-vault-v1-auth")  -- sent to server
--   wrapped_vault_key = AES-GCM(vault_key, enc_key, nonce, aad=tenant_id)
-- The server never sees master_key, enc_key, or vault_key.
--
-- This migration only creates the new table. The old app_vault_users
-- table stays in place so the data-migration tool (cmd/vault-migrate)
-- can read from it. A follow-up migration drops it once every tenant
-- has been migrated.

CREATE TABLE app_vault_tenants (
    tenant_id                UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    kdf_params               JSONB NOT NULL,
                              -- {algo:"pbkdf2-sha256", iterations:600000, salt:"<base64-16>"}
    auth_hash                BYTEA NOT NULL,
                              -- HKDF(master_key, info="kit-vault-v1-auth"); 32 bytes
    wrapped_vault_key        BYTEA NOT NULL,
                              -- AES-GCM(vault_key, enc_key, nonce, aad=tenant_id bytes)
    wrapped_vault_key_nonce  BYTEA NOT NULL,
                              -- 12 random bytes
    vault_generation         INT NOT NULL DEFAULT 1,
                              -- bumped on rotate; SharedWorker discards
                              -- its cached vault_key when it sees a
                              -- higher generation in any API response.
    locked_until             TIMESTAMPTZ,
                              -- soft lockout; driven by per-IP token bucket
                              -- in ratelimit.go (no per-tenant counter — it
                              -- would create a self-DoS griefing vector).
    last_rotated_by_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS app_vault_tenants;
