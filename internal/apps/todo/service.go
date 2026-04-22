package todo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// TodoService handles todo operations with authorization.
type TodoService struct {
	pool *pgxpool.Pool
}

// ErrInvalidRole is returned when a todo is created/updated with a role name
// that does not exist in the tenant's roles table.
var ErrInvalidRole = errors.New("role does not exist")

// ClearRoleScope is the sentinel string callers pass to update_todo to clear
// the role_scope back to the default (caller user-scope). Kept for tool
// backwards-compat.
const ClearRoleScope = "none"

// NewService returns a TodoService bound to pool. Exported so builder-app
// bridges (and other external wiring) can construct a service without going
// through the app init path.
func NewService(pool *pgxpool.Pool) *TodoService {
	return &TodoService{pool: pool}
}

// CreateInput is the high-level shape callers (agent, MCP) supply for a new
// todo. Exactly one of AssignedTo / RoleName may be set; both omitted means
// "scope to caller". Visibility defaults to "scoped".
type CreateInput struct {
	Title         string
	Description   string
	Status        string
	Priority      string
	BlockedReason string
	DueDate       *time.Time

	AssignedTo *uuid.UUID // user UUID (already resolved)
	RoleName   string     // role name (translated to role_id at create time)
	Visibility string     // "scoped" (default) or "public"
}

// UpdateInput mirrors CreateInput but is sparse — set only the fields you
// want to change. ScopeChange/Visibility are mutually independent of each
// other but each is optional.
type UpdateInput struct {
	Title         *string
	Description   *string
	Status        *string
	Priority      *string
	BlockedReason *string
	DueDate       *time.Time
	ClearDueDate  bool
	SnoozedUntil  *time.Time
	ClearSnooze   bool

	NewAssignee *uuid.UUID // re-scope to this user
	NewRoleName *string    // re-scope to this role; "" or ClearRoleScope to fall back to caller
	Visibility  *string    // "scoped" or "public"
}

// ResolveAssignee turns a flexible user reference (UUID, Slack user ID, or
// display-name fragment) into a kit user UUID.
func (s *TodoService) ResolveAssignee(ctx context.Context, c *services.Caller, ref string) (*uuid.UUID, string) {
	u, err := models.ResolveUserRef(ctx, s.pool, c.TenantID, ref)
	if err != nil {
		return nil, services.FormatUserRefError(ref, err)
	}
	if u == nil {
		return nil, services.FormatUserRefError(ref, services.ErrNotFound)
	}
	return &u.ID, ""
}

// Create creates a new todo. Non-admins can only assign-to-self or scope to
// roles they hold; the resolved scope is stored as scope_id on the row.
func (s *TodoService) Create(ctx context.Context, c *services.Caller, in CreateInput) (*Todo, error) {
	scopeID, err := s.resolveScope(ctx, c, in.AssignedTo, in.RoleName)
	if err != nil {
		return nil, err
	}

	visibility := in.Visibility
	if visibility == "" {
		visibility = "scoped"
	}

	t := &Todo{
		TenantID:      c.TenantID,
		Title:         in.Title,
		Description:   in.Description,
		Status:        in.Status,
		Priority:      in.Priority,
		BlockedReason: in.BlockedReason,
		ScopeID:       scopeID,
		Visibility:    visibility,
		DueDate:       in.DueDate,
	}
	if t.Status == "" {
		t.Status = "open"
	}
	if t.Priority == "" {
		t.Priority = "medium"
	}

	if err := createTodo(ctx, s.pool, t); err != nil {
		return nil, fmt.Errorf("creating todo: %w", err)
	}

	_ = appendEvent(ctx, s.pool, c.TenantID, t.ID, &c.UserID, "comment", "Created todo", "", "")
	return t, nil
}

