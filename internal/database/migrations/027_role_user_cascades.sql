-- +goose Up
-- +goose StatementBegin

-- Pre-existing FKs from 001 had no ON DELETE behavior, so role deletion
-- with assigned members fails (user_roles.role_id) and tenant deletion
-- with users fails (users.tenant_id). Both should cascade — deleting a
-- role should drop its bindings; deleting a tenant should drop everything
-- inside it. The scopes refactor (and the new force=true DeleteRole) both
-- depend on these cascades working.

ALTER TABLE user_roles DROP CONSTRAINT user_roles_role_id_fkey;
ALTER TABLE user_roles
    ADD CONSTRAINT user_roles_role_id_fkey
    FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE;

ALTER TABLE user_roles DROP CONSTRAINT user_roles_user_id_fkey;
ALTER TABLE user_roles
    ADD CONSTRAINT user_roles_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE user_roles DROP CONSTRAINT user_roles_tenant_id_fkey;
ALTER TABLE user_roles
    ADD CONSTRAINT user_roles_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE users DROP CONSTRAINT users_tenant_id_fkey;
ALTER TABLE users
    ADD CONSTRAINT users_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE roles DROP CONSTRAINT roles_tenant_id_fkey;
ALTER TABLE roles
    ADD CONSTRAINT roles_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE roles DROP CONSTRAINT roles_tenant_id_fkey;
ALTER TABLE roles ADD CONSTRAINT roles_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id);

ALTER TABLE users DROP CONSTRAINT users_tenant_id_fkey;
ALTER TABLE users ADD CONSTRAINT users_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id);

ALTER TABLE user_roles DROP CONSTRAINT user_roles_tenant_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id);

ALTER TABLE user_roles DROP CONSTRAINT user_roles_user_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id);

ALTER TABLE user_roles DROP CONSTRAINT user_roles_role_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_role_id_fkey
    FOREIGN KEY (role_id) REFERENCES roles(id);
-- +goose StatementEnd
