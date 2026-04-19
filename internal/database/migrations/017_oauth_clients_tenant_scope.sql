-- +goose Up

-- oauth_clients become tenant-scoped: each Claude Code install ↔ workspace
-- pair gets its own client row. Existing rows are dropped; MCP clients
-- re-register transparently on next connect via /{slug}/oauth/register.
-- oauth_codes have no FK to oauth_clients; wipe them too so no code
-- references a dropped client_id during the re-register window (they're
-- 10-minute-lived anyway).
DELETE FROM oauth_codes;
DELETE FROM oauth_clients;

ALTER TABLE oauth_clients
    ADD COLUMN tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE oauth_clients DROP CONSTRAINT IF EXISTS oauth_clients_client_id_key;

CREATE UNIQUE INDEX oauth_clients_tenant_client_id_idx
    ON oauth_clients(tenant_id, client_id);
CREATE INDEX oauth_clients_tenant_id_idx ON oauth_clients(tenant_id);

-- +goose Down
DROP INDEX IF EXISTS oauth_clients_tenant_id_idx;
DROP INDEX IF EXISTS oauth_clients_tenant_client_id_idx;
ALTER TABLE oauth_clients DROP COLUMN tenant_id;
ALTER TABLE oauth_clients ADD CONSTRAINT oauth_clients_client_id_key UNIQUE (client_id);
