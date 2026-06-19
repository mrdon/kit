-- +goose Up

-- Public expense intake. A clean, listed URL (/{slug}/expenses/submit) lets
-- people without a Slack account — volunteers, occasional helpers — upload a
-- receipt and submit an expense report straight into the approval workflow.
-- Such a report has no Kit user as submitter, just a captured email/name for
-- the payee, so submitter_user_id becomes nullable.
ALTER TABLE app_expense_reports ALTER COLUMN submitter_user_id DROP NOT NULL;
ALTER TABLE app_expense_reports ADD COLUMN submitter_email TEXT;
ALTER TABLE app_expense_reports ADD COLUMN submitter_name  TEXT;

-- Public-intake config on the tenant policy. Off until an admin enables it and
-- picks the owning/approving role (intake_role, by name, mirroring
-- approver_role). intake_currency is the default for anonymous submissions,
-- which have no workspace currency preference of their own.
ALTER TABLE app_expense_policy ADD COLUMN intake_enabled  BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE app_expense_policy ADD COLUMN intake_role     TEXT;
ALTER TABLE app_expense_policy ADD COLUMN intake_currency TEXT NOT NULL DEFAULT 'USD';

-- +goose Down
ALTER TABLE app_expense_policy DROP COLUMN IF EXISTS intake_currency;
ALTER TABLE app_expense_policy DROP COLUMN IF EXISTS intake_role;
ALTER TABLE app_expense_policy DROP COLUMN IF EXISTS intake_enabled;
ALTER TABLE app_expense_reports DROP COLUMN IF EXISTS submitter_name;
ALTER TABLE app_expense_reports DROP COLUMN IF EXISTS submitter_email;
ALTER TABLE app_expense_reports ALTER COLUMN submitter_user_id SET NOT NULL;
