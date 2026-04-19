-- +goose Up
-- +goose StatementBegin

-- Pre-create canonical scope rows for every (tenant, role|user|nil)
-- referenced by app_todos. The mapping is:
--   private=false (any assignee/role) -> scope chosen below; visibility='public'
--   private=true,  assigned_to set    -> user-scope of assignee
--   private=true,  role_scope set     -> role-scope
--   private=true,  no assignee/role   -> user-scope of created_by
INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT t.tenant_id, NULL::uuid, t.assigned_to
FROM app_todos t
WHERE t.assigned_to IS NOT NULL
ON CONFLICT DO NOTHING;

INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT t.tenant_id, r.id, NULL::uuid
FROM app_todos t
JOIN roles r ON r.tenant_id = t.tenant_id AND r.name = t.role_scope
WHERE t.role_scope IS NOT NULL
ON CONFLICT DO NOTHING;

-- Tenant-wide row (used as fallback for any non-private no-owner todo)
INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT t.tenant_id, NULL::uuid, NULL::uuid
FROM app_todos t
WHERE NOT t.private
  AND t.assigned_to IS NULL
  AND t.role_scope IS NULL
ON CONFLICT DO NOTHING;

-- Cover the (rare) private-with-no-assignee case via creator's user scope.
INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT t.tenant_id, NULL::uuid, t.created_by
FROM app_todos t
WHERE t.private
  AND t.assigned_to IS NULL
  AND t.role_scope IS NULL
ON CONFLICT DO NOTHING;

ALTER TABLE app_todos
    ADD COLUMN scope_id UUID REFERENCES scopes(id) ON DELETE CASCADE,
    ADD COLUMN visibility TEXT NOT NULL DEFAULT 'scoped'
        CHECK (visibility IN ('scoped','public'));

-- Backfill scope_id + visibility per the mapping.
UPDATE app_todos t
SET scope_id = s.id,
    visibility = 'public'
FROM scopes s
WHERE NOT t.private
  AND s.tenant_id = t.tenant_id
  AND (
    -- prefer assignee scope if there's one
    (t.assigned_to IS NOT NULL AND s.user_id = t.assigned_to AND s.role_id IS NULL)
    OR (t.assigned_to IS NULL AND t.role_scope IS NOT NULL
        AND s.role_id = (SELECT id FROM roles WHERE tenant_id = t.tenant_id AND name = t.role_scope))
    OR (t.assigned_to IS NULL AND t.role_scope IS NULL
        AND s.role_id IS NULL AND s.user_id IS NULL)
  );

UPDATE app_todos t
SET scope_id = s.id,
    visibility = 'scoped'
FROM scopes s
WHERE t.private
  AND t.assigned_to IS NOT NULL
  AND s.tenant_id = t.tenant_id
  AND s.user_id = t.assigned_to AND s.role_id IS NULL;

UPDATE app_todos t
SET scope_id = s.id,
    visibility = 'scoped'
FROM scopes s
WHERE t.private
  AND t.assigned_to IS NULL
  AND t.role_scope IS NOT NULL
  AND s.tenant_id = t.tenant_id
  AND s.role_id = (SELECT id FROM roles WHERE tenant_id = t.tenant_id AND name = t.role_scope);

UPDATE app_todos t
SET scope_id = s.id,
    visibility = 'scoped'
FROM scopes s
WHERE t.private
  AND t.assigned_to IS NULL
  AND t.role_scope IS NULL
  AND s.tenant_id = t.tenant_id
  AND s.user_id = t.created_by AND s.role_id IS NULL;

DO $$
DECLARE
    null_count INT;
BEGIN
    SELECT COUNT(*) INTO null_count FROM app_todos WHERE scope_id IS NULL;
    IF null_count > 0 THEN
        RAISE EXCEPTION 'Migration failed: % app_todos rows have NULL scope_id', null_count;
    END IF;
END $$;

ALTER TABLE app_todos ALTER COLUMN scope_id SET NOT NULL;

DROP INDEX IF EXISTS idx_app_todos_assigned;
DROP INDEX IF EXISTS idx_app_todos_role;

ALTER TABLE app_todos
    DROP COLUMN private,
    DROP COLUMN created_by,
    DROP COLUMN assigned_to,
    DROP COLUMN role_scope;

CREATE INDEX idx_app_todos_scope ON app_todos(tenant_id, scope_id);
CREATE INDEX idx_app_todos_visibility ON app_todos(tenant_id, visibility) WHERE visibility = 'public';

-- +goose StatementEnd

-- +goose Down
-- This redesign is one-way: the down migration restores columns but cannot
-- accurately reconstruct private/created_by/assigned_to/role_scope from
-- scope_id alone. Use only for local schema rollback during development.
-- +goose StatementBegin
ALTER TABLE app_todos
    ADD COLUMN private BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN created_by UUID,
    ADD COLUMN assigned_to UUID REFERENCES users(id),
    ADD COLUMN role_scope TEXT;

UPDATE app_todos t
SET private = (visibility = 'scoped'),
    assigned_to = s.user_id,
    role_scope = (SELECT name FROM roles WHERE id = s.role_id)
FROM scopes s
WHERE s.id = t.scope_id;

ALTER TABLE app_todos DROP COLUMN scope_id;
ALTER TABLE app_todos DROP COLUMN visibility;

CREATE INDEX idx_app_todos_assigned ON app_todos(tenant_id, assigned_to, status) WHERE assigned_to IS NOT NULL;
CREATE INDEX idx_app_todos_role ON app_todos(tenant_id, role_scope) WHERE role_scope IS NOT NULL;
-- +goose StatementEnd
