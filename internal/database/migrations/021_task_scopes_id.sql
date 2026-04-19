-- +goose Up
-- +goose StatementBegin

INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT
    ts.tenant_id,
    CASE WHEN ts.scope_type = 'role' THEN r.id END AS role_id,
    CASE WHEN ts.scope_type = 'user' THEN u.id END AS user_id
FROM task_scopes ts
LEFT JOIN roles r
    ON ts.scope_type = 'role'
    AND r.tenant_id = ts.tenant_id
    AND r.name = ts.scope_value
LEFT JOIN users u
    ON ts.scope_type = 'user'
    AND u.tenant_id = ts.tenant_id
    AND u.slack_user_id = ts.scope_value
WHERE ts.scope_type = 'tenant'
   OR (ts.scope_type = 'role' AND r.id IS NOT NULL)
   OR (ts.scope_type = 'user' AND u.id IS NOT NULL)
ON CONFLICT DO NOTHING;

ALTER TABLE task_scopes ADD COLUMN scope_id UUID REFERENCES scopes(id) ON DELETE CASCADE;

UPDATE task_scopes ts
SET scope_id = s.id
FROM scopes s
WHERE s.tenant_id = ts.tenant_id
  AND (
    (ts.scope_type = 'tenant' AND s.role_id IS NULL AND s.user_id IS NULL)
    OR (ts.scope_type = 'role'
        AND s.role_id = (SELECT id FROM roles WHERE tenant_id = ts.tenant_id AND name = ts.scope_value))
    OR (ts.scope_type = 'user'
        AND s.user_id = (SELECT id FROM users WHERE tenant_id = ts.tenant_id AND slack_user_id = ts.scope_value))
  );

DO $$
DECLARE
    null_count INT;
BEGIN
    SELECT COUNT(*) INTO null_count FROM task_scopes WHERE scope_id IS NULL;
    IF null_count > 0 THEN
        RAISE EXCEPTION 'Migration failed: % task_scopes rows have NULL scope_id (orphan role/user references)', null_count;
    END IF;
END $$;

ALTER TABLE task_scopes ALTER COLUMN scope_id SET NOT NULL;

ALTER TABLE task_scopes DROP CONSTRAINT task_scopes_pkey;
ALTER TABLE task_scopes ADD PRIMARY KEY (tenant_id, task_id, scope_id);
ALTER TABLE task_scopes DROP COLUMN scope_type;
ALTER TABLE task_scopes DROP COLUMN scope_value;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE task_scopes ADD COLUMN scope_type TEXT;
ALTER TABLE task_scopes ADD COLUMN scope_value TEXT;

UPDATE task_scopes ts
SET scope_type = CASE
        WHEN s.role_id IS NULL AND s.user_id IS NULL THEN 'tenant'
        WHEN s.role_id IS NOT NULL THEN 'role'
        ELSE 'user'
    END,
    scope_value = COALESCE(r.name, u.slack_user_id, '*')
FROM scopes s
LEFT JOIN roles r ON r.id = s.role_id
LEFT JOIN users u ON u.id = s.user_id
WHERE s.id = ts.scope_id;

ALTER TABLE task_scopes ALTER COLUMN scope_type SET NOT NULL;
ALTER TABLE task_scopes ALTER COLUMN scope_value SET NOT NULL;
ALTER TABLE task_scopes DROP CONSTRAINT task_scopes_pkey;
ALTER TABLE task_scopes ADD PRIMARY KEY (tenant_id, task_id, scope_type, scope_value);
ALTER TABLE task_scopes DROP COLUMN scope_id;
-- +goose StatementEnd