// resolveScope translates the (assignee, roleName) pair into a single
// scope_id, honoring non-admin restrictions and creating the canonical scope
// row if necessary. Defaults to caller's user-scope.
func (s *TodoService) resolveScope(ctx context.Context, c *services.Caller, assignedTo *uuid.UUID, roleName string) (uuid.UUID, error) {
	if assignedTo != nil && roleName != "" && roleName != ClearRoleScope {
		return uuid.Nil, errors.New("specify either assigned_to or role_scope, not both")
	}

	if assignedTo != nil {
		if !c.IsAdmin && *assignedTo != c.UserID {
			return uuid.Nil, services.ErrForbidden
		}
		return models.GetOrCreateScope(ctx, s.pool, c.TenantID, nil, assignedTo)
	}

	if roleName != "" && roleName != ClearRoleScope {
		if !c.IsAdmin && !callerHasRole(c, roleName) {
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

	// Default: caller's own user-scope.
	return models.GetOrCreateScope(ctx, s.pool, c.TenantID, nil, &c.UserID)
}

func callerHasRole(c *services.Caller, name string) bool {
	return slices.Contains(c.Roles, name)
}

// List returns todos visible to the caller, with optional filters.
func (s *TodoService) List(ctx context.Context, c *services.Caller, f TodoFilters) ([]Todo, error) {
	if c.IsAdmin {
		return listTodos(ctx, s.pool, c.TenantID, nil, nil, f)
	}
	return listTodos(ctx, s.pool, c.TenantID, &c.UserID, c.RoleIDs, f)
}

// Get returns a single todo if the caller can read it.
func (s *TodoService) Get(ctx context.Context, c *services.Caller, todoID uuid.UUID) (*Todo, []TodoEvent, error) {
	t, err := getTodo(ctx, s.pool, c.TenantID, todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, services.ErrNotFound
		}
		return nil, nil, err
	}
	if !s.canRead(ctx, c, t) {
		return nil, nil, services.ErrNotFound // don't leak existence
	}
	events, err := getRecentEvents(ctx, s.pool, c.TenantID, todoID, 10)
	if err != nil {
		return nil, nil, err
	}
	return t, events, nil
}

// Update updates a todo. Checks both read and write access.
func (s *TodoService) Update(ctx context.Context, c *services.Caller, todoID uuid.UUID, u UpdateInput) (*Todo, error) {
	t, err := getTodo(ctx, s.pool, c.TenantID, todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, services.ErrNotFound
		}
		return nil, err
	}
	if !s.canRead(ctx, c, t) {
		return nil, services.ErrNotFound
	}
	if !s.canWrite(ctx, c, t) {
		return nil, services.ErrForbidden
	}

	// Translate any re-scope request into a new scope_id (with non-admin
	// guards in resolveScope).
	var newScope *uuid.UUID
	if u.NewAssignee != nil || u.NewRoleName != nil {
		var roleName string
		if u.NewRoleName != nil {
			roleName = *u.NewRoleName
		}
		sid, err := s.resolveScope(ctx, c, u.NewAssignee, roleName)
		if err != nil {
			return nil, err
		}
		newScope = &sid
	}

	if u.Status != nil && *u.Status == "blocked" {
		if u.BlockedReason == nil || *u.BlockedReason == "" {
			return nil, errors.New("blocked_reason is required when setting status to blocked")
		}
	}

	dbUpdates := TodoUpdates{
		Title:         u.Title,
		Description:   u.Description,
		Status:        u.Status,
		Priority:      u.Priority,
		BlockedReason: u.BlockedReason,
		ScopeID:       newScope,
		Visibility:    u.Visibility,
		DueDate:       u.DueDate,
		ClearDueDate:  u.ClearDueDate,
		SnoozedUntil:  u.SnoozedUntil,
		ClearSnooze:   u.ClearSnooze,
	}

	if u.Status != nil && *u.Status != t.Status {
		content := ""
		if u.BlockedReason != nil {
			content = *u.BlockedReason
		}
		_ = appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "status_change", content, t.Status, *u.Status)
	}
	if newScope != nil && *newScope != t.ScopeID {
		_ = appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "assignment", "", t.ScopeID.String(), newScope.String())
	}
	if u.Priority != nil && *u.Priority != t.Priority {
		_ = appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "priority_change", "", t.Priority, *u.Priority)
	}

	if err := updateTodo(ctx, s.pool, c.TenantID, todoID, dbUpdates); err != nil {
		return nil, err
	}

	return getTodo(ctx, s.pool, c.TenantID, todoID)
}

// Complete is a shortcut to mark a todo as done.
func (s *TodoService) Complete(ctx context.Context, c *services.Caller, todoID uuid.UUID) (*Todo, error) {
	done := "done"
	return s.Update(ctx, c, todoID, UpdateInput{Status: &done})
}

// Cancel is a shortcut to soft-delete a todo via the 'cancelled' status. The
// row stays in the DB so an admin can recover it with an UPDATE; it just
// drops off the swipe feed.
func (s *TodoService) Cancel(ctx context.Context, c *services.Caller, todoID uuid.UUID) (*Todo, error) {
	cancelled := "cancelled"
	return s.Update(ctx, c, todoID, UpdateInput{Status: &cancelled})
}

// SnoozeDaysToUntil validates a snooze duration (must be 1, 3, or 7) and
// returns the absolute snoozed_until timestamp. Shared by the card action
// handler, agent tool, and MCP tool so the allowed values and time
// calculation live in one place.
func SnoozeDaysToUntil(days int) (time.Time, error) {
	if days != 1 && days != 3 && days != 7 {
		return time.Time{}, fmt.Errorf("snooze days must be 1, 3, or 7 (got %d)", days)
	}
	return time.Now().Add(time.Duration(days) * 24 * time.Hour), nil
}

