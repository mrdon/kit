-- +goose Up
-- +goose StatementBegin
INSERT INTO roles (id, tenant_id, name, description)
SELECT gen_random_uuid(), t.id, 'admin', 'Tenant administrators'
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM roles r WHERE r.tenant_id = t.id AND r.name = 'admin'
);

INSERT INTO user_roles (tenant_id, user_id, role_id)
SELECT u.tenant_id, u.id, r.id
FROM users u
JOIN roles r ON r.tenant_id = u.tenant_id AND r.name = 'admin'
WHERE u.is_admin = true
ON CONFLICT DO NOTHING;

ALTER TABLE users DROP COLUMN is_admin;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN is_admin BOOLEAN DEFAULT false;

UPDATE users u
SET is_admin = true
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = u.id AND ur.tenant_id = u.tenant_id AND r.name = 'admin';
-- +goose StatementEnd
