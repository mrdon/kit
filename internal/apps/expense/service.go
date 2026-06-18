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
	return uuid.Nil, ErrPrimaryRoleNotSet
}

// List returns reports visible to the caller.
func (s *ExpenseService) List(ctx context.Context, c *services.Caller, f ReportFilters) ([]Report, error) {
	if c.IsAdmin {
		return listReports(ctx, s.pool, c.TenantID, nil, nil, f)
	}
	return listReports(ctx, s.pool, c.TenantID, &c.UserID, c.RoleIDs, f)
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

// canRead: admin, the submitter, the assigned approver, or a member of the
// owning role. The assigned approver is included so an approver outside the
// role can still open the report they're asked to decide on.
func (s *ExpenseService) canRead(ctx context.Context, c *services.Caller, r *Report) bool {
	if c.IsAdmin || r.SubmitterUserID == c.UserID {
		return true
	}
	if r.ApproverUserID != nil && *r.ApproverUserID == c.UserID {
		return true
	}
	scope, err := getScopeRow(ctx, s.pool, c.TenantID, r.ScopeID)
	if err != nil {
		return false
	}
	return c.CanSee([]services.ScopeRef{scope})
}

// AddComment appends a comment to a readable report's activity log.
func (s *ExpenseService) AddComment(ctx context.Context, c *services.Caller, reportID uuid.UUID, content string) error {
	if _, err := s.load(ctx, c, reportID); err != nil {
		return err
	}
	return appendEvent(ctx, s.pool, c.TenantID, reportID, &c.UserID, "comment", content, "", "")
}
