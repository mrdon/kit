-- +goose Up

-- One row per Slack thread that has at least one Netlify change
-- request in it. Used to group successive agent_runs together
-- (for future branch-chaining) and to know where to post the
-- "build ready" message back to.
--
-- production_branch is captured at thread-create time so that even
-- if someone changes the connected Netlify site's default branch
-- mid-thread, the merge target for ship-it stays consistent.
CREATE TABLE app_netlify_change_threads (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    slack_channel      TEXT NOT NULL,
    slack_thread_ts    TEXT NOT NULL,
    state              TEXT NOT NULL DEFAULT 'active',
                        -- 'active' | 'shipped' | 'abandoned'
    production_branch  TEXT NOT NULL,
    latest_agent_run_id UUID,
                        -- FK added below after agent_runs exists (circular)
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slack_channel, slack_thread_ts)
);

-- One row per Netlify Agent Run. Mirrors the upstream AgentRunner
-- (id, state, branches, preview URL) plus our own grouping +
-- post-processing fields (summary, result_diff cache).
CREATE TABLE app_netlify_agent_runs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    change_thread_id  UUID NOT NULL REFERENCES app_netlify_change_threads(id) ON DELETE CASCADE,
    netlify_run_id    TEXT NOT NULL UNIQUE,
                       -- Netlify's agent_runner id; we read updates from it
                       -- via GET /api/v1/agent_runners/<id>
    prompt            TEXT NOT NULL,
    base_branch       TEXT NOT NULL,
    result_branch     TEXT,
                       -- populated once the agent commits
    preview_url       TEXT,
                       -- latest_session_deploy_url; populated async by Netlify
    state             TEXT NOT NULL DEFAULT 'pending',
                       -- mirrors Netlify's state. Treat empty done_at as "still running".
    summary           TEXT,
                       -- plain-language diff summary, posted to Slack
    result_diff       TEXT,
                       -- cached unified diff (for branch-chaining context)
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at           TIMESTAMPTZ
);

-- Circular FK from change_threads.latest_agent_run_id to agent_runs.
ALTER TABLE app_netlify_change_threads
    ADD CONSTRAINT app_netlify_change_threads_latest_fk
    FOREIGN KEY (latest_agent_run_id)
    REFERENCES app_netlify_agent_runs(id)
    ON DELETE SET NULL;

-- For a future cron poller: pick up in-flight runs quickly.
CREATE INDEX idx_app_netlify_agent_runs_in_flight
    ON app_netlify_agent_runs (state)
    WHERE done_at IS NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_app_netlify_agent_runs_in_flight;
ALTER TABLE app_netlify_change_threads
    DROP CONSTRAINT IF EXISTS app_netlify_change_threads_latest_fk;
DROP TABLE IF EXISTS app_netlify_agent_runs;
DROP TABLE IF EXISTS app_netlify_change_threads;
