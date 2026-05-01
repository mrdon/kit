package task

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

// TaskService handles task operations with authorization.
type TaskService struct {
	pool *pgxpool.Pool
	// app is the owning TaskApp; used to reach Configure-wired
	// dependencies (llm) at call time. May be nil in tests that
	// construct a service via NewService(pool).
	app *TaskApp
}

// ErrInvalidRole is returned when a task is created/updated with a role name
// that does not exist in the tenant's roles table.
var ErrInvalidRole = errors.New("role does not exist")

// ErrPrimaryRoleNotSet is returned when the resolver can't pick a default
// role for the caller — they hold multiple roles and no primary is set.
// The agent surfaces this as "set a primary role or pass role_scope".
var ErrPrimaryRoleNotSet = errors.New("primary role not set")

// NewService returns a TaskService bound to pool. Exported so builder-app
// bridges (and other external wiring) can construct a service without going
// through the app init path.
func NewService(pool *pgxpool.Pool) *TaskService {
	return &TaskService{pool: pool}
}

// CreateInput is the high-level shape callers (agent, MCP) supply for a new
// task. RoleName is optional (falls back to caller's primary role); assignee
// is orthogonal — anyone in the role can see and edit regardless.
type CreateInput struct {
	Title         string
	Description   string
	Status        string
	Priority      string
	BlockedReason string
	DueDate       *time.Time

	AssigneeUserID *uuid.UUID // optional; orthogonal to RoleName
	RoleName       string     // optional; resolver fills in caller's primary if empty
}

// UpdateInput mirrors CreateInput but is sparse — set only the fields you
// want to change.
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

	NewAssigneeUserID *uuid.UUID // set/replace assignee
	ClearAssignee     bool       // unset assignee
	NewRoleName       *string    // re-scope to this role; "" or "none" rejected
}

// ResolveAssignee turns a flexible user reference (UUID, Slack user ID, or
// display-name fragment) into a kit user UUID.
func (s *TaskService) ResolveAssignee(ctx context.Context, c *services.Caller, ref string) (*uuid.UUID, string) {
	u, err := models.ResolveUserRef(ctx, s.pool, c.TenantID, ref)
	if err != nil {
		return nil, services.FormatUserRefError(ref, err)
	}
	if u == nil {
		return nil, services.FormatUserRefError(ref, services.ErrNotFound)
	}
	return &u.ID, ""
}

// Create creates a new task. Every task lives in a role; if RoleName is
// empty the resolver falls back to the caller's primary role (or their
// only role if they hold exactly one).
func (s *TaskService) Create(ctx context.Context, c *services.Caller, in CreateInput) (*Task, error) {
	scopeID, err := s.resolveTaskRole(ctx, c, in.RoleName)
	if err != nil {
		return nil, err
	}

	t := &Task{
		TenantID:       c.TenantID,
		Title:          in.Title,
		Description:    in.Description,
		Status:         in.Status,
		Priority:       in.Priority,
		BlockedReason:  in.BlockedReason,
		ScopeID:        scopeID,
		AssigneeUserID: in.AssigneeUserID,
		DueDate:        in.DueDate,
	}
	if t.Status == "" {
		t.Status = "open"
	}
	if t.Priority == "" {
		t.Priority = "medium"
	}

	if err := createTask(ctx, s.pool, t); err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}

	_ = appendEvent(ctx, s.pool, c.TenantID, t.ID, &c.UserID, "comment", "Created task", "", "")

	// Fire the resolution suggester asynchronously. Detached context
	// because the request context may cancel before Haiku finishes; the
	// goroutine has its own semaphore + logging. Caller/task passed by
	// value so a later mutation by the request path can't observe them.
	if s.app != nil && s.app.llm != nil {
		go runResolutionSuggester(s.pool, s.app.llm, *c, *t)
	}

	return t, nil
}

