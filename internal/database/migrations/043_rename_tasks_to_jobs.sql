-- +goose Up

-- Rename the cron-task system to "jobs" so the noun "task" is freed for
-- the team work-item meaning shipping in 044/045. Schema and behaviour
-- are unchanged here — pure rename.

-- Tables
ALTER TABLE tasks RENAME TO jobs;
ALTER TABLE task_scopes RENAME TO job_scopes;
ALTER TABLE job_scopes RENAME COLUMN task_id TO job_id;

-- Column on jobs that names the kind of scheduled work (agent, builtin,
-- builder_script). Rename so Go-side identifiers can be `job_type`.
ALTER TABLE jobs RENAME COLUMN task_type TO job_type;

-- The partial unique index on jobs uses `task_type = 'builder_script'`.
-- Rebuild it with the new column name.
DROP INDEX IF EXISTS jobs_builder_script_fn_unique;
CREATE UNIQUE INDEX jobs_builder_script_fn_unique
    ON jobs (tenant_id, (config->>'script_id'), (config->>'fn_name'))
    WHERE job_type = 'builder_script' AND status = 'active';

-- Rename FK constraints carried over from the original `tasks` creation in
-- migration 004 so they aren't permanently misnamed.
ALTER TABLE jobs RENAME CONSTRAINT tasks_created_by_fkey TO jobs_created_by_fkey;
ALTER TABLE jobs RENAME CONSTRAINT tasks_resume_session_id_fkey TO jobs_resume_session_id_fkey;
ALTER TABLE jobs RENAME CONSTRAINT tasks_skill_id_fkey TO jobs_skill_id_fkey;

-- Indexes (Postgres doesn't auto-rename indexes when their table renames).
-- `tasks_builder_script_fn_unique` is rebuilt above (rename + new column name
-- in the predicate); the rest just need a name change.
ALTER INDEX idx_tasks_tenant RENAME TO idx_jobs_tenant;
ALTER INDEX idx_tasks_due RENAME TO idx_jobs_due;

-- Constraint name (PRIMARY KEY) — auto-named "tasks_pkey" when 004 created
-- the table. Postgres doesn't rename PK constraints when their table is
-- renamed; rename explicitly so future migrations referencing "jobs_pkey"
-- aren't surprised.
ALTER TABLE jobs RENAME CONSTRAINT tasks_pkey TO jobs_pkey;
ALTER TABLE job_scopes RENAME CONSTRAINT task_scopes_pkey TO job_scopes_pkey;

-- FK columns in unrelated tables that reference cron-tasks. Renaming the
-- target table preserves the FK target but leaves the column names
-- misleading; rename them too.
ALTER TABLE app_card_decisions RENAME COLUMN origin_task_id TO origin_job_id;
ALTER TABLE app_card_decisions RENAME COLUMN resolved_task_id TO resolved_job_id;
ALTER TABLE app_coordinations RENAME COLUMN shepherd_task_id TO shepherd_job_id;

-- The partial index on app_card_decisions(origin_task_id) doesn't need a
-- structural change — just the name follows.
ALTER INDEX idx_app_card_decisions_origin_task RENAME TO idx_app_card_decisions_origin_job;

-- +goose Down

ALTER INDEX idx_app_card_decisions_origin_job RENAME TO idx_app_card_decisions_origin_task;
ALTER TABLE app_coordinations RENAME COLUMN shepherd_job_id TO shepherd_task_id;
ALTER TABLE app_card_decisions RENAME COLUMN resolved_job_id TO resolved_task_id;
ALTER TABLE app_card_decisions RENAME COLUMN origin_job_id TO origin_task_id;

ALTER TABLE jobs RENAME CONSTRAINT jobs_skill_id_fkey TO tasks_skill_id_fkey;
ALTER TABLE jobs RENAME CONSTRAINT jobs_resume_session_id_fkey TO tasks_resume_session_id_fkey;
ALTER TABLE jobs RENAME CONSTRAINT jobs_created_by_fkey TO tasks_created_by_fkey;

DROP INDEX IF EXISTS jobs_builder_script_fn_unique;
ALTER TABLE jobs RENAME COLUMN job_type TO task_type;
CREATE UNIQUE INDEX tasks_builder_script_fn_unique
    ON jobs (tenant_id, (config->>'script_id'), (config->>'fn_name'))
    WHERE task_type = 'builder_script' AND status = 'active';

ALTER TABLE job_scopes RENAME CONSTRAINT job_scopes_pkey TO task_scopes_pkey;
ALTER TABLE jobs RENAME CONSTRAINT jobs_pkey TO tasks_pkey;

ALTER INDEX idx_jobs_due RENAME TO idx_tasks_due;
ALTER INDEX idx_jobs_tenant RENAME TO idx_tasks_tenant;

ALTER TABLE job_scopes RENAME COLUMN job_id TO task_id;
ALTER TABLE job_scopes RENAME TO task_scopes;
ALTER TABLE jobs RENAME TO tasks;
