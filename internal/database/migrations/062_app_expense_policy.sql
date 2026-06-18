-- +goose Up

-- Tenant-wide expense approval policy. Small orgs configure one default
-- approver for the whole workspace (a role like "board"/"managers", or a
-- specific person) instead of choosing per report. Per-role routing can layer
-- on later; this is intentionally one row per tenant.
CREATE TABLE app_expense_policy (
    tenant_id        UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    -- Role whose members approve (by name). Anyone in it except the submitter,
    -- plus admins, can approve.
    approver_role    TEXT,
    -- A specific approver. Takes precedence over approver_role when set.
    approver_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Snapshot of the policy's role on the report at submit time, so approval
-- authorization is self-contained and later policy edits don't retroactively
-- change in-flight reports. (A specific-user approver reuses the existing
-- approver_user_id column.)
ALTER TABLE app_expense_reports ADD COLUMN approver_role TEXT;

-- +goose Down
ALTER TABLE app_expense_reports DROP COLUMN IF EXISTS approver_role;
DROP TABLE IF EXISTS app_expense_policy;