// resolveTaskRole turns an optional role name into a role-scope_id, falling
// back through user.primary_role_id and the caller's only role. Never
// returns a non-role scope. The user-level primary takes precedence over
// any tenant default — a person's tasks shouldn't get sucked into whatever
// the tenant's most-common role is.
func (s *TaskService) resolveTaskRole(ctx context.Context, c *services.Caller, roleName string) (uuid.UUID, error) {
	roleName = strings.TrimSpace(roleName)
	if roleName != "" && roleName != "none" {
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

	// Empty role name: fall back to user primary_role_id.
	primaryID, err := models.GetUserPrimaryRoleID(ctx, s.pool, c.TenantID, c.UserID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("looking up primary role: %w", err)
	}
	if primaryID != nil {
		return models.GetOrCreateScope(ctx, s.pool, c.TenantID, primaryID, nil)
	}

	// No primary set. If caller holds exactly one role, use it.
	if len(c.RoleIDs) == 1 {
		return models.GetOrCreateScope(ctx, s.pool, c.TenantID, &c.RoleIDs[0], nil)
	}

	// Multiple roles + no primary: agent must ask.
	return uuid.Nil, ErrPrimaryRoleNotSet
}

func callerHasRole(c *services.Caller, name string) bool {
	return slices.Contains(c.Roles, name)
}

// List returns tasks visible to the caller, with optional filters.
func (s *TaskService) List(ctx context.Context, c *services.Caller, f TaskFilters) ([]Task, error) {
	if c.IsAdmin {
		return listTasks(ctx, s.pool, c.TenantID, nil, nil, f)
	}
	return listTasks(ctx, s.pool, c.TenantID, &c.UserID, c.RoleIDs, f)
}

// Get returns a single task if the caller can read it.
func (s *TaskService) Get(ctx context.Context, c *services.Caller, taskID uuid.UUID) (*Task, []TaskEvent, error) {
	t, err := getTask(ctx, s.pool, c.TenantID, taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, services.ErrNotFound
		}
		return nil, nil, err
	}
	if !s.canRead(ctx, c, t) {
		return nil, nil, services.ErrNotFound // don't leak existence
	}
	events, err := getRecentEvents(ctx, s.pool, c.TenantID, taskID, 10)
	if err != nil {
		return nil, nil, err
	}
	return t, events, nil
}

