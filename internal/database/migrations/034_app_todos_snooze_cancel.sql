-- +goose Up
-- +goose StatementBegin

ALTER TABLE app_todos ADD COLUMN snoozed_until TIMESTAMPTZ;

CREATE INDEX idx_app_todos_snoozed_until ON app_todos(tenant_id, snoozed_until)
    WHERE snoozed_until IS NOT NULL;

-- The original status CHECK (migration 007) was inline; Postgres named it
-- by the conventional <table>_<column>_check pattern. Drop it before adding
-- the widened version that includes 'cancelled' as a soft-delete status.
ALTER TABLE app_todos DROP CONSTRAINT IF EXISTS app_todos_status_check;
ALTER TABLE app_todos ADD CONSTRAINT app_todos_status_check
    CHECK (status IN ('open','in_progress','blocked','done','cancelled'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Fail loudly if any cancelled rows exist — re-narrowing the CHECK would
-- otherwise produce a cryptic constraint violation.
DO $$
DECLARE
    n int;
BEGIN
    SELECT count(*) INTO n FROM app_todos WHERE status = 'cancelled';
    IF n > 0 THEN
        RAISE EXCEPTION 'cannot roll back: % app_todos rows have status=cancelled', n;
    END IF;
END $$;

ALTER TABLE app_todos DROP CONSTRAINT IF EXISTS app_todos_status_check;
ALTER TABLE app_todos ADD CONSTRAINT app_todos_status_check
    CHECK (status IN ('open','in_progress','blocked','done'));

DROP INDEX IF EXISTS idx_app_todos_snoozed_until;
ALTER TABLE app_todos DROP COLUMN IF EXISTS snoozed_until;

-- +goose StatementEnd
