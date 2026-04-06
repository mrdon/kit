-- +goose Up

CREATE TABLE app_todos (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id),
    title          TEXT NOT NULL,
    description    TEXT,
    status         TEXT NOT NULL DEFAULT 'open'
                   CHECK (status IN ('open', 'in_progress', 'blocked', 'done')),
    priority       TEXT NOT NULL DEFAULT 'medium'
                   CHECK (priority IN ('low', 'medium', 'high', 'urgent')),
    blocked_reason TEXT,
    private        BOOLEAN NOT NULL DEFAULT false,
    assigned_to    UUID REFERENCES users(id),
    role_scope     TEXT,
    due_date       DATE,
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at      TIMESTAMPTZ
);

CREATE INDEX idx_app_todos_tenant_status_due ON app_todos(tenant_id, status, due_date);
CREATE INDEX idx_app_todos_assigned ON app_todos(tenant_id, assigned_to, status)
    WHERE assigned_to IS NOT NULL;
CREATE INDEX idx_app_todos_role ON app_todos(tenant_id, role_scope)
    WHERE role_scope IS NOT NULL;
CREATE INDEX idx_app_todos_search ON app_todos
    USING gin(to_tsvector('english', coalesce(title, '') || ' ' || coalesce(description, '')));

CREATE TABLE app_todo_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id),
    todo_id    UUID NOT NULL REFERENCES app_todos(id) ON DELETE CASCADE,
    author_id  UUID REFERENCES users(id),
    event_type TEXT NOT NULL
               CHECK (event_type IN ('comment', 'status_change', 'assignment', 'priority_change')),
    content    TEXT,
    old_value  TEXT,
    new_value  TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_todo_events_todo ON app_todo_events(tenant_id, todo_id, created_at);
CREATE INDEX idx_app_todo_events_recent ON app_todo_events(tenant_id, created_at);

-- +goose Down

DROP TABLE IF EXISTS app_todo_events;
DROP TABLE IF EXISTS app_todos;