// Update updates a task. Checks both read and write access.
func (s *TaskService) Update(ctx context.Context, c *services.Caller, taskID uuid.UUID, u UpdateInput) (*Task, error) {
	t, err := getTask(ctx, s.pool, c.TenantID, taskID)
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

	// Re-scope to a different role if requested. "" / "none" are explicit
	// errors — every task must belong to a role.
	var newScope *uuid.UUID
	if u.NewRoleName != nil {
		name := strings.TrimSpace(*u.NewRoleName)
		if name == "" || name == "none" {
			return nil, errors.New("role_scope cannot be empty — every task must belong to a role")
		}
		sid, err := s.resolveTaskRole(ctx, c, name)
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

	dbUpdates := TaskUpdates{
		Title:          u.Title,
		Description:    u.Description,
		Status:         u.Status,
		Priority:       u.Priority,
		BlockedReason:  u.BlockedReason,
		ScopeID:        newScope,
		AssigneeUserID: u.NewAssigneeUserID,
		ClearAssignee:  u.ClearAssignee,
		DueDate:        u.DueDate,
		ClearDueDate:   u.ClearDueDate,
		SnoozedUntil:   u.SnoozedUntil,
		ClearSnooze:    u.ClearSnooze,
	}

	if u.Status != nil && *u.Status != t.Status {
		content := ""
		if u.BlockedReason != nil {
			content = *u.BlockedReason
		}
		_ = appendEvent(ctx, s.pool, c.TenantID, taskID, &c.UserID, "status_change", content, t.Status, *u.Status)
	}
	if newScope != nil && *newScope != t.ScopeID {
		_ = appendEvent(ctx, s.pool, c.TenantID, taskID, &c.UserID, "assignment", "", t.ScopeID.String(), newScope.String())
	}
	if u.NewAssigneeUserID != nil || u.ClearAssignee {
		oldVal := ""
		newVal := ""
		if t.AssigneeUserID != nil {
			oldVal = t.AssigneeUserID.String()
		}
		if u.NewAssigneeUserID != nil {
			newVal = u.NewAssigneeUserID.String()
		}
		if oldVal != newVal {
			_ = appendEvent(ctx, s.pool, c.TenantID, taskID, &c.UserID, "assignee_change", "", oldVal, newVal)
		}
	}
	if u.Priority != nil && *u.Priority != t.Priority {
		_ = appendEvent(ctx, s.pool, c.TenantID, taskID, &c.UserID, "priority_change", "", t.Priority, *u.Priority)
	}

	if err := updateTask(ctx, s.pool, c.TenantID, taskID, dbUpdates); err != nil {
		return nil, err
	}

	return getTask(ctx, s.pool, c.TenantID, taskID)
}

// Complete is a shortcut to mark a task as done.
func (s *TaskService) Complete(ctx context.Context, c *services.Caller, taskID uuid.UUID) (*Task, error) {
	done := "done"
	return s.Update(ctx, c, taskID, UpdateInput{Status: &done})
}

// Cancel is a shortcut to soft-delete a task via the 'cancelled' status. The
// row stays in the DB so an admin can recover it with an UPDATE; it just
// drops off the swipe feed.
func (s *TaskService) Cancel(ctx context.Context, c *services.Caller, taskID uuid.UUID) (*Task, error) {
	cancelled := "cancelled"
	return s.Update(ctx, c, taskID, UpdateInput{Status: &cancelled})
}

// snoozeHourLocal is the clock hour (local to the tenant's timezone) a
// snoozed task reappears at. 03:00 keeps the task off the feed through
// the overnight window regardless of how late the user is working;
// anything on the snoozer's desk for the next morning shows up before
// they wake up, not in the middle of the night.
const snoozeHourLocal = 3

// SnoozeDaysToUntil validates a snooze duration (between 1 and 365 days)
// and returns the snoozed_until timestamp: N calendar days from today
// (in tz), clock set to snoozeHourLocal local, converted to UTC. Shared
// by the card action handler, agent tool, and MCP tool so the bounds
// and time calculation live in one place. The UI exposes a curated set
// of options; this validator only catches typos and abuse.
func SnoozeDaysToUntil(days int, tz string) (time.Time, error) {
	if days < 1 || days > 365 {
		return time.Time{}, fmt.Errorf("snooze days must be between 1 and 365 (got %d)", days)
	}
	return snoozeUntilAt(time.Now(), days, tz)
}

// snoozeUntilAt is the pure computation behind SnoozeDaysToUntil,
// exposed to the test package so the "advance N days then set to
// 03:00 local" rule can be verified without freezing wall time.
func snoozeUntilAt(now time.Time, days int, tz string) (time.Time, error) {
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("loading timezone %q: %w", tz, err)
	}
	local := now.In(loc)
	advanced := time.Date(local.Year(), local.Month(), local.Day()+days,
		snoozeHourLocal, 0, 0, 0, loc)
	return advanced.UTC(), nil
}

// SnoozeDays looks up the tenant timezone and snoozes the task until
// snoozeHourLocal (03:00) local time N days from today. Thin wrapper
// around Snooze so callers don't have to fetch the tenant row
// themselves.
func (s *TaskService) SnoozeDays(ctx context.Context, c *services.Caller, taskID uuid.UUID, days int) (*Task, error) {
	tz, err := s.tenantTimezone(ctx, c.TenantID)
	if err != nil {
		return nil, err
	}
	until, err := SnoozeDaysToUntil(days, tz)
	if err != nil {
		return nil, err
	}
	return s.Snooze(ctx, c, taskID, until)
}

// SnoozeUntilNextMonday snoozes the task until the upcoming Monday at
// snoozeHourLocal (03:00) in the tenant timezone. If today is Monday,
// "next" means a full week out — the user tapped "Monday" knowing today
// is Monday, so they mean the one after this.
func (s *TaskService) SnoozeUntilNextMonday(ctx context.Context, c *services.Caller, taskID uuid.UUID) (*Task, error) {
	tz, err := s.tenantTimezone(ctx, c.TenantID)
	if err != nil {
		return nil, err
	}
	until, err := snoozeUntilNextMondayAt(time.Now(), tz)
	if err != nil {
		return nil, err
	}
	return s.Snooze(ctx, c, taskID, until)
}

