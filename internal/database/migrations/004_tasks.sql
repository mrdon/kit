-- +goose Up

-- User timezone (from Slack profile)
ALTER TABLE users ADD COLUMN timezone TEXT NOT NULL DEFAULT '';

-- Scheduled tasks
CREATE TABLE tasks (
    id          UUID PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES tenants ON DELETE CASCADE,
    created_by  UUID NOT NULL REFERENCES users ON DELETE CASCADE,
    description TEXT NOT NULL,
    cron_expr   TEXT NOT NULL,
    timezone    TEXT NOT NULL,
    channel_id  TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'active',
    next_run_at TIMESTAMPTZ NOT NULL,
    last_run_at TIMESTAMPTZ,
    last_error  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tasks_tenant ON tasks (tenant_id);
CREATE INDEX idx_tasks_due ON tasks (status, next_run_at) WHERE status = 'active';

-- Task scoping (same pattern as skill_scopes / rule_scopes)
CREATE TABLE task_scopes (
    tenant_id   UUID NOT NULL REFERENCES tenants ON DELETE CASCADE,
    task_id     UUID NOT NULL REFERENCES tasks ON DELETE CASCADE,
    scope_type  TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    PRIMARY KEY(tenant_id, task_id, scope_type, scope_value)
);

-- +goose Down
DROP TABLE IF EXISTS task_scopes;
DROP TABLE IF EXISTS tasks;
ALTER TABLE users DROP COLUMN IF EXISTS timezone;
