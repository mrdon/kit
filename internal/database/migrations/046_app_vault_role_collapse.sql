-- +goose Up

-- Collapse the vault's authz model from {role,user,tenant}-scope to a
-- single role_id per entry (NULL = visible to everyone in the tenant).
-- Multi-role and per-user scoping are gone; users who need fanout pick
-- "everyone" or create an umbrella role.
--
-- Rationale: 1Password/Bitwarden-style multi-principal scoping was
-- over-engineered for a small-business team tool. The grant ceremony
-- (per-user wrapping of the tenant vault_key) is unchanged — joining
-- a role still requires a one-time grant before decryption works.
-- This migration only touches the authz layer; the crypto layer is
-- intentionally untouched. See plan §"Locked design choices".
--
-- Safe to ALTER + DROP because v1 vault shipped earlier today and prod
-- has zero entries. If a tenant somehow already has rows, this would
-- need a backfill — keep that in mind if this ever runs on a populated
-- DB (the current expectation is empty).

ALTER TABLE app_vault_entries
    ADD COLUMN role_id UUID REFERENCES roles(id) ON DELETE SET NULL;

CREATE INDEX idx_app_vault_entries_role
    ON app_vault_entries (tenant_id, role_id)
    WHERE role_id IS NOT NULL;

DROP TABLE IF EXISTS app_vault_entry_scopes;

-- +goose Down

CREATE TABLE app_vault_entry_scopes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    entry_id    UUID NOT NULL REFERENCES app_vault_entries(id) ON DELETE CASCADE,
    scope_kind  TEXT NOT NULL CHECK (scope_kind IN ('user','role','tenant')),
    scope_id    UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT app_vault_entry_scopes_kind_check
        CHECK ((scope_kind = 'tenant' AND scope_id IS NULL)
            OR (scope_kind IN ('user','role') AND scope_id IS NOT NULL))
);

CREATE INDEX idx_app_vault_entry_scopes_entry
    ON app_vault_entry_scopes (tenant_id, entry_id);
CREATE INDEX idx_app_vault_entry_scopes_principal
    ON app_vault_entry_scopes (tenant_id, scope_kind, scope_id);

DROP INDEX IF EXISTS idx_app_vault_entries_role;
ALTER TABLE app_vault_entries DROP COLUMN IF EXISTS role_id;
