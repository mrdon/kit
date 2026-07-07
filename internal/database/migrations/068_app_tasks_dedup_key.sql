-- +goose Up

-- Deterministic idempotency for tasks created by the email→task intake agent.
-- The agent passes the source email's uid; the task app stores a per-user key
-- ("email:<user_id>:<uid>") here. Before this, dedup was advisory only — the
-- intake prompt asked the agent to eyeball list_tasks — so retries, overlapping
-- scans, and the 50-row list cap let the same email spawn task after task, and
-- cancelling a duplicate did nothing to stop the next run recreating it.
--
-- The unique index spans EVERY status (including cancelled and done): once a
-- source email has produced a task, it can never produce another, even after
-- the user cancels the first. That is the whole point — a cancelled duplicate
-- must stay dead. Partial (dedup_key IS NOT NULL) so hand- and MCP-created
-- tasks, which carry no key, are entirely unconstrained.
ALTER TABLE app_tasks ADD COLUMN dedup_key TEXT;

CREATE UNIQUE INDEX app_tasks_dedup_key_unique
    ON app_tasks (tenant_id, dedup_key)
    WHERE dedup_key IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS app_tasks_dedup_key_unique;
ALTER TABLE app_tasks DROP COLUMN IF EXISTS dedup_key;
