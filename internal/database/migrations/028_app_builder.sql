-- +goose Up

-- Logical bundles: CRM, mug_club, review_triage, etc. One per (tenant, name).
CREATE TABLE builder_apps (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    created_by  UUID REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

-- MongoDB-shaped polymorphic item store. "Collections" are logical (admin-chosen
-- strings) scoped to (tenant_id, builder_app_id, collection).
CREATE TABLE app_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    builder_app_id  UUID NOT NULL REFERENCES builder_apps(id) ON DELETE RESTRICT,
    collection      TEXT NOT NULL,
    data            JSONB NOT NULL,
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    script_run_id   UUID,  -- soft ref, no FK; survives script deletion
    caller_user_id  UUID REFERENCES users(id)
);

CREATE INDEX idx_app_items_scope       ON app_items (tenant_id, builder_app_id, collection);
CREATE INDEX idx_app_items_data_gin    ON app_items USING gin (data);
CREATE INDEX idx_app_items_script_run  ON app_items (script_run_id) WHERE script_run_id IS NOT NULL;

-- Temporal shadow of app_items. Each UPDATE/DELETE writes the prior row here
-- with valid_from = OLD.updated_at (or OLD.created_at when never updated) and
-- valid_to defaulting to now().
CREATE TABLE app_items_history (
    history_id      BIGSERIAL PRIMARY KEY,
    id              UUID NOT NULL,
    tenant_id       UUID NOT NULL,
    builder_app_id  UUID NOT NULL,
    collection      TEXT NOT NULL,
    data            JSONB NOT NULL,
    operation       TEXT NOT NULL CHECK (operation IN ('UPDATE', 'DELETE')),
    valid_from      TIMESTAMPTZ NOT NULL,
    valid_to        TIMESTAMPTZ NOT NULL DEFAULT now(),
    script_run_id   UUID,
    caller_user_id  UUID
);

CREATE INDEX idx_app_items_history_id      ON app_items_history (id, valid_to);
CREATE INDEX idx_app_items_history_scope   ON app_items_history (tenant_id, builder_app_id, collection, valid_to);
CREATE INDEX idx_app_items_history_run     ON app_items_history (script_run_id) WHERE script_run_id IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_items_history_record() RETURNS TRIGGER AS $$
DECLARE
    op   TEXT;
    vfrm TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        op := 'UPDATE';
    ELSIF TG_OP = 'DELETE' THEN
        op := 'DELETE';
    ELSE
        RETURN NULL;
    END IF;

    -- valid_from = when the OLD row's state began. Rows that were never updated
    -- since insert have updated_at = created_at, but GREATEST is safe either way.
    vfrm := GREATEST(OLD.created_at, OLD.updated_at);

    INSERT INTO app_items_history (
        id, tenant_id, builder_app_id, collection, data, operation,
        valid_from, script_run_id, caller_user_id
    ) VALUES (
        OLD.id, OLD.tenant_id, OLD.builder_app_id, OLD.collection, OLD.data, op,
        vfrm, OLD.script_run_id, OLD.caller_user_id
    );

    IF TG_OP = 'UPDATE' THEN
        RETURN NEW;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER app_items_history_trg
    AFTER UPDATE OR DELETE ON app_items
    FOR EACH ROW EXECUTE FUNCTION app_items_history_record();

-- Per-tenant builder configuration knobs.
CREATE TABLE tenant_builder_config (
    tenant_id              UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    history_retention_days INTEGER,                          -- NULL = unlimited
    max_db_calls_per_run   INTEGER NOT NULL DEFAULT 1000,
    llm_daily_cent_cap     INTEGER NOT NULL DEFAULT 500,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Scripts: one per (tenant, builder_app, name). current_rev_id points at the
-- active revision; initially NULL (chicken-and-egg with script_revisions).
CREATE TABLE scripts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    builder_app_id  UUID NOT NULL REFERENCES builder_apps(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    description     TEXT,
    current_rev_id  UUID,
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, builder_app_id, name)
);

-- Immutable script revisions. Every create_script / update_script appends one.
CREATE TABLE script_revisions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    script_id  UUID NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Close the loop: scripts.current_rev_id -> script_revisions.id.
ALTER TABLE scripts
    ADD CONSTRAINT scripts_current_rev_fk
    FOREIGN KEY (current_rev_id) REFERENCES script_revisions(id);

