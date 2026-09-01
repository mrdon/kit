-- +goose Up

-- Drop the tables behind six retired apps: expense, voting, coordination,
-- netlify, github and builder.
--
-- Each was removed in its own commit; this is the data half. The evidence
-- for retiring them was that nothing was using them: expense never held a
-- single row, voting last saw a vote on 2026-04-30, coordination on
-- 2026-04-29, netlify on 2026-05-14, and the builder's only app failed
-- every scheduled run from April to August. The one production tenant had
-- already switched all six off.
--
-- This is not reversible and the Down below says so rather than pretending
-- otherwise. Anything worth keeping should be exported before this runs.

-- Expense: children (items, events) before reports; policy is standalone.
DROP TABLE IF EXISTS app_expense_report_events;
DROP TABLE IF EXISTS app_expense_items;
DROP TABLE IF EXISTS app_expense_reports;
DROP TABLE IF EXISTS app_expense_policy;

-- Voting.
DROP TABLE IF EXISTS app_vote_participants;
DROP TABLE IF EXISTS app_votes;

-- Coordination. app_coordinations holds an FK to jobs(shepherd_job_id),
-- so it goes before any job cleanup below.
DROP TABLE IF EXISTS app_coordination_participants;
DROP TABLE IF EXISTS app_coordinations;

-- Netlify. agent_runs and change_threads reference each other (a thread
-- points at its latest run, a run points back at its thread), so neither
-- can go first -- they have to be named in one DROP.
DROP TABLE IF EXISTS app_netlify_agent_runs, app_netlify_change_threads;
DROP TABLE IF EXISTS app_netlify_config;

-- GitHub App installs. Only ever a substrate for netlify's pull requests.
DROP TABLE IF EXISTS app_github_installations;

-- Builder. Dropped leaf-first: llm_call_log and script_logs reference
-- script_runs; script_runs and exposed_tools reference scripts.
DROP TABLE IF EXISTS llm_call_log;
DROP TABLE IF EXISTS script_logs;
DROP TABLE IF EXISTS script_runs;
DROP TABLE IF EXISTS exposed_tools;
-- scripts and script_revisions reference each other (a script points at
-- its current revision, a revision points back at its script), so they
-- have to be named in one DROP -- same shape as the netlify pair above.
DROP TABLE IF EXISTS scripts, script_revisions;
DROP TABLE IF EXISTS app_items_history;
DROP TABLE IF EXISTS app_items;
DROP TABLE IF EXISTS builder_apps;
DROP TABLE IF EXISTS tenant_builder_config;

-- The temporal trigger function that wrote app_items_history rows. Its
-- trigger went with the table; the function would otherwise linger.
DROP FUNCTION IF EXISTS app_items_history_record();

-- Scheduled builder scripts. job_type='builder_script' no longer has a
-- runner, so these rows would be claimed forever and never dispatch.
-- job_scopes rows cascade on the FK.
DELETE FROM jobs WHERE job_type = 'builder_script';

-- Both partial unique indexes are builder-script-specific. The tasks_*
-- name is a leftover from the tasks -> jobs rename, which copied the
-- index without dropping the original.
DROP INDEX IF EXISTS jobs_builder_script_fn_unique;
DROP INDEX IF EXISTS tasks_builder_script_fn_unique;

-- Enablement rows for the retired apps. Harmless if left (nothing reads a
-- name that no longer registers), but they show up in the Apps admin page
-- query and in any audit of what a tenant has turned off.
DELETE FROM tenant_app_enablement
WHERE app_name IN ('expense', 'voting', 'coordination', 'netlify', 'github', 'builder');

-- +goose Down

-- Deliberately not reversible. Recreating 22 empty tables would restore
-- the schema but not the data, and would reintroduce a scripting runtime,
-- an expense workflow and a PR-publishing pipeline that no longer have any
-- Go code behind them. To go back, revert the app-removal commits and
-- restore from a backup taken before this migration ran.
SELECT 1;
