-- +goose Up

-- Add netlify_runner_id to distinguish first-turn rows (where netlify_run_id
-- IS the agent_runner id) from follow-up session rows (where netlify_run_id
-- is the session id within a parent runner).
--
-- Iteration in Netlify is modelled as: one agent_runner has many sessions.
-- POST /agent_runners creates the runner + first session. POST /agent_runners/
-- {id}/sessions adds follow-up turns. We treat each Slack thread as a single
-- runner with N sessions underneath it, one per user message in the thread.
ALTER TABLE app_netlify_agent_runs
    ADD COLUMN netlify_runner_id TEXT;

-- Backfill: for existing rows, the run_id was the runner_id (we hadn't yet
-- introduced sessions). Set runner_id = run_id so they're treated as
-- first-turn rows by the watcher.
UPDATE app_netlify_agent_runs SET netlify_runner_id = netlify_run_id
    WHERE netlify_runner_id IS NULL;

-- Going forward we require it; the column stays nullable for compatibility
-- but the application writes it on every insert.

-- +goose Down

ALTER TABLE app_netlify_agent_runs DROP COLUMN IF EXISTS netlify_runner_id;
