-- +goose Up

-- Per-user crypto state. One row per user who has set up the vault in their
-- tenant. The tenant's vault is "initialized" iff any row exists for that
-- tenant with wrapped_vault_key NOT NULL. The first such row is the
-- bootstrapping admin; subsequent users register with wrapped_vault_key NULL
-- and wait for an existing member to grant them access.
--
-- Crypto model (Bitwarden-Org / 1Password-Shared-Vault style):
--   master_key = Argon2id(password, kdf_params.salt)               -- in browser only
--   enc_key    = HKDF-SHA256(master_key, salt, "kit-vault-v1-enc") -- in browser
--   auth_hash  = HKDF-SHA256(master_key, salt, "kit-vault-v1-auth")-- sent to server
--   user_private_key_ciphertext = AES-GCM(rsa_priv_pkcs8, enc_key) -- in browser
--   wrapped_vault_key = RSA-OAEP-SHA256(vault_key, user_public_key)
-- The server never sees master_key, enc_key, or vault_key.
CREATE TABLE app_vault_users (
    tenant_id                    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id                      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kdf_params                   JSONB NOT NULL,
                                  -- {algo:"argon2id", v:19, m:65536, t:3, p:1, salt:"<base64-16>"}
    auth_hash                    BYTEA NOT NULL,
                                  -- HKDF(master_key, info="kit-vault-v1-auth"); 32 bytes
    user_public_key              BYTEA NOT NULL,
                                  -- 2048-bit RSA SPKI (DER). validated server-side at register/grant.
    user_private_key_ciphertext  BYTEA NOT NULL,
                                  -- AES-GCM(rsa_priv_pkcs8, enc_key)
    user_private_key_nonce       BYTEA NOT NULL,
                                  -- 12 random bytes
    wrapped_vault_key            BYTEA,
                                  -- RSA-OAEP(vault_key, user_public_key); NULL until granted
    granted_by_user_id           UUID REFERENCES users(id) ON DELETE SET NULL,
    granted_at                   TIMESTAMPTZ,
    failed_unlocks               INT NOT NULL DEFAULT 0,
    locked_until                 TIMESTAMPTZ,
    reset_pending_until          TIMESTAMPTZ,
                                  -- non-null during 24h master-password reset cooldown
    pending                      BOOLEAN NOT NULL DEFAULT TRUE,
                                  -- flipped to FALSE only after browser passes self-unlock test
    created_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id)
);

-- Bootstrap guard: at most one "tenant initializer" (a user whose access was
-- self-issued, not granted by anyone else) per tenant. Eliminates the race
-- where two concurrent registrations both try to seed the tenant vault.
CREATE UNIQUE INDEX idx_app_vault_first_user
    ON app_vault_users (tenant_id)
    WHERE wrapped_vault_key IS NOT NULL AND granted_by_user_id IS NULL;

-- One vault entry per stored secret. Metadata is plaintext for search
-- (the agent's `find_secret` and `list_secrets` ranking happens here);
-- the value field (password + notes JSON) is AES-GCM encrypted in the
-- browser with the tenant's vault_key. The server only ever sees ciphertext.
CREATE TABLE app_vault_entries (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    owner_user_id     UUID NOT NULL REFERENCES users(id),
                       -- authorization: owner can always see; scope rows extend visibility
    title             TEXT NOT NULL,
    username          TEXT,
    url               TEXT,
    tags              TEXT[] NOT NULL DEFAULT '{}'::text[],
    value_ciphertext  BYTEA NOT NULL,        -- AES-GCM(JSON{password,notes}, vault_key)
    value_nonce       BYTEA NOT NULL,        -- 12 random bytes; fresh per encryption
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_viewed_at    TIMESTAMPTZ
);

CREATE INDEX idx_app_vault_entries_owner
    ON app_vault_entries (tenant_id, owner_user_id);
CREATE INDEX idx_app_vault_entries_recent
    ON app_vault_entries (tenant_id, last_viewed_at DESC NULLS LAST);
CREATE INDEX idx_app_vault_entries_title_fts
    ON app_vault_entries USING GIN (to_tsvector('english', coalesce(title, '') || ' ' || coalesce(url, '') || ' ' || coalesce(username, '')));

-- Authorization layer (separate from crypto): mirrors skill_scopes /
-- rule_scopes / memory_scopes. Default-deny — with no scope rows, an entry
-- is visible only to its owner. Scope rows extend visibility to named
-- users / role members / the whole tenant.
CREATE TABLE app_vault_entry_scopes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    entry_id    UUID NOT NULL REFERENCES app_vault_entries(id) ON DELETE CASCADE,
    scope_kind  TEXT NOT NULL CHECK (scope_kind IN ('user','role','tenant')),
    scope_id    UUID,                        -- user_id or role_id; NULL for kind='tenant'
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT app_vault_entry_scopes_kind_check
        CHECK ((scope_kind = 'tenant' AND scope_id IS NULL)
            OR (scope_kind IN ('user','role') AND scope_id IS NOT NULL))
);

CREATE INDEX idx_app_vault_entry_scopes_entry
    ON app_vault_entry_scopes (tenant_id, entry_id);
CREATE INDEX idx_app_vault_entry_scopes_principal
    ON app_vault_entry_scopes (tenant_id, scope_kind, scope_id);

-- +goose Down
DROP TABLE IF EXISTS app_vault_entry_scopes;
DROP TABLE IF EXISTS app_vault_entries;
DROP INDEX IF EXISTS idx_app_vault_first_user;
DROP TABLE IF EXISTS app_vault_users;