// Snooze hides the todo from the caller's swipe feed until `until`. Re-snooze
// overwrites the timestamp. A comment event records the action for the audit
// log — we reuse the existing comment type rather than a new event_type so
// the CHECK constraint stays narrow.
func (s *TodoService) Snooze(ctx context.Context, c *services.Caller, todoID uuid.UUID, until time.Time) (*Todo, error) {
	t, err := getTodo(ctx, s.pool, c.TenantID, todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, services.ErrNotFound
		}
		return nil, err
	}
	if !s.canRead(ctx, c, t) {
		return nil, services.ErrNotFound
	}
	if !s.canWrite(ctx, c, t) {
		return nil, services.ErrForbidden
	}
	if err := updateTodo(ctx, s.pool, c.TenantID, todoID, TodoUpdates{SnoozedUntil: &until}); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "comment",
		"Snoozed until "+until.Format(time.RFC3339), "", "")
	return getTodo(ctx, s.pool, c.TenantID, todoID)
}

// AddComment appends a comment to the activity log.
func (s *TodoService) AddComment(ctx context.Context, c *services.Caller, todoID uuid.UUID, content string) error {
	t, err := getTodo(ctx, s.pool, c.TenantID, todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return services.ErrNotFound
		}
		return err
	}
	if !s.canRead(ctx, c, t) {
		return services.ErrNotFound
	}
	return appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "comment", content, "", "")
}

// canRead: public todos visible to all; otherwise the caller must be in
// the todo's scope (assignee or role member). Admin always wins.
func (s *TodoService) canRead(ctx context.Context, c *services.Caller, t *Todo) bool {
	if c.IsAdmin {
		return true
	}
	if t.Visibility == "public" {
		return true
	}
	scope, err := getScopeRow(ctx, s.pool, c.TenantID, t.ScopeID)
	if err != nil {
		return false
	}
	return c.CanSee([]services.ScopeRef{scope})
}

// canWrite: must be in the scope (assignee or role member). Admin wins.
// Note: public todos still require scope membership to write.
func (s *TodoService) canWrite(ctx context.Context, c *services.Caller, t *Todo) bool {
	if c.IsAdmin {
		return true
	}
	scope, err := getScopeRow(ctx, s.pool, c.TenantID, t.ScopeID)
	if err != nil {
		return false
	}
	return c.CanSee([]services.ScopeRef{scope})
}

// FormatTodo formats a todo for display.
func FormatTodo(t *Todo) string {
	var status string
	switch t.Status {
	case "open":
		status = "Open"
	case "in_progress":
		status = "In Progress"
	case "blocked":
		status = "Blocked"
	case "done":
		status = "Done"
	case "cancelled":
		status = "Cancelled"
	default:
		status = t.Status
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s — %s (priority: %s)", t.ID, t.Title, status, t.Priority)

	if t.Visibility == "public" {
		b.WriteString(" [public]")
	}
	if t.DueDate != nil {
		fmt.Fprintf(&b, " [due: %s]", t.DueDate.Format("2006-01-02"))
	}
	if t.BlockedReason != "" {
		b.WriteString("\n  Blocked: ")
		b.WriteString(t.BlockedReason)
	}
	if t.Description != "" {
		desc := t.Description
		if len(desc) > 100 {
			desc = desc[:100] + "..."
		}
		b.WriteString("\n  ")
		b.WriteString(desc)
	}
	return b.String()
}

// FormatTodoDetailed formats a todo with its events for display.
func FormatTodoDetailed(t *Todo, events []TodoEvent) string {
	var b strings.Builder
	b.WriteString(FormatTodo(t))
	if t.ClosedAt != nil {
		b.WriteString("\n  Closed: ")
		b.WriteString(t.ClosedAt.Format(time.RFC3339))
	}
	if len(events) > 0 {
		b.WriteString("\n\nRecent activity:")
		for _, e := range events {
			ts := e.CreatedAt.Format("2006-01-02 15:04")
			switch e.EventType {
			case "comment":
				fmt.Fprintf(&b, "\n  [%s] %s", ts, e.Content)
			case "status_change":
				fmt.Fprintf(&b, "\n  [%s] Status: %s → %s", ts, e.OldValue, e.NewValue)
				if e.Content != "" {
					fmt.Fprintf(&b, " (%s)", e.Content)
				}
			case "assignment":
				fmt.Fprintf(&b, "\n  [%s] Re-scoped: %s → %s", ts, e.OldValue, e.NewValue)
			case "priority_change":
				fmt.Fprintf(&b, "\n  [%s] Priority: %s → %s", ts, e.OldValue, e.NewValue)
			}
		}
	}
	return b.String()
}
