-- +goose Up

-- Split "when did the scan last run" from "how far has it read".
--
-- last_scanned_at is a watermark over *email dates* — the newest message the
-- agent has already been shown. Due-ness was computed from it, which conflates
-- the two: a mailbox that receives nothing new leaves the watermark parked in
-- the past, so the row reads as overdue on every sweep tick and re-runs every
-- 15 minutes, posting a briefing card each time. (IMAP SEARCH SINCE is
-- date-granular, so the same messages come back forever and the watermark
-- never moves past them.)
--
-- last_run_at answers only "when did a scan last complete", and is what the
-- cron schedule is evaluated against.
ALTER TABLE app_task_email_intake ADD COLUMN last_run_at TIMESTAMPTZ;

-- Backfill from updated_at: every existing row is touched by its own sweep, so
-- this is the last run for any row that has ever run, and it keeps the fleet
-- from all coming due at once on the deploy that adds this column.
UPDATE app_task_email_intake
SET last_run_at = updated_at
WHERE last_scanned_at IS NOT NULL;

-- +goose Down
ALTER TABLE app_task_email_intake DROP COLUMN last_run_at;
