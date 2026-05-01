-- +goose Up

-- Decouple assignment from scope. Assignment is who the task is on now;
-- scope is which role owns it. Anyone in the role can see and edit a
-- task regardless of who it's assigned to.
ALTER TABLE app_tasks
  ADD COLUMN assignee_user_id UUID NULL REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX idx_app_tasks_assignee
  ON app_tasks(tenant_id, assignee_user_id, status)
  WHERE assignee_user_id IS NOT NULL;

-- Primary role per user. Used by the task-create resolver as a default
-- when the caller has multiple roles and doesn't pass role_scope.
ALTER TABLE users
  ADD COLUMN primary_role_id UUID NULL REFERENCES roles(id) ON DELETE SET NULL;

-- Backfill assignee from any user-scoped legacy row, but only for active
-- (non-closed) tasks — a closed personal todo shouldn't pop onto someone's
-- "assigned to me" list under the new model.
UPDATE app_tasks t
SET assignee_user_id = s.user_id
FROM scopes s
WHERE t.scope_id = s.id
  AND s.user_id IS NOT NULL
  AND t.status NOT IN ('done','cancelled');

-- Tenant-aware repointing of non-role-scoped tasks (data-driven from
-- planning against prod). Each (tenant, target_role) row gets its
-- canonical scope row created if missing, then every non-role-scoped
-- task in that tenant repoints to it. The existing partial unique index
-- on `scopes` is `WHERE role_id IS NOT NULL`; the ON CONFLICT predicate
-- below matches that index.
INSERT INTO scopes (tenant_id, role_id, user_id)
SELECT DISTINCT te.id, r.id, NULL::uuid
FROM tenants te
JOIN (VALUES
  ('Monarch Bands',   'board'),
  ('Gravity Brewing', 'founder'),
  ('Louisville DBA',  'board'),
  ('Sleuth',          'member'),
  ('Unofficial Team B','member')
) AS tt(tenant_name, target_role) ON tt.tenant_name = te.name
JOIN roles r ON r.tenant_id = te.id AND r.name = tt.target_role
ON CONFLICT (tenant_id, role_id) WHERE role_id IS NOT NULL DO NOTHING;

UPDATE app_tasks t
SET scope_id = (
  SELECT sc.id
  FROM scopes sc
  JOIN roles r ON r.id = sc.role_id
  JOIN tenants te ON te.id = sc.tenant_id
  JOIN (VALUES
    ('Monarch Bands',   'board'),
    ('Gravity Brewing', 'founder'),
    ('Louisville DBA',  'board'),
    ('Sleuth',          'member'),
    ('Unofficial Team B','member')
  ) AS tt(tenant_name, target_role) ON tt.tenant_name = te.name AND tt.target_role = r.name
  WHERE sc.tenant_id = t.tenant_id
)
FROM scopes s
WHERE t.scope_id = s.id
  AND s.role_id IS NULL;

-- Drop visibility. With role-only ownership there is no "public"
-- middle ground — if you need cross-team visibility, join the other role.
DROP INDEX IF EXISTS idx_app_tasks_visibility;
ALTER TABLE app_tasks DROP COLUMN visibility;

-- Allow new audit event type for assignee changes (separate from
-- assignment, which now means "scope was re-pointed to a different role").
ALTER TABLE app_task_events
  DROP CONSTRAINT app_task_events_event_type_check,
  ADD CONSTRAINT app_task_events_event_type_check
    CHECK (event_type IN ('comment','status_change','assignment','priority_change','assignee_change'));

-- +goose Down

ALTER TABLE app_task_events
  DROP CONSTRAINT app_task_events_event_type_check,
  ADD CONSTRAINT app_task_events_event_type_check
    CHECK (event_type IN ('comment','status_change','assignment','priority_change'));

ALTER TABLE app_tasks ADD COLUMN visibility TEXT NOT NULL DEFAULT 'scoped';
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_visibility_check
  CHECK (visibility = ANY (ARRAY['scoped','public']));
CREATE INDEX idx_app_tasks_visibility ON app_tasks(tenant_id, visibility)
  WHERE visibility = 'public';

-- Best-effort revert of the per-tenant rescoping is not provided; the
-- forward migration loses information (which tasks were originally user-
-- scoped). Restore from backup if you need the original shape.

ALTER TABLE users DROP COLUMN primary_role_id;
DROP INDEX IF EXISTS idx_app_tasks_assignee;
ALTER TABLE app_tasks DROP COLUMN assignee_user_id;
