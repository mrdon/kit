-- +goose Up

-- Scheduled-job registry.
--
-- Kit ran scheduled work through three unrelated mechanisms: the jobs table,
-- per-app goroutine tickers (apps.CronJob), and a per-tick sweep hook. Only
-- the first left any trace. This migration adds the two columns that let
-- everything become a jobs row.

-- builtin_key is the stable identity of a code-registered job. Handler lookup
-- used to match on the description string, so renaming a builtin orphaned its
-- rows AND inserted duplicates. The key never changes; the description is now
-- free to be an editable label.
--
-- NULL for user-created (agent) and builder_script rows.
ALTER TABLE jobs ADD COLUMN builtin_key TEXT;

-- lane is the execution pool the row belongs to. Deliberately NOT derived
-- from job_type: some builtin registrations call the LLM (the task app's
-- email intake runs a full agent loop, the coordination sweep calls the
-- Messages API directly) and must stay in the serialized 'agent' lane, while
-- the rest of the builtins are IO-bound and safe to run wide.
ALTER TABLE jobs ADD COLUMN lane TEXT NOT NULL DEFAULT 'agent';

-- claimed_at is stamped when the claim query flips a row to 'running'.
-- Stuck-row recovery used to key off last_run_at, which is NULL on a row that
-- has never run — so every freshly-created registry row was instantly
-- eligible for recovery and could be double-claimed by a sibling scheduler.
ALTER TABLE jobs ADD COLUMN claimed_at TIMESTAMPTZ;

-- The one builtin that predates the registry.
UPDATE jobs
   SET builtin_key = 'system.profile_sync'
 WHERE job_type = 'builtin'
   AND description = 'Sync user profiles from Slack';

-- Native work moves off the serialized lane. Any builtin left without a key
-- above is an orphan from an older rename; the reconciler will retire it.
UPDATE jobs SET lane = 'function' WHERE job_type IN ('builtin', 'builder_script');

-- Rows already mid-flight when this deploys must not look ancient to the
-- recovery sweep.
UPDATE jobs SET claimed_at = now() WHERE status = 'running';

-- One row per (tenant, registered task). Partial so user-created and
-- builder_script rows (builtin_key NULL) are unaffected — no conflict with
-- jobs_builder_script_fn_unique, which keys off config JSONB.
CREATE UNIQUE INDEX idx_jobs_builtin_key
    ON jobs (tenant_id, builtin_key)
    WHERE builtin_key IS NOT NULL;

-- The claim query now filters by lane, so lane leads the index.
DROP INDEX IF EXISTS idx_jobs_due;
CREATE INDEX idx_jobs_due ON jobs (lane, status, next_run_at) WHERE status = 'active';

-- +goose Down

DROP INDEX IF EXISTS idx_jobs_due;
CREATE INDEX idx_jobs_due ON jobs (status, next_run_at) WHERE status = 'active';
DROP INDEX IF EXISTS idx_jobs_builtin_key;
ALTER TABLE jobs DROP COLUMN IF EXISTS claimed_at;
ALTER TABLE jobs DROP COLUMN IF EXISTS lane;
ALTER TABLE jobs DROP COLUMN IF EXISTS builtin_key;
