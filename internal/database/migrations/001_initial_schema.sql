-- +goose Up

-- Multi-tenant core
CREATE TABLE tenants (
    id UUID PRIMARY KEY,
    slack_team_id TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    bot_token TEXT NOT NULL,
    business_type TEXT,
    timezone TEXT DEFAULT 'UTC',
    setup_complete BOOLEAN DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    slack_user_id TEXT NOT NULL,
    display_name TEXT,
    is_admin BOOLEAN DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, slack_user_id)
);

CREATE TABLE roles (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    name TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, name)
);

CREATE TABLE user_roles (
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    user_id UUID NOT NULL REFERENCES users(id),
    role_id UUID NOT NULL REFERENCES roles(id),
    PRIMARY KEY(tenant_id, user_id, role_id)
);

-- Skills engine
CREATE TABLE skills (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    content TEXT NOT NULL,
    user_invocable BOOLEAN DEFAULT true,
    source TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE skill_references (
    id UUID PRIMARY KEY,
    skill_id UUID REFERENCES skills(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    filename TEXT NOT NULL,
    content TEXT NOT NULL
);

CREATE TABLE skill_scopes (
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    skill_id UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    scope_type TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    PRIMARY KEY(tenant_id, skill_id, scope_type, scope_value)
);

-- Rules
CREATE TABLE rules (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    content TEXT NOT NULL,
    priority INT DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE rule_scopes (
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    rule_id UUID NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
    scope_type TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    PRIMARY KEY(tenant_id, rule_id, scope_type, scope_value)
);

-- Memory
CREATE TABLE memories (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    content TEXT NOT NULL,
    scope_type TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    source_session_id UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

-- Sessions
CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    slack_thread_ts TEXT NOT NULL,
    slack_channel_id TEXT NOT NULL,
    user_id UUID REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, slack_channel_id, slack_thread_ts)
);

CREATE TABLE session_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    session_id UUID NOT NULL REFERENCES sessions(id),
    event_type TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

-- Add foreign key for memories.source_session_id now that sessions table exists
ALTER TABLE memories
    ADD CONSTRAINT memories_source_session_id_fkey
    FOREIGN KEY (source_session_id) REFERENCES sessions(id);

-- FTS indexes
CREATE INDEX idx_skills_content_fts ON skills USING GIN (to_tsvector('english', content));
CREATE INDEX idx_skills_description_fts ON skills USING GIN (to_tsvector('english', description));
CREATE INDEX idx_skill_references_content_fts ON skill_references USING GIN (to_tsvector('english', content));
CREATE INDEX idx_memories_content_fts ON memories USING GIN (to_tsvector('english', content));

-- Lookup indexes
CREATE INDEX idx_users_tenant_id ON users(tenant_id);
CREATE INDEX idx_roles_tenant_id ON roles(tenant_id);
CREATE INDEX idx_skills_tenant_id ON skills(tenant_id);
CREATE INDEX idx_rules_tenant_id ON rules(tenant_id);
CREATE INDEX idx_memories_tenant_id ON memories(tenant_id);
CREATE INDEX idx_sessions_tenant_id ON sessions(tenant_id);
CREATE INDEX idx_session_events_session_id ON session_events(session_id);
CREATE INDEX idx_session_events_tenant_id ON session_events(tenant_id);

-- +goose Down

DROP TABLE IF EXISTS session_events;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS memories;
DROP TABLE IF EXISTS rule_scopes;
DROP TABLE IF EXISTS rules;
DROP TABLE IF EXISTS skill_scopes;
DROP TABLE IF EXISTS skill_references;
DROP TABLE IF EXISTS skills;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
