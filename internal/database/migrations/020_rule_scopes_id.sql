-- +goose Up
-- +goose StatementBegin

INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT
    rs.tenant_id,
    CASE WHEN rs.scope_type = 'role' THEN r.id END AS role_id,
    CASE WHEN rs.scope_type = 'user' THEN u.id END AS user_id
FROM rule_scopes rs
LEFT JOIN roles r
    ON rs.scope_type = 'role'
    AND r.tenant_id = rs.tenant_id
    AND r.name = rs.scope_value
LEFT JOIN users u
    ON rs.scope_type = 'user'
    AND u.tenant_id = rs.tenant_id
    AND u.slack_user_id = rs.scope_value
WHERE rs.scope_type = 'tenant'
   OR (rs.scope_type = 'role' AND r.id IS NOT NULL)
   OR (rs.scope_type = 'user' AND u.id IS NOT NULL)
ON CONFLICT DO NOTHING;

ALTER TABLE rule_scopes ADD COLUMN scope_id UUID REFERENCES scopes(id) ON DELETE CASCADE;

UPDATE rule_scopes rs
SET scope_id = s.id
FROM scopes s
WHERE s.tenant_id = rs.tenant_id
  AND (
    (rs.scope_type = 'tenant' AND s.role_id IS NULL AND s.user_id IS NULL)
    OR (rs.scope_type = 'role'
        AND s.role_id = (SELECT id FROM roles WHERE tenant_id = rs.tenant_id AND name = rs.scope_value))
    OR (rs.scope_type = 'user'
        AND s.user_id = (SELECT id FROM users WHERE tenant_id = rs.tenant_id AND slack_user_id = rs.scope_value))
  );

DO $$
DECLARE
    null_count INT;
BEGIN
    SELECT COUNT(*) INTO null_count FROM rule_scopes WHERE scope_id IS NULL;
    IF null_count > 0 THEN
        RAISE EXCEPTION 'Migration failed: % rule_scopes rows have NULL scope_id (orphan role/user references)', null_count;
    END IF;
END $$;

ALTER TABLE rule_scopes ALTER COLUMN scope_id SET NOT NULL;

ALTER TABLE rule_scopes DROP CONSTRAINT rule_scopes_pkey;
ALTER TABLE rule_scopes ADD PRIMARY KEY (tenant_id, rule_id, scope_id);
ALTER TABLE rule_scopes DROP COLUMN scope_type;
ALTER TABLE rule_scopes DROP COLUMN scope_value;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE rule_scopes ADD COLUMN scope_type TEXT;
ALTER TABLE rule_scopes ADD COLUMN scope_value TEXT;

UPDATE rule_scopes rs
SET scope_type = CASE
        WHEN s.role_id IS NULL AND s.user_id IS NULL THEN 'tenant'
        WHEN s.role_id IS NOT NULL THEN 'role'
        ELSE 'user'
    END,
    scope_value = COALESCE(r.name, u.slack_user_id, '*')
FROM scopes s
LEFT JOIN roles r ON r.id = s.role_id
LEFT JOIN users u ON u.id = s.user_id
WHERE s.id = rs.scope_id;

ALTER TABLE rule_scopes ALTER COLUMN scope_type SET NOT NULL;
ALTER TABLE rule_scopes ALTER COLUMN scope_value SET NOT NULL;
ALTER TABLE rule_scopes DROP CONSTRAINT rule_scopes_pkey;
ALTER TABLE rule_scopes ADD PRIMARY KEY (tenant_id, rule_id, scope_type, scope_value);
ALTER TABLE rule_scopes DROP COLUMN scope_id;
-- +goose StatementEnd
