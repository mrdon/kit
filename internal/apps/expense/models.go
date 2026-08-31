package expense

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Report statuses. The DB CHECK constraint (migration 061) is the source of
// truth; these constants keep the Go-side state machine from drifting into
// raw string literals.
const (
	StatusDraft      = "draft"
	StatusSubmitted  = "submitted"
	StatusApproved   = "approved"
	StatusRejected   = "rejected"
	StatusReimbursed = "reimbursed"
)

// Report is an expense report — a titled group of line items routed for
// approval.
type Report struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	ScopeID     uuid.UUID `json:"scope_id"`
	// SubmitterUserID is uuid.Nil for an anonymous public-intake report (one
	// submitted via /{slug}/expenses/submit with no Kit account); SubmitterEmail
	// / SubmitterName carry the payee identity in that case.
	SubmitterUserID uuid.UUID  `json:"submitter_user_id"`
	SubmitterEmail  string     `json:"submitter_email,omitempty"`
	SubmitterName   string     `json:"submitter_name,omitempty"`
	ApproverUserID  *uuid.UUID `json:"approver_user_id,omitempty"`   // assigned/policy approver (specific person)
	ApproverRole    string     `json:"approver_role,omitempty"`      // policy approver role (snapshot at submit)
	DecidedByUserID *uuid.UUID `json:"decided_by_user_id,omitempty"` // who approved/rejected
	DecisionCardID  *uuid.UUID `json:"decision_card_id,omitempty"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	TotalCents      int64      `json:"total_cents"`
	Currency        string     `json:"currency"`
	SubmittedAt     *time.Time `json:"submitted_at,omitempty"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
	ReimbursedAt    *time.Time `json:"reimbursed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Items           []Item     `json:"items,omitempty"`
}

// Item is a single line on a report, optionally backed by a receipt
// attachment.
type Item struct {
	ID           uuid.UUID  `json:"id"`
	TenantID     uuid.UUID  `json:"tenant_id"`
	ReportID     uuid.UUID  `json:"report_id"`
	AttachmentID *uuid.UUID `json:"attachment_id,omitempty"`
	Vendor       string     `json:"vendor,omitempty"`
	SpentOn      *time.Time `json:"spent_on,omitempty"`
	AmountCents  int64      `json:"amount_cents"`
	TaxCents     int64      `json:"tax_cents"`
	Category     string     `json:"category,omitempty"`
	Note         string     `json:"note,omitempty"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ReportEvent is an entry in a report's activity log.
