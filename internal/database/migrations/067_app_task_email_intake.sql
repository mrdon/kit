-- +goose Up

-- Per-user email-to-task intake config + watermark. Opt-in: a mailbox user
-- turns this on in the Tasks console; the task app's cron sweep then, on each
-- user's own schedule, discovers new mail since last_scanned_at, seeds the
-- summaries into an agent session, and lets the agent create tasks.
--
-- One row per (tenant, mailbox user). No tenant-default row — a config row
-- only exists for a real user who has opted in, and the sweep runs as that
-- user's identity.
--
-- Instructions are NOT stored here in full: the durable triage prose ships in
-- the binary (task app template) and is always current. Only extra_instructions
-- (a per-user append) is persisted, so improving the default reaches everyone
-- and users can't edit out the core logic.
CREATE TABLE app_task_email_intake (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enabled            BOOLEAN NOT NULL DEFAULT false,
    schedule           TEXT NOT NULL DEFAULT '0 7 * * *',
    extra_instructions TEXT NOT NULL DEFAULT '',
    -- Precise watermark: the newest email the sweep has already presented to
    -- the agent. Also the cutoff for the next discovery. NULL = never scanned
    -- (first run bounds itself to a recent window).
    last_scanned_at    TIMESTAMPTZ,
    -- Rolling-deploy double-run guard. A sweep atomically stamps this before
    -- running a row; a second instance skips a freshly-claimed row.
    claimed_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, user_id)
);

-- Sweep enumerates enabled rows per tenant; tenant-leading index.
CREATE INDEX idx_app_task_email_intake_enabled
    ON app_task_email_intake (tenant_id, enabled);

-- +goose Down
DROP TABLE IF EXISTS app_task_email_intake;