func (s *TaskService) tenantTimezone(ctx context.Context, tenantID uuid.UUID) (string, error) {
	tenant, err := models.GetTenantByID(ctx, s.pool, tenantID)
	if err != nil {
		return "", fmt.Errorf("looking up tenant: %w", err)
	}
	if tenant == nil || tenant.Timezone == "" {
		return "UTC", nil
	}
	return tenant.Timezone, nil
}

// snoozeUntilNextMondayAt is the pure computation behind
// SnoozeUntilNextMonday. "Next Monday" is the nearest future Monday
// counting today's calendar day as not-Monday — so tapping on Monday
// lands you seven days out, not back to 03:00 that same morning.
func snoozeUntilNextMondayAt(now time.Time, tz string) (time.Time, error) {
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("loading timezone %q: %w", tz, err)
	}
	local := now.In(loc)
	// Weekday(): Sunday=0, Monday=1, ..., Saturday=6. We want the offset
	// to the next Monday strictly after today; if today is Monday the
	// user means the following week.
	offset := (int(time.Monday) - int(local.Weekday()) + 7) % 7
	if offset == 0 {
		offset = 7
	}
	advanced := time.Date(local.Year(), local.Month(), local.Day()+offset,
		snoozeHourLocal, 0, 0, 0, loc)
	return advanced.UTC(), nil
}

// Snooze hides the task from the caller's swipe feed until `until`. Re-snooze
// overwrites the timestamp. A comment event records the action for the audit
// log — we reuse the existing comment type rather than a new event_type so
// the CHECK constraint stays narrow.
func (s *TaskService) Snooze(ctx context.Context, c *services.Caller, taskID uuid.UUID, until time.Time) (*Task, error) {
	t, err := getTask(ctx, s.pool, c.TenantID, taskID)
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
	if err := updateTask(ctx, s.pool, c.TenantID, taskID, TaskUpdates{SnoozedUntil: &until}); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, taskID, &c.UserID, "comment",
		"Snoozed until "+until.Format(time.RFC3339), "", "")
	return getTask(ctx, s.pool, c.TenantID, taskID)
}

// AddComment appends a comment to the activity log.
func (s *TaskService) AddComment(ctx context.Context, c *services.Caller, taskID uuid.UUID, content string) error {
	t, err := getTask(ctx, s.pool, c.TenantID, taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return services.ErrNotFound
		}
		return err
	}
	if !s.canRead(ctx, c, t) {
		return services.ErrNotFound
	}
	return appendEvent(ctx, s.pool, c.TenantID, taskID, &c.UserID, "comment", content, "", "")
}

// canRead and canWrite collapse to the same check now that visibility is
// purely role-membership. Admin always wins; otherwise the caller must be
// in the role that owns this task.
func (s *TaskService) canRead(ctx context.Context, c *services.Caller, t *Task) bool {
	if c.IsAdmin {
		return true
	}
	scope, err := getScopeRow(ctx, s.pool, c.TenantID, t.ScopeID)
	if err != nil {
		return false
	}
	return c.CanSee([]services.ScopeRef{scope})
}

func (s *TaskService) canWrite(ctx context.Context, c *services.Caller, t *Task) bool {
	return s.canRead(ctx, c, t)
}

// FormatTask formats a task for display.
func FormatTask(t *Task) string {
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

	if t.AssigneeUserID == nil {
		b.WriteString(" [unassigned]")
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

// FormatTaskDetailed formats a task with its events for display.
func FormatTaskDetailed(t *Task, events []TaskEvent) string {
	var b strings.Builder
	b.WriteString(FormatTask(t))
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
			case "assignee_change":
				fmt.Fprintf(&b, "\n  [%s] Assignee: %s → %s", ts, e.OldValue, e.NewValue)
			case "priority_change":
				fmt.Fprintf(&b, "\n  [%s] Priority: %s → %s", ts, e.OldValue, e.NewValue)
			}
		}
	}
	return b.String()
}
