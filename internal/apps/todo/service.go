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

// ResolveAssignee turns a flexible user reference (UUID, Slack user ID, or
// display-name fragment) into a kit user UUID. Returns a user-facing message
// in the second return when the reference can't be resolved unambiguously;
// callers should surface that string to the agent or MCP client.
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

// Create creates a new todo. Non-admins can only self-assign and scope to their own roles.
func (s *TodoService) Create(ctx context.Context, c *services.Caller, t *Todo) error {
	t.TenantID = c.TenantID
	t.CreatedBy = c.UserID

	if !c.IsAdmin {
		// Non-admins can only assign to themselves
		if t.AssignedTo != nil && *t.AssignedTo != c.UserID {
			return services.ErrForbidden
		}
		// Non-admins can only scope to their own roles
		if t.RoleScope != "" && !slices.Contains(c.Roles, t.RoleScope) {
			return services.ErrForbidden
		}
	}

	if t.Status == "" {
		t.Status = "open"
	}
	if t.Priority == "" {
		t.Priority = "medium"
	}

	if err := createTodo(ctx, s.pool, t); err != nil {
		return fmt.Errorf("creating todo: %w", err)
	}

	_ = appendEvent(ctx, s.pool, c.TenantID, t.ID, &c.UserID, "comment", "Created todo", "", "")
	return nil
}

// List returns todos visible to the caller, with optional filters.
func (s *TodoService) List(ctx context.Context, c *services.Caller, f TodoFilters) ([]Todo, error) {
	if c.IsAdmin {
		return listTodosAll(ctx, s.pool, c.TenantID, f)
	}
	return listTodosScoped(ctx, s.pool, c.TenantID, c.UserID, c.Roles, f)
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
	if !canRead(c, t) {
		return nil, nil, services.ErrNotFound // don't leak existence
	}
	events, err := getRecentEvents(ctx, s.pool, c.TenantID, todoID, 10)
	if err != nil {
		return nil, nil, err
	}
	return t, events, nil
}

// Update updates a todo. Checks both read and write access.
func (s *TodoService) Update(ctx context.Context, c *services.Caller, todoID uuid.UUID, u TodoUpdates) (*Todo, error) {
	t, err := getTodo(ctx, s.pool, c.TenantID, todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, services.ErrNotFound
		}
		return nil, err
	}
	if !canRead(c, t) {
		return nil, services.ErrNotFound
	}
	if !canWrite(c, t) {
		return nil, services.ErrForbidden
	}

	if !c.IsAdmin {
		if err := validateNonAdminUpdate(c, u); err != nil {
			return nil, err
		}
	}

	// Blocked requires reason
	if u.Status != nil && *u.Status == "blocked" {
		if u.BlockedReason == nil || *u.BlockedReason == "" {
			return nil, errors.New("blocked_reason is required when setting status to blocked")
		}
	}

	// Log status change
	if u.Status != nil && *u.Status != t.Status {
		content := ""
		if u.BlockedReason != nil {
			content = *u.BlockedReason
		}
		_ = appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "status_change", content, t.Status, *u.Status)
	}

	// Log assignment change
	if u.AssignedTo != nil && (t.AssignedTo == nil || *u.AssignedTo != *t.AssignedTo) {
		_ = appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "assignment", "", uuidStr(t.AssignedTo), u.AssignedTo.String())
	}

	// Log priority change
	if u.Priority != nil && *u.Priority != t.Priority {
		_ = appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "priority_change", "", t.Priority, *u.Priority)
	}

	if err := updateTodo(ctx, s.pool, c.TenantID, todoID, u); err != nil {
		return nil, err
	}

	return getTodo(ctx, s.pool, c.TenantID, todoID)
}

// Complete is a shortcut to mark a todo as done.
func (s *TodoService) Complete(ctx context.Context, c *services.Caller, todoID uuid.UUID) (*Todo, error) {
	done := "done"
	return s.Update(ctx, c, todoID, TodoUpdates{Status: &done})
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
	if !canRead(c, t) {
		return services.ErrNotFound
	}
	return appendEvent(ctx, s.pool, c.TenantID, todoID, &c.UserID, "comment", content, "", "")
}

// canRead checks if the caller can see this todo.
func canRead(c *services.Caller, t *Todo) bool {
	if c.IsAdmin {
		return true
	}
	if t.Private {
		return c.UserID == t.CreatedBy || (t.AssignedTo != nil && c.UserID == *t.AssignedTo)
	}
	return true // non-private todos are visible to everyone
}

// canWrite checks if the caller can modify this todo.
func canWrite(c *services.Caller, t *Todo) bool {
	if c.IsAdmin {
		return true
	}
	if c.UserID == t.CreatedBy {
		return true
	}
	if t.AssignedTo != nil && c.UserID == *t.AssignedTo {
		return true
	}
	if t.RoleScope != "" && slices.Contains(c.Roles, t.RoleScope) {
		return true
	}
	return false
}

func validateNonAdminUpdate(c *services.Caller, u TodoUpdates) error {
	// Non-admins can't assign to others
	if u.AssignedTo != nil && *u.AssignedTo != c.UserID {
		return services.ErrForbidden
	}
	// Non-admins can only scope to their own roles
	if u.RoleScope != nil && *u.RoleScope != "" && !slices.Contains(c.Roles, *u.RoleScope) {
		return services.ErrForbidden
	}
	return nil
}

func uuidStr(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
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
	default:
		status = t.Status
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s — %s (priority: %s)", t.ID, t.Title, status, t.Priority)

	if t.RoleScope != "" {
		fmt.Fprintf(&b, " [role: %s]", t.RoleScope)
	}
	if t.AssignedTo != nil {
		fmt.Fprintf(&b, " [assigned: %s]", t.AssignedTo)
	}
	if t.DueDate != nil {
		fmt.Fprintf(&b, " [due: %s]", t.DueDate.Format("2006-01-02"))
	}
	if t.Private {
		b.WriteString(" [private]")
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
				fmt.Fprintf(&b, "\n  [%s] Assigned: %s → %s", ts, e.OldValue, e.NewValue)
			case "priority_change":
				fmt.Fprintf(&b, "\n  [%s] Priority: %s → %s", ts, e.OldValue, e.NewValue)
			}
		}
	}
	return b.String()
}
