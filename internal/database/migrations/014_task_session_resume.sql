-- +goose Up
-- Set by ResolveDecision to tell the scheduler "on your next run of this
-- task, reuse this session instead of minting a fresh one". Consumed
-- (cleared) by the scheduler at claim time. Unused for tasks that aren't
-- waiting on a decision.
ALTER TABLE tasks
    ADD COLUMN resume_session_id UUID REFERENCES sessions(id) ON DELETE SET NULL;

-- Decisions created inside a task-run agent carry both the task to wake
-- and the session to append the resolution event to. Ad-hoc decisions
-- (manual/external MCP) leave both null and fall through to the existing
-- option-prompt path.
ALTER TABLE app_card_decisions
    ADD COLUMN origin_task_id    UUID REFERENCES tasks(id) ON DELETE SET NULL,
    ADD COLUMN origin_session_id UUID REFERENCES sessions(id) ON DELETE SET NULL;

CREATE INDEX idx_app_card_decisions_origin_task
    ON app_card_decisions(origin_task_id)
    WHERE origin_task_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_app_card_decisions_origin_task;
ALTER TABLE app_card_decisions DROP COLUMN IF EXISTS origin_session_id;
ALTER TABLE app_card_decisions DROP COLUMN IF EXISTS origin_task_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS resume_session_id;