type ReportEvent struct {
	ID        uuid.UUID  `json:"id"`
	TenantID  uuid.UUID  `json:"tenant_id"`
	ReportID  uuid.UUID  `json:"report_id"`
	AuthorID  *uuid.UUID `json:"author_id,omitempty"`
	EventType string     `json:"event_type"`
	Content   string     `json:"content,omitempty"`
	OldValue  string     `json:"old_value,omitempty"`
	NewValue  string     `json:"new_value,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ReportFilters holds optional filters for listing reports.
type ReportFilters struct {
	Status        string
	MineOnly      bool // only reports I submitted
	Search        string
	IncludeClosed bool // admit approved/rejected/reimbursed when Status is empty
}

const reportColumns = `r.id, r.tenant_id, r.title, r.description, r.status, r.scope_id, r.submitter_user_id, r.submitter_email, r.submitter_name, r.approver_user_id, r.approver_role, r.decided_by_user_id, r.decision_card_id, r.rejection_reason, r.total_cents, r.currency, r.submitted_at, r.decided_at, r.reimbursed_at, r.created_at, r.updated_at`

func scanReport(row interface{ Scan(...any) error }) (*Report, error) {
	var r Report
	var description, rejection, approverRole, submitterEmail, submitterName *string
	var submitterID *uuid.UUID // NULL for anonymous public-intake reports
	err := row.Scan(
		&r.ID, &r.TenantID, &r.Title, &description, &r.Status,
		&r.ScopeID, &submitterID, &submitterEmail, &submitterName, &r.ApproverUserID, &approverRole, &r.DecidedByUserID, &r.DecisionCardID,
		&rejection, &r.TotalCents, &r.Currency,
		&r.SubmittedAt, &r.DecidedAt, &r.ReimbursedAt,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if submitterID != nil {
		r.SubmitterUserID = *submitterID
	}
	if submitterEmail != nil {
		r.SubmitterEmail = *submitterEmail
	}
	if submitterName != nil {
		r.SubmitterName = *submitterName
	}
	if description != nil {
		r.Description = *description
	}
	if rejection != nil {
		r.RejectionReason = *rejection
	}
	if approverRole != nil {
		r.ApproverRole = *approverRole
	}
	return &r, nil
}

func createReport(ctx context.Context, pool *pgxpool.Pool, r *Report) error {
	return pool.QueryRow(ctx, `
		INSERT INTO app_expense_reports (tenant_id, title, description, status, scope_id, submitter_user_id, submitter_email, submitter_name, approver_user_id, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at, updated_at`,
		r.TenantID, r.Title, nilIfEmpty(r.Description), r.Status, r.ScopeID,
		nullUUID(r.SubmitterUserID), nilIfEmpty(r.SubmitterEmail), nilIfEmpty(r.SubmitterName),
		r.ApproverUserID, r.Currency,
	).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
}

func getReport(ctx context.Context, pool *pgxpool.Pool, tenantID, reportID uuid.UUID) (*Report, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+reportColumns+` FROM app_expense_reports r WHERE r.tenant_id = $1 AND r.id = $2`,
		tenantID, reportID,
	)
	return scanReport(row)
}

// listReports returns reports visible to the caller. Visibility follows
// approval authority, not owning-role membership: you always see your own
// reports; you see someone else's only if you're its approver (the assigned
// person, or a member of its snapshot approver_role). Admin (userID == nil)
// sees everything.
func listReports(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, userID *uuid.UUID, roleNames []string, f ReportFilters) ([]Report, error) {
	var b strings.Builder
	args := []any{tenantID}
	argN := 1

	b.WriteString(`SELECT ` + reportColumns + ` FROM app_expense_reports r`)
	b.WriteString(` WHERE r.tenant_id = $1`)

	if userID != nil {
		argN++
		meArg := argN
		args = append(args, *userID)
		argN++
		rolesArg := argN
		args = append(args, roleNames)
		// Own submission, OR I'm the assigned approver, OR I'm in the report's
		// snapshot approver role. Others' drafts have no approver snapshot, so
		// they stay invisible.
		fmt.Fprintf(&b, ` AND (r.submitter_user_id = $%d OR r.approver_user_id = $%d OR (r.approver_role IS NOT NULL AND r.approver_role = ANY($%d)))`,
			meArg, meArg, rolesArg)
	}

	if f.MineOnly && userID != nil {
		argN++
		fmt.Fprintf(&b, ` AND r.submitter_user_id = $%d`, argN)
		args = append(args, *userID)
	}

	if f.Status != "" {
		argN++
		fmt.Fprintf(&b, ` AND r.status = $%d`, argN)
		args = append(args, f.Status)
	} else if !f.IncludeClosed {
		b.WriteString(` AND r.status IN ('draft','submitted')`)
	}

	if f.Search != "" {
		argN++
		fmt.Fprintf(&b, ` AND to_tsvector('english', coalesce(r.title,'') || ' ' || coalesce(r.description,'')) @@ plainto_tsquery('english', $%d)`, argN)
		args = append(args, f.Search)
	}

	b.WriteString(` ORDER BY CASE r.status WHEN 'submitted' THEN 0 WHEN 'draft' THEN 1 ELSE 2 END, r.created_at DESC LIMIT 50`)

	rows, err := pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("listing reports: %w", err)
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning report: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// reportUpdate carries the state-transition fields applied by the service
// when a report changes status. Only set the fields the transition touches.
type reportUpdate struct {
	Status           *string
	Title            *string
	Description      *string
	ApproverUserID   *uuid.UUID // assigned approver
	ClearApprover    bool
	ApproverRole     *string    // policy approver role snapshot
	DecidedByUserID  *uuid.UUID // who approved/rejected
	DecisionCardID   *uuid.UUID
	RejectionReason  *string
	SetSubmittedNow  bool
	SetDecidedNow    bool
	SetReimbursedNow bool
}

func updateReport(ctx context.Context, pool *pgxpool.Pool, tenantID, reportID uuid.UUID, u reportUpdate) error {
	var sets []string
	args := []any{tenantID, reportID}
	argN := 2

	add := func(col string, v any) {
		argN++
		sets = append(sets, fmt.Sprintf("%s = $%d", col, argN))
		args = append(args, v)
	}
	if u.Status != nil {
		add("status", *u.Status)
	}
	if u.Title != nil {
		add("title", *u.Title)
	}
	if u.Description != nil {
		add("description", nilIfEmpty(*u.Description))
	}
	if u.ApproverUserID != nil {
		add("approver_user_id", *u.ApproverUserID)
	} else if u.ClearApprover {
		sets = append(sets, "approver_user_id = NULL")
	}
	if u.ApproverRole != nil {
		add("approver_role", nilIfEmpty(*u.ApproverRole))
	}
	if u.DecidedByUserID != nil {
		add("decided_by_user_id", *u.DecidedByUserID)
	}
	if u.DecisionCardID != nil {
		add("decision_card_id", *u.DecisionCardID)
	}
	if u.RejectionReason != nil {
		add("rejection_reason", nilIfEmpty(*u.RejectionReason))
	}
	if u.SetSubmittedNow {
		sets = append(sets, "submitted_at = now()")
	}
	if u.SetDecidedNow {
		sets = append(sets, "decided_at = now()")
	}
	if u.SetReimbursedNow {
		sets = append(sets, "reimbursed_at = now()")
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = now()")
	q := fmt.Sprintf(`UPDATE app_expense_reports SET %s WHERE tenant_id = $1 AND id = $2`, strings.Join(sets, ", "))
	if _, err := pool.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("updating report: %w", err)
	}
	return nil
}

// deleteReport removes a report; items and events cascade via FK.
func deleteReport(ctx context.Context, pool *pgxpool.Pool, tenantID, reportID uuid.UUID) error {
	if _, err := pool.Exec(ctx,
		`DELETE FROM app_expense_reports WHERE tenant_id = $1 AND id = $2`, tenantID, reportID); err != nil {
		return fmt.Errorf("deleting report: %w", err)
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullUUID returns nil for the zero UUID so an INSERT writes SQL NULL. Used for
// submitter_user_id on anonymous public-intake reports (no Kit account).
func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
