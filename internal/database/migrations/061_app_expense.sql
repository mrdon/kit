-- +goose Up

-- Expense Reports. A report groups one or more line items (each optionally
-- backed by a receipt stored in the general `attachments` table, migration
-- 060) and routes for approval. The receipt itself is just an attachment
-- row — this app only references it.
--
-- State machine (enforced in service.go, mirrored by the status CHECK):
--   draft → submitted → {approved, rejected}
--   approved → reimbursed
--   rejected → draft        (fix & resubmit)
-- Line items are mutable only while the report is in `draft`.

CREATE TABLE app_expense_reports (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title             TEXT NOT NULL,
    description       TEXT,
    status            TEXT NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft','submitted','approved','rejected','reimbursed')),
    -- Role that owns the report (visibility = role membership, same model as
    -- tasks). The submitter is always a member of this role.
    scope_id          UUID NOT NULL REFERENCES scopes(id),
    submitter_user_id UUID NOT NULL REFERENCES users(id),
    -- The person designated to approve this report. Optional: when set, only
    -- they (or an admin) can approve and the decision card routes to them;
    -- when null, approval falls back to any non-submitter member of the
    -- owning role (or an admin).
    approver_user_id  UUID REFERENCES users(id),
    -- Who actually approved/rejected (distinct from the assigned approver).
    decided_by_user_id UUID REFERENCES users(id),
    -- The decision card raised at submit time. Loose reference (no FK) — the
    -- cards app may purge old cards independently of expense history.
    decision_card_id  UUID,
    rejection_reason  TEXT,
    -- Denormalised sum of the items' amount_cents, recomputed on every item
    -- mutation so list/console views don't have to aggregate.
    total_cents       BIGINT NOT NULL DEFAULT 0,
    currency          TEXT NOT NULL DEFAULT 'USD',
    submitted_at      TIMESTAMPTZ,
    decided_at        TIMESTAMPTZ,
    reimbursed_at     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_expense_reports_scope ON app_expense_reports(tenant_id, scope_id);
CREATE INDEX idx_app_expense_reports_submitter ON app_expense_reports(tenant_id, submitter_user_id, status);
CREATE INDEX idx_app_expense_reports_status ON app_expense_reports(tenant_id, status, created_at DESC);
CREATE INDEX idx_app_expense_reports_search ON app_expense_reports
    USING gin(to_tsvector('english', coalesce(title, '') || ' ' || coalesce(description, '')));

CREATE TABLE app_expense_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    report_id     UUID NOT NULL REFERENCES app_expense_reports(id) ON DELETE CASCADE,
    -- The receipt. SET NULL (not CASCADE) so deleting an attachment doesn't
    -- erase the line item's vendor/amount/date — the money was still spent.
    attachment_id UUID REFERENCES attachments(id) ON DELETE SET NULL,
    vendor        TEXT,
    spent_on      DATE,
    amount_cents  BIGINT NOT NULL DEFAULT 0,
    tax_cents     BIGINT NOT NULL DEFAULT 0,
    category      TEXT,
    note          TEXT,
    sort_order    INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_expense_items_report ON app_expense_items(tenant_id, report_id, sort_order);

CREATE TABLE app_expense_report_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    report_id  UUID NOT NULL REFERENCES app_expense_reports(id) ON DELETE CASCADE,
    author_id  UUID REFERENCES users(id),
    event_type TEXT NOT NULL
               CHECK (event_type IN ('comment','status_change','item_added','item_removed','submitted','approved','rejected','reimbursed')),
    content    TEXT,
    old_value  TEXT,
    new_value  TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_expense_report_events_report ON app_expense_report_events(tenant_id, report_id, created_at);
CREATE INDEX idx_app_expense_report_events_recent ON app_expense_report_events(tenant_id, created_at);

-- +goose Down

DROP TABLE IF EXISTS app_expense_report_events;
DROP TABLE IF EXISTS app_expense_items;
DROP TABLE IF EXISTS app_expense_reports;
