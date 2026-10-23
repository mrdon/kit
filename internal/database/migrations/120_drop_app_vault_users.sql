-- +goose Up

-- The follow-up 050 promised. app_vault_users held the per-user master
-- passwords from before the shared-password vault; it stayed only so
-- cmd/vault-migrate could re-wrap each tenant's key under the shared
-- password. Every tenant now has an app_vault_tenants row and nothing reads
-- this table, so its rows are stale key material left lying around.
DROP TABLE IF EXISTS app_vault_users;

-- +goose Down
-- Nothing to restore: the per-user vault model and its data are gone.
SELECT 1;