-- One row per script invocation, including cross-script calls (chained via
-- parent_run_id) and scheduled runs.
CREATE TABLE script_runs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    script_id         UUID NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    revision_id       UUID NOT NULL REFERENCES script_revisions(id),
    fn_called         TEXT,
    args              JSONB,
    result            JSONB,
    status            TEXT NOT NULL
                      CHECK (status IN ('running', 'completed', 'error', 'limit_exceeded', 'cancelled')),
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ,
    duration_ms       BIGINT,
    tokens_used       INTEGER,
    cost_cents        INTEGER,
    error             TEXT,
    triggered_by      TEXT CHECK (triggered_by IN ('manual', 'schedule', 'exposed_tool', 'tools_call')),
    parent_run_id     UUID REFERENCES script_runs(id),
    mutation_summary  JSONB,       -- {"inserts": N, "updates": M, "deletes": K}
    caller_user_id    UUID REFERENCES users(id)
);

CREATE INDEX idx_script_runs_script_time ON script_runs (tenant_id, script_id, started_at DESC);

-- Scheduled builder script runs live on the existing tasks table as
-- task_type='builder_script'. config JSONB carries {"script_id","fn_name"};
-- the scheduler's generic TaskRunner dispatch routes these rows to the
-- builder runner. See internal/scheduler/runner.go for the registry and
-- internal/apps/builder/builder_runner.go for the dispatcher.
ALTER TABLE tasks ADD COLUMN config JSONB;

-- Prevent two active schedules for the same (script, fn) — a duplicate
-- would double-fire every tick. Partial unique index because inactive
-- rows may accumulate as admins unschedule + reschedule.
CREATE UNIQUE INDEX tasks_builder_script_fn_unique
    ON tasks (tenant_id, (config->>'script_id'), (config->>'fn_name'))
    WHERE task_type = 'builder_script' AND status = 'active';

-- Admin-published script functions surfaced as regular LLM/MCP tools.
-- Tenant-scoped (not app-scoped) so cross-app composition works via tools.call.
CREATE TABLE exposed_tools (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    tool_name         TEXT NOT NULL,
    script_id         UUID NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    fn_name           TEXT NOT NULL,
    description       TEXT,
    args_schema       JSONB,
    visible_to_roles  TEXT[],
    is_stale          BOOLEAN NOT NULL DEFAULT FALSE,
    created_by        UUID REFERENCES users(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, tool_name)
);

-- Per-run log lines written via the runtime's log() built-in.
CREATE TABLE script_logs (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    script_run_id  UUID NOT NULL REFERENCES script_runs(id) ON DELETE CASCADE,
    level          TEXT NOT NULL CHECK (level IN ('debug', 'info', 'warn', 'error')),
    message        TEXT NOT NULL,
    fields         JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_script_logs_run ON script_logs (script_run_id, id);

-- One row per llm.* call. args_hash indexed for future cache lookups (v0.2+).
-- script_run_id SET NULL on script_run delete so the cost record survives.
CREATE TABLE llm_call_log (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    script_run_id  UUID REFERENCES script_runs(id) ON DELETE SET NULL,
    fn             TEXT NOT NULL CHECK (fn IN ('classify', 'extract', 'summarize', 'generate')),
    model_tier     TEXT NOT NULL,
    args_hash      TEXT NOT NULL,
    args_payload   JSONB,
    result         JSONB,
    tokens_in      INTEGER,
    tokens_out     INTEGER,
    cost_cents     INTEGER,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_llm_call_log_cache ON llm_call_log (tenant_id, args_hash, created_at DESC);

-- +goose Down

DROP INDEX IF EXISTS idx_llm_call_log_cache;
DROP TABLE IF EXISTS llm_call_log;

DROP INDEX IF EXISTS idx_script_logs_run;
DROP TABLE IF EXISTS script_logs;

DROP TABLE IF EXISTS exposed_tools;

DROP INDEX IF EXISTS tasks_builder_script_fn_unique;
ALTER TABLE tasks DROP COLUMN IF EXISTS config;

DROP INDEX IF EXISTS idx_script_runs_script_time;
DROP TABLE IF EXISTS script_runs;

ALTER TABLE scripts DROP CONSTRAINT IF EXISTS scripts_current_rev_fk;
DROP TABLE IF EXISTS script_revisions;
DROP TABLE IF EXISTS scripts;

DROP TABLE IF EXISTS tenant_builder_config;

DROP TRIGGER IF EXISTS app_items_history_trg ON app_items;
DROP FUNCTION IF EXISTS app_items_history_record();

DROP INDEX IF EXISTS idx_app_items_history_run;
DROP INDEX IF EXISTS idx_app_items_history_scope;
DROP INDEX IF EXISTS idx_app_items_history_id;
DROP TABLE IF EXISTS app_items_history;

DROP INDEX IF EXISTS idx_app_items_script_run;
DROP INDEX IF EXISTS idx_app_items_data_gin;
DROP INDEX IF EXISTS idx_app_items_scope;
DROP TABLE IF EXISTS app_items;

DROP TABLE IF EXISTS builder_apps;
