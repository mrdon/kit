-- +goose Up
-- +goose StatementBegin

-- Pre-create canonical scope rows for every (tenant, role|user|nil) triple
-- referenced by skill_scopes. The partial unique indexes on `scopes` dedup
-- across this and every other entity migration that follows.
INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT
    ss.tenant_id,
    CASE WHEN ss.scope_type = 'role' THEN r.id END AS role_id,
    CASE WHEN ss.scope_type = 'user' THEN u.id END AS user_id
FROM skill_scopes ss
LEFT JOIN roles r
    ON ss.scope_type = 'role'
    AND r.tenant_id = ss.tenant_id
    AND r.name = ss.scope_value
LEFT JOIN users u
    ON ss.scope_type = 'user'
    AND u.tenant_id = ss.tenant_id
    AND u.slack_user_id = ss.scope_value
WHERE ss.scope_type = 'tenant'
   OR (ss.scope_type = 'role' AND r.id IS NOT NULL)
   OR (ss.scope_type = 'user' AND u.id IS NOT NULL)
ON CONFLICT DO NOTHING;

ALTER TABLE skill_scopes ADD COLUMN scope_id UUID REFERENCES scopes(id) ON DELETE CASCADE;

UPDATE skill_scopes ss
SET scope_id = s.id
FROM scopes s
WHERE s.tenant_id = ss.tenant_id
  AND (
    (ss.scope_type = 'tenant' AND s.role_id IS NULL AND s.user_id IS NULL)
    OR (ss.scope_type = 'role'
        AND s.role_id = (SELECT id FROM roles WHERE tenant_id = ss.tenant_id AND name = ss.scope_value))
    OR (ss.scope_type = 'user'
        AND s.user_id = (SELECT id FROM users WHERE tenant_id = ss.tenant_id AND slack_user_id = ss.scope_value))
  );

DO $$
DECLARE
    null_count INT;
BEGIN
    SELECT COUNT(*) INTO null_count FROM skill_scopes WHERE scope_id IS NULL;
    IF null_count > 0 THEN
        RAISE EXCEPTION 'Migration failed: % skill_scopes rows have NULL scope_id (orphan role/user references)', null_count;
    END IF;
END $$;

ALTER TABLE skill_scopes ALTER COLUMN scope_id SET NOT NULL;

ALTER TABLE skill_scopes DROP CONSTRAINT skill_scopes_pkey;
ALTER TABLE skill_scopes ADD PRIMARY KEY (tenant_id, skill_id, scope_id);
ALTER TABLE skill_scopes DROP COLUMN scope_type;
ALTER TABLE skill_scopes DROP COLUMN scope_value;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE skill_scopes ADD COLUMN scope_type TEXT;
ALTER TABLE skill_scopes ADD COLUMN scope_value TEXT;

UPDATE skill_scopes ss
SET scope_type = CASE
        WHEN s.role_id IS NULL AND s.user_id IS NULL THEN 'tenant'
        WHEN s.role_id IS NOT NULL THEN 'role'
        ELSE 'user'
    END,
    scope_value = COALESCE(r.name, u.slack_user_id, '*')
FROM scopes s
LEFT JOIN roles r ON r.id = s.role_id
LEFT JOIN users u ON u.id = s.user_id
WHERE s.id = ss.scope_id;

ALTER TABLE skill_scopes ALTER COLUMN scope_type SET NOT NULL;
ALTER TABLE skill_scopes ALTER COLUMN scope_value SET NOT NULL;
ALTER TABLE skill_scopes DROP CONSTRAINT skill_scopes_pkey;
ALTER TABLE skill_scopes ADD PRIMARY KEY (tenant_id, skill_id, scope_type, scope_value);
ALTER TABLE skill_scopes DROP COLUMN scope_id;
-- +goose StatementEnd
