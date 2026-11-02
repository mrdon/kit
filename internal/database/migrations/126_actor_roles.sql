-- +goose Up

-- Roles a paired device holds, the device counterpart of user_roles. A device
-- still authorises its console pages by capability; roles decide what Kit
-- knowledge (skills, memories, vault entries) it can see. Unlike people,
-- devices get no implicit `member` role: they hold exactly the rows here.
-- `admin` is refused in the service and filtered on read, never stored.
CREATE TABLE actor_roles (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_id  UUID NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    role_id   UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, actor_id, role_id)
);

CREATE INDEX idx_actor_roles_role ON actor_roles(tenant_id, role_id);

-- +goose Down
DROP TABLE IF EXISTS actor_roles;
