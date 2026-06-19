package expense

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// ExpenseService handles expense-report operations with authorization and the
// draft→submitted→{approved,rejected}→reimbursed state machine.
type ExpenseService struct {
	pool *pgxpool.Pool
	app  *ExpenseApp
}

var (
	// ErrInvalidRole is returned when a report is created with a role name
	// that does not exist in the tenant.
	ErrInvalidRole = errors.New("role does not exist")
	// ErrPrimaryRoleNotSet is returned when no owning role can be inferred.
	ErrPrimaryRoleNotSet = errors.New("primary role not set")
	// ErrInvalidTransition is returned when a status change isn't allowed by
	// the state machine (e.g. approving a draft, editing a submitted report).
	ErrInvalidTransition = errors.New("invalid status transition")
	// ErrNoItems is returned when a report is submitted with no line items.
	ErrNoItems = errors.New("report has no line items")
	// ErrNotEditable is returned when items are mutated on a non-draft report.
	ErrNotEditable = errors.New("report is not editable in its current status")
	// ErrSelfApproval blocks the submitter from approving their own report.
	ErrSelfApproval = errors.New("you cannot approve your own report")
	// ErrNotDeletable is returned when deleting a report that's in the approval
	// pipeline or already a financial record (only draft/rejected can go).
	ErrNotDeletable = errors.New("only draft or rejected reports can be deleted")
)

// NewService returns an ExpenseService bound to pool. Exported for external
// wiring (and tests) that don't go through the app init path.
func NewService(pool *pgxpool.Pool) *ExpenseService {
	return &ExpenseService{pool: pool}
}

// CreateInput is the shape callers supply for a new report.
type CreateInput struct {
	Title       string
	Description string
	Currency    string
	RoleName    string // optional; falls back to caller's primary role
	ApproverRef string // optional; user ref (UUID, Slack ID, or name) to approve
}

// Create creates a draft report owned by the caller's (or named) role.
func (s *ExpenseService) Create(ctx context.Context, c *services.Caller, in CreateInput) (*Report, error) {
	scopeID, err := s.resolveRole(ctx, c, in.RoleName)
	if err != nil {
		return nil, err
	}
	var approverID *uuid.UUID
	if strings.TrimSpace(in.ApproverRef) != "" {
		approverID, err = s.resolveUser(ctx, c, in.ApproverRef)
		if err != nil {
			return nil, err
		}
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	r := &Report{
		TenantID:        c.TenantID,
		Title:           in.Title,
		Description:     in.Description,
		Status:          StatusDraft,
		ScopeID:         scopeID,
		SubmitterUserID: c.UserID,
		ApproverUserID:  approverID,
		Currency:        currency,
	}
	if err := createReport(ctx, s.pool, r); err != nil {
		return nil, fmt.Errorf("creating report: %w", err)
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, r.ID, &c.UserID, "comment", "Created report", "", "")
	return r, nil
}

// ErrInvalidApprover is returned when an approver user reference can't be
// resolved to a real user in the tenant.
var ErrInvalidApprover = errors.New("approver not found")

// resolveUser turns a flexible user reference (UUID, Slack user ID, or
// display-name fragment) into a kit user UUID.
func (s *ExpenseService) resolveUser(ctx context.Context, c *services.Caller, ref string) (*uuid.UUID, error) {
	u, err := models.ResolveUserRef(ctx, s.pool, c.TenantID, ref)
	if err != nil || u == nil {
		return nil, fmt.Errorf("%q: %w", ref, ErrInvalidApprover)
	}
	return &u.ID, nil
}

// AssignApprover sets (or with an empty ref, clears) the designated approver on
// a draft report. Only the submitter or an admin may change it.
func (s *ExpenseService) AssignApprover(ctx context.Context, c *services.Caller, reportID uuid.UUID, ref string) (*Report, error) {
	if _, err := s.editableReport(ctx, c, reportID); err != nil {
		return nil, err
	}
	var u reportUpdate
	if strings.TrimSpace(ref) == "" {
		u.ClearApprover = true
	} else {
		id, err := s.resolveUser(ctx, c, ref)
		if err != nil {
			return nil, err
		}
		u.ApproverUserID = id
	}
	if err := updateReport(ctx, s.pool, c.TenantID, reportID, u); err != nil {
		return nil, err
	}
	return getReport(ctx, s.pool, c.TenantID, reportID)
}

// resolveRole turns an optional role name into a role-scope id, falling back
// through user primary role then the caller's only role. Mirrors task's
// resolver so ownership semantics stay identical across apps.
func (s *ExpenseService) resolveRole(ctx context.Context, c *services.Caller, roleName string) (uuid.UUID, error) {
	roleName = strings.TrimSpace(roleName)
	if roleName != "" && roleName != "none" {
		if !c.IsAdmin && !slices.Contains(c.Roles, roleName) {
			return uuid.Nil, services.ErrForbidden
		}
		roleID, err := services.ResolveRoleID(ctx, s.pool, c.TenantID, roleName)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return uuid.Nil, fmt.Errorf("%q: %w", roleName, ErrInvalidRole)
			}
			return uuid.Nil, err
		}
		return models.GetOrCreateScope(ctx, s.pool, c.TenantID, &roleID, nil)
	}
	primaryID, err := models.GetUserPrimaryRoleID(ctx, s.pool, c.TenantID, c.UserID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("looking up primary role: %w", err)
	}
	if primaryID != nil {
		return models.GetOrCreateScope(ctx, s.pool, c.TenantID, primaryID, nil)
	}
	if len(c.RoleIDs) == 1 {
		return models.GetOrCreateScope(ctx, s.pool, c.TenantID, &c.RoleIDs[0], nil)
	}
	// Multiple roles, no primary: default to the member catchall (everyone
	// holds it) rather than blocking the submitter. Small-org expenses being
	// team-visible is fine; an admin can set the user's primary role to narrow
	// ownership later.
	memberID, err := services.ResolveRoleID(ctx, s.pool, c.TenantID, models.RoleMember)
	if err != nil {
		return uuid.Nil, ErrPrimaryRoleNotSet
	}
	return models.GetOrCreateScope(ctx, s.pool, c.TenantID, &memberID, nil)
}

