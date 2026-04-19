-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE
    n INT;
BEGIN
    WITH deleted AS (
        DELETE FROM memories m
        WHERE (m.scope_type = 'role'
               AND NOT EXISTS (SELECT 1 FROM roles r WHERE r.tenant_id = m.tenant_id AND r.name = m.scope_value))
           OR (m.scope_type = 'user'
               AND NOT EXISTS (SELECT 1 FROM users u WHERE u.tenant_id = m.tenant_id AND u.slack_user_id = m.scope_value))
        RETURNING 1
    )
    SELECT COUNT(*) INTO n FROM deleted;
    IF n > 0 THEN
        RAISE NOTICE 'memories: deleted % rows scoped to non-existent role/user', n;
    END IF;
END $$;

INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT
    m.tenant_id,
    CASE WHEN m.scope_type = 'role' THEN r.id END AS role_id,
    CASE WHEN m.scope_type = 'user' THEN u.id END AS user_id
FROM memories m
LEFT JOIN roles r
    ON m.scope_type = 'role'
    AND r.tenant_id = m.tenant_id
    AND r.name = m.scope_value
LEFT JOIN users u
    ON m.scope_type = 'user'
    AND u.tenant_id = m.tenant_id
    AND u.slack_user_id = m.scope_value
WHERE m.scope_type = 'tenant'
   OR (m.scope_type = 'role' AND r.id IS NOT NULL)
   OR (m.scope_type = 'user' AND u.id IS NOT NULL)
ON CONFLICT DO NOTHING;

ALTER TABLE memories ADD COLUMN scope_id UUID REFERENCES scopes(id) ON DELETE CASCADE;

UPDATE memories m
SET scope_id = s.id
FROM scopes s
WHERE s.tenant_id = m.tenant_id
  AND (
    (m.scope_type = 'tenant' AND s.role_id IS NULL AND s.user_id IS NULL)
    OR (m.scope_type = 'role'
        AND s.role_id = (SELECT id FROM roles WHERE tenant_id = m.tenant_id AND name = m.scope_value))
    OR (m.scope_type = 'user'
        AND s.user_id = (SELECT id FROM users WHERE tenant_id = m.tenant_id AND slack_user_id = m.scope_value))
  );

DO $$
DECLARE
    null_count INT;
BEGIN
    SELECT COUNT(*) INTO null_count FROM memories WHERE scope_id IS NULL;
    IF null_count > 0 THEN
        RAISE EXCEPTION 'Migration failed: % memories rows have NULL scope_id', null_count;
    END IF;
END $$;

ALTER TABLE memories ALTER COLUMN scope_id SET NOT NULL;

ALTER TABLE memories DROP COLUMN scope_type;
ALTER TABLE memories DROP COLUMN scope_value;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE memories ADD COLUMN scope_type TEXT;
ALTER TABLE memories ADD COLUMN scope_value TEXT;

UPDATE memories m
SET scope_type = CASE
        WHEN s.role_id IS NULL AND s.user_id IS NULL THEN 'tenant'
        WHEN s.role_id IS NOT NULL THEN 'role'
        ELSE 'user'
    END,
    scope_value = COALESCE(r.name, u.slack_user_id, '*')
FROM scopes s
LEFT JOIN roles r ON r.id = s.role_id
LEFT JOIN users u ON u.id = s.user_id
WHERE s.id = m.scope_id;

ALTER TABLE memories ALTER COLUMN scope_type SET NOT NULL;
ALTER TABLE memories ALTER COLUMN scope_value SET NOT NULL;
ALTER TABLE memories DROP COLUMN scope_id;
-- +goose StatementEnd
