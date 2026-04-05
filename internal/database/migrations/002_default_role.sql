-- +goose Up
ALTER TABLE tenants ADD COLUMN default_role_id UUID REFERENCES roles(id) ON DELETE SET NULL;

-- +goose StatementBegin
DO $$
DECLARE
    t RECORD;
    role_id UUID;
BEGIN
    FOR t IN SELECT id FROM tenants LOOP
        role_id := gen_random_uuid();
        INSERT INTO roles (id, tenant_id, name, description)
        VALUES (role_id, t.id, 'member', 'Default role for all team members');
        UPDATE tenants SET default_role_id = role_id WHERE id = t.id;
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE tenants DROP COLUMN IF EXISTS default_role_id;
