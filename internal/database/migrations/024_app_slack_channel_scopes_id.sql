-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE
    n INT;
BEGIN
    WITH deleted AS (
        DELETE FROM app_slack_channel_scopes cs
        WHERE (cs.scope_type = 'role'
               AND NOT EXISTS (SELECT 1 FROM roles r WHERE r.tenant_id = cs.tenant_id AND r.name = cs.scope_value))
           OR (cs.scope_type = 'user'
               AND NOT EXISTS (SELECT 1 FROM users u WHERE u.tenant_id = cs.tenant_id AND u.slack_user_id = cs.scope_value))
        RETURNING 1
    )
    SELECT COUNT(*) INTO n FROM deleted;
    IF n > 0 THEN
        RAISE NOTICE 'app_slack_channel_scopes: deleted % orphan scope rows', n;
    END IF;
END $$;

INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT
    cs.tenant_id,
    CASE WHEN cs.scope_type = 'role' THEN r.id END AS role_id,
    CASE WHEN cs.scope_type = 'user' THEN u.id END AS user_id
FROM app_slack_channel_scopes cs
LEFT JOIN roles r
    ON cs.scope_type = 'role'
    AND r.tenant_id = cs.tenant_id
    AND r.name = cs.scope_value
LEFT JOIN users u
    ON cs.scope_type = 'user'
    AND u.tenant_id = cs.tenant_id
    AND u.slack_user_id = cs.scope_value
WHERE cs.scope_type = 'tenant'
   OR (cs.scope_type = 'role' AND r.id IS NOT NULL)
   OR (cs.scope_type = 'user' AND u.id IS NOT NULL)
ON CONFLICT DO NOTHING;

ALTER TABLE app_slack_channel_scopes ADD COLUMN scope_id UUID REFERENCES scopes(id) ON DELETE CASCADE;

UPDATE app_slack_channel_scopes cs
SET scope_id = s.id
FROM scopes s
WHERE s.tenant_id = cs.tenant_id
  AND (
    (cs.scope_type = 'tenant' AND s.role_id IS NULL AND s.user_id IS NULL)
    OR (cs.scope_type = 'role'
        AND s.role_id = (SELECT id FROM roles WHERE tenant_id = cs.tenant_id AND name = cs.scope_value))
    OR (cs.scope_type = 'user'
        AND s.user_id = (SELECT id FROM users WHERE tenant_id = cs.tenant_id AND slack_user_id = cs.scope_value))
  );

DO $$
DECLARE
    null_count INT;
BEGIN
    SELECT COUNT(*) INTO null_count FROM app_slack_channel_scopes WHERE scope_id IS NULL;
    IF null_count > 0 THEN
        RAISE EXCEPTION 'Migration failed: % app_slack_channel_scopes rows have NULL scope_id', null_count;
    END IF;
END $$;

ALTER TABLE app_slack_channel_scopes ALTER COLUMN scope_id SET NOT NULL;

ALTER TABLE app_slack_channel_scopes DROP CONSTRAINT app_slack_channel_scopes_pkey;
ALTER TABLE app_slack_channel_scopes ADD PRIMARY KEY (tenant_id, channel_id, scope_id);
ALTER TABLE app_slack_channel_scopes DROP COLUMN scope_type;
ALTER TABLE app_slack_channel_scopes DROP COLUMN scope_value;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_slack_channel_scopes ADD COLUMN scope_type TEXT;
ALTER TABLE app_slack_channel_scopes ADD COLUMN scope_value TEXT;

UPDATE app_slack_channel_scopes cs
SET scope_type = CASE
        WHEN s.role_id IS NULL AND s.user_id IS NULL THEN 'tenant'
        WHEN s.role_id IS NOT NULL THEN 'role'
        ELSE 'user'
    END,
    scope_value = COALESCE(r.name, u.slack_user_id, '*')
FROM scopes s
LEFT JOIN roles r ON r.id = s.role_id
LEFT JOIN users u ON u.id = s.user_id
WHERE s.id = cs.scope_id;

ALTER TABLE app_slack_channel_scopes ALTER COLUMN scope_type SET NOT NULL;
ALTER TABLE app_slack_channel_scopes ALTER COLUMN scope_value SET NOT NULL;
ALTER TABLE app_slack_channel_scopes DROP CONSTRAINT app_slack_channel_scopes_pkey;
ALTER TABLE app_slack_channel_scopes ADD PRIMARY KEY (tenant_id, channel_id, scope_type, scope_value);
ALTER TABLE app_slack_channel_scopes DROP COLUMN scope_id;
-- +goose StatementEnd
