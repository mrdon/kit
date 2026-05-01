-- +goose Up

-- Rename the todos system to "tasks" — kit's user-facing tracker for
-- team work items. Pure rename here; semantic changes (assignee, role-
-- only scoping, visibility removal) ship in 045.

ALTER TABLE app_todos RENAME TO app_tasks;
ALTER TABLE app_todo_events RENAME TO app_task_events;
ALTER TABLE app_task_events RENAME COLUMN todo_id TO task_id;

-- Indexes (Postgres doesn't auto-rename indexes when their table renames).
ALTER INDEX app_todos_pkey RENAME TO app_tasks_pkey;
ALTER INDEX idx_app_todos_scope RENAME TO idx_app_tasks_scope;
ALTER INDEX idx_app_todos_search RENAME TO idx_app_tasks_search;
ALTER INDEX idx_app_todos_snoozed_until RENAME TO idx_app_tasks_snoozed_until;
ALTER INDEX idx_app_todos_tenant_status_due RENAME TO idx_app_tasks_tenant_status_due;
ALTER INDEX idx_app_todos_visibility RENAME TO idx_app_tasks_visibility;
ALTER INDEX app_todo_events_pkey RENAME TO app_task_events_pkey;
ALTER INDEX idx_app_todo_events_recent RENAME TO idx_app_task_events_recent;
ALTER INDEX idx_app_todo_events_todo RENAME TO idx_app_task_events_task;

-- Constraints.
ALTER TABLE app_tasks RENAME CONSTRAINT app_todos_tenant_id_fkey TO app_tasks_tenant_id_fkey;
ALTER TABLE app_tasks RENAME CONSTRAINT app_todos_status_check TO app_tasks_status_check;
ALTER TABLE app_tasks RENAME CONSTRAINT app_todos_priority_check TO app_tasks_priority_check;
ALTER TABLE app_tasks RENAME CONSTRAINT app_todos_visibility_check TO app_tasks_visibility_check;
ALTER TABLE app_tasks RENAME CONSTRAINT app_todos_scope_id_fkey TO app_tasks_scope_id_fkey;
ALTER TABLE app_task_events RENAME CONSTRAINT app_todo_events_tenant_id_fkey TO app_task_events_tenant_id_fkey;
ALTER TABLE app_task_events RENAME CONSTRAINT app_todo_events_todo_id_fkey TO app_task_events_task_id_fkey;
ALTER TABLE app_task_events RENAME CONSTRAINT app_todo_events_author_id_fkey TO app_task_events_author_id_fkey;
ALTER TABLE app_task_events RENAME CONSTRAINT app_todo_events_event_type_check TO app_task_events_event_type_check;

-- +goose Down

ALTER TABLE app_task_events RENAME CONSTRAINT app_task_events_event_type_check TO app_todo_events_event_type_check;
ALTER TABLE app_task_events RENAME CONSTRAINT app_task_events_author_id_fkey TO app_todo_events_author_id_fkey;
ALTER TABLE app_task_events RENAME CONSTRAINT app_task_events_task_id_fkey TO app_todo_events_todo_id_fkey;
ALTER TABLE app_task_events RENAME CONSTRAINT app_task_events_tenant_id_fkey TO app_todo_events_tenant_id_fkey;
ALTER TABLE app_tasks RENAME CONSTRAINT app_tasks_scope_id_fkey TO app_todos_scope_id_fkey;
ALTER TABLE app_tasks RENAME CONSTRAINT app_tasks_visibility_check TO app_todos_visibility_check;
ALTER TABLE app_tasks RENAME CONSTRAINT app_tasks_priority_check TO app_todos_priority_check;
ALTER TABLE app_tasks RENAME CONSTRAINT app_tasks_status_check TO app_todos_status_check;
ALTER TABLE app_tasks RENAME CONSTRAINT app_tasks_tenant_id_fkey TO app_todos_tenant_id_fkey;

ALTER INDEX idx_app_task_events_task RENAME TO idx_app_todo_events_todo;
ALTER INDEX idx_app_task_events_recent RENAME TO idx_app_todo_events_recent;
ALTER INDEX app_task_events_pkey RENAME TO app_todo_events_pkey;
ALTER INDEX idx_app_tasks_visibility RENAME TO idx_app_todos_visibility;
ALTER INDEX idx_app_tasks_tenant_status_due RENAME TO idx_app_todos_tenant_status_due;
ALTER INDEX idx_app_tasks_snoozed_until RENAME TO idx_app_todos_snoozed_until;
ALTER INDEX idx_app_tasks_search RENAME TO idx_app_todos_search;
ALTER INDEX idx_app_tasks_scope RENAME TO idx_app_todos_scope;
ALTER INDEX app_tasks_pkey RENAME TO app_todos_pkey;

ALTER TABLE app_task_events RENAME COLUMN task_id TO todo_id;
ALTER TABLE app_task_events RENAME TO app_todo_events;
ALTER TABLE app_tasks RENAME TO app_todos;
