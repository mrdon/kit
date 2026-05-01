-- +goose Up

-- Make role_id required on app_vault_entries: every entry now belongs to
-- exactly one role. "Tenant-wide visibility" is expressed by scoping to
-- the tenant's default_role_id (a 'member' role auto-created per tenant
-- in 002_default_role.sql; every user is implicitly assigned it via
-- GetUserRoleIDs(..., tenant.DefaultRoleID)).
--
-- Backfill any rows still on NULL with the tenant's default_role_id.
-- This is safe: those rows were "everyone in tenant" semantically, and
-- the member role IS everyone in tenant. If somehow a tenant has no
-- default_role_id (theoretically possible but every existing tenant
-- has one per the 002 migration), the row keeps NULL and the ALTER
-- will fail loudly — better to surface than to drop data silently.

UPDATE app_vault_entries e
   SET role_id = t.default_role_id
  FROM tenants t
 WHERE e.tenant_id = t.id
   AND e.role_id IS NULL
   AND t.default_role_id IS NOT NULL;

ALTER TABLE app_vault_entries
    ALTER COLUMN role_id SET NOT NULL;

-- +goose Down
ALTER TABLE app_vault_entries
    ALTER COLUMN role_id DROP NOT NULL;