// GetPolicy returns the tenant's expense approval policy (readable by anyone).
func (s *ExpenseService) GetPolicy(ctx context.Context, c *services.Caller) (Policy, error) {
	return loadPolicy(ctx, s.pool, c.TenantID)
}

// SetPolicyInput is the admin-supplied shape for configuring approval routing.
// At most one of ApproverRef / ApproverRole should be set; ApproverRef wins.
type SetPolicyInput struct {
	ApproverRole string // role name whose members approve
	ApproverRef  string // a specific user (UUID, Slack ID, or name)
}

// SetPolicy configures the tenant-wide approval policy. Admin-only.
func (s *ExpenseService) SetPolicy(ctx context.Context, c *services.Caller, in SetPolicyInput) (Policy, error) {
	if !c.IsAdmin {
		return Policy{}, services.ErrForbidden
	}
	var p Policy
	if strings.TrimSpace(in.ApproverRef) != "" {
		id, err := s.resolveUser(ctx, c, in.ApproverRef)
		if err != nil {
			return Policy{}, err
		}
		p.ApproverUserID = id
	} else if role := strings.TrimSpace(in.ApproverRole); role != "" {
		if _, err := services.ResolveRoleID(ctx, s.pool, c.TenantID, role); err != nil {
			return Policy{}, fmt.Errorf("%q: %w", role, ErrInvalidRole)
		}
		p.ApproverRole = role
	}
	if err := savePolicy(ctx, s.pool, c.TenantID, p); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// List returns reports visible to the caller.
func (s *ExpenseService) List(ctx context.Context, c *services.Caller, f ReportFilters) ([]Report, error) {
	if c.IsAdmin {
		return listReports(ctx, s.pool, c.TenantID, nil, nil, f)
	}
	return listReports(ctx, s.pool, c.TenantID, &c.UserID, c.Roles, f)
}

// Get returns a report with its items and recent activity, if readable.
func (s *ExpenseService) Get(ctx context.Context, c *services.Caller, reportID uuid.UUID) (*Report, []ReportEvent, error) {
	r, err := s.load(ctx, c, reportID)
	if err != nil {
		return nil, nil, err
	}
	items, err := listItems(ctx, s.pool, c.TenantID, reportID)
	if err != nil {
		return nil, nil, err
	}
	r.Items = items
	events, err := getRecentEvents(ctx, s.pool, c.TenantID, reportID, 15)
	if err != nil {
		return nil, nil, err
	}
	return r, events, nil
}

// load fetches a report and enforces read access, mapping missing/forbidden to
// ErrNotFound so callers don't leak existence.
func (s *ExpenseService) load(ctx context.Context, c *services.Caller, reportID uuid.UUID) (*Report, error) {
	r, err := getReport(ctx, s.pool, c.TenantID, reportID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, services.ErrNotFound
		}
		return nil, err
	}
	if !s.canRead(ctx, c, r) {
		return nil, services.ErrNotFound
	}
	return r, nil
}

// canRead: the submitter and admins always; otherwise only the report's
// approver (the assigned person, or a member of its snapshot approver role).
// Owning-role membership no longer grants visibility — expenses are personal,
// seen by their owner and whoever approves them.
func (s *ExpenseService) canRead(_ context.Context, c *services.Caller, r *Report) bool {
	if c.IsAdmin || r.SubmitterUserID == c.UserID {
		return true
	}
	if r.ApproverUserID != nil && *r.ApproverUserID == c.UserID {
		return true
	}
	return r.ApproverRole != "" && slices.Contains(c.Roles, r.ApproverRole)
}

// CanApprove reports whether the caller may act on this report right now (it's
// submitted and they're an eligible approver). Drives the approve/reject UI so
// non-approvers never see those controls.
func (s *ExpenseService) CanApprove(ctx context.Context, c *services.Caller, r *Report) bool {
	return r.Status == StatusSubmitted && s.canApprove(ctx, c, r)
}

// Delete removes a draft or rejected report (and its items) — for cleaning up
// mistakes and abandoned reports. Submitted/approved/reimbursed reports can't
// be deleted (reject or reopen them first); they're in-flight or financial
// records. Only the submitter or an admin may delete.
func (s *ExpenseService) Delete(ctx context.Context, c *services.Caller, reportID uuid.UUID) error {
	r, err := s.load(ctx, c, reportID)
	if err != nil {
		return err
	}
	if !s.canWrite(c, r) {
		return services.ErrForbidden
	}
	if r.Status != StatusDraft && r.Status != StatusRejected {
		return ErrNotDeletable
	}
	return deleteReport(ctx, s.pool, c.TenantID, reportID)
}

// AddComment appends a comment to a readable report's activity log.
func (s *ExpenseService) AddComment(ctx context.Context, c *services.Caller, reportID uuid.UUID, content string) error {
	if _, err := s.load(ctx, c, reportID); err != nil {
		return err
	}
	return appendEvent(ctx, s.pool, c.TenantID, reportID, &c.UserID, "comment", content, "", "")
}
