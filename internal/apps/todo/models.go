package todo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// Todo represents a todo item.
type Todo struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      uuid.UUID  `json:"tenant_id"`
	Title         string     `json:"title"`
	Description   string     `json:"description,omitempty"`
	Status        string     `json:"status"`
	Priority      string     `json:"priority"`
	BlockedReason string     `json:"blocked_reason,omitempty"`
	ScopeID       uuid.UUID  `json:"scope_id"`
	Visibility    string     `json:"visibility"` // "scoped" or "public"
	DueDate       *time.Time `json:"due_date,omitempty"`
	SnoozedUntil  *time.Time `json:"snoozed_until,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
}

// TodoEvent represents an entry in the activity log.
type TodoEvent struct {
	ID        uuid.UUID  `json:"id"`
	TenantID  uuid.UUID  `json:"tenant_id"`
	TodoID    uuid.UUID  `json:"todo_id"`
	AuthorID  *uuid.UUID `json:"author_id,omitempty"`
	EventType string     `json:"event_type"`
	Content   string     `json:"content,omitempty"`
	OldValue  string     `json:"old_value,omitempty"`
	NewValue  string     `json:"new_value,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// TodoFilters holds optional filters for listing todos.
type TodoFilters struct {
	Status       string
	Priority     string
	AssignedToMe bool   // alias for "scoped to caller's user-scope"
	RoleName     string // human-friendly role name; resolved to role_id at query time
	Search       string
	Overdue      bool
	ClosedSince  *time.Time
}

// TodoUpdates holds optional fields for updating a todo.
type TodoUpdates struct {
	Title         *string
	Description   *string
	Status        *string
	Priority      *string
	BlockedReason *string
	ScopeID       *uuid.UUID
	Visibility    *string
	DueDate       *time.Time
	ClearDueDate  bool
	SnoozedUntil  *time.Time
	ClearSnooze   bool
}

// todoColumns is the SELECT list for app_todos, always aliased as t. in the
// query — the alias is required because list queries JOIN scopes which has
// its own id/tenant_id columns.
const todoColumns = `t.id, t.tenant_id, t.title, t.description, t.status, t.priority, t.blocked_reason, t.scope_id, t.visibility, t.due_date, t.snoozed_until, t.created_at, t.updated_at, t.closed_at`

func scanTodo(row interface{ Scan(...any) error }) (*Todo, error) {
	var t Todo
	var description, blockedReason *string
	var dueDate, snoozedUntil *time.Time
	err := row.Scan(
		&t.ID, &t.TenantID, &t.Title, &description,
		&t.Status, &t.Priority, &blockedReason,
		&t.ScopeID, &t.Visibility, &dueDate, &snoozedUntil,
		&t.CreatedAt, &t.UpdatedAt, &t.ClosedAt,
	)
	if err != nil {
		return nil, err
	}
	if description != nil {
		t.Description = *description
	}
	if blockedReason != nil {
		t.BlockedReason = *blockedReason
	}
	t.DueDate = dueDate
	t.SnoozedUntil = snoozedUntil
	return &t, nil
}

func createTodo(ctx context.Context, pool *pgxpool.Pool, t *Todo) error {
	return pool.QueryRow(ctx, `
		INSERT INTO app_todos (tenant_id, title, description, status, priority, scope_id, visibility, due_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at`,
		t.TenantID, t.Title, nilIfEmpty(t.Description), t.Status, t.Priority,
		t.ScopeID, t.Visibility, t.DueDate,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

func getTodo(ctx context.Context, pool *pgxpool.Pool, tenantID, todoID uuid.UUID) (*Todo, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+todoColumns+` FROM app_todos t WHERE t.tenant_id = $1 AND t.id = $2`,
		tenantID, todoID,
	)
	t, err := scanTodo(row)
	if err != nil {
		return nil, fmt.Errorf("getting todo: %w", err)
	}
	return t, nil
}

// listTodos returns todos visible to the caller. When userID is nil, no
// visibility filter is applied (admin path). Otherwise: visibility='public' OR
// the caller's scope_id matches.
func listTodos(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, userID *uuid.UUID, roleIDs []uuid.UUID, f TodoFilters) ([]Todo, error) {
	query, args := buildListQuery(tenantID, userID, roleIDs, f)
	return queryTodos(ctx, pool, query, args)
}

func buildListQuery(tenantID uuid.UUID, userID *uuid.UUID, roleIDs []uuid.UUID, f TodoFilters) (string, []any) {
	var b strings.Builder
	args := []any{tenantID}
	argN := 1

	b.WriteString(`SELECT ` + todoColumns + ` FROM app_todos t`)

	// We only need to JOIN scopes when we have to filter by it. For admin
	// (no userID) we still might need the join if the caller filters by role.
	needScopeJoin := userID != nil || f.AssignedToMe || f.RoleName != ""
	if needScopeJoin {
		b.WriteString(` JOIN scopes sc ON sc.id = t.scope_id`)
	}
	b.WriteString(` WHERE t.tenant_id = $1`)

	if userID != nil {
		// Public todos are visible to everyone in the tenant; otherwise the
		// caller's scope must match.
		argN++
		userParam := argN
		args = append(args, *userID)
		b.WriteString(fmt.Sprintf(` AND (t.visibility = 'public' OR sc.user_id = $%d`, userParam))
		if len(roleIDs) > 0 {
			argN++
			b.WriteString(fmt.Sprintf(` OR sc.role_id = ANY($%d)`, argN))
			args = append(args, roleIDs)
		}
		b.WriteString(`)`)
	}

	if f.Status != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND t.status = $%d`, argN))
		args = append(args, f.Status)
	}

	if f.Priority != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND t.priority = $%d`, argN))
		args = append(args, f.Priority)
	}

	if f.AssignedToMe && userID != nil {
		argN++
		b.WriteString(fmt.Sprintf(` AND sc.user_id = $%d`, argN))
		args = append(args, *userID)
	}

	if f.RoleName != "" {
		argN++
		b.WriteString(fmt.Sprintf(
			` AND sc.role_id = (SELECT id FROM roles WHERE tenant_id = $1 AND name = $%d)`, argN))
		args = append(args, f.RoleName)
	}

	if f.Search != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND to_tsvector('english', coalesce(t.title, '') || ' ' || coalesce(t.description, '')) @@ plainto_tsquery('english', $%d)`, argN))
		args = append(args, f.Search)
	}

	if f.Overdue {
		b.WriteString(` AND t.due_date < CURRENT_DATE AND t.status NOT IN ('done','cancelled')`)
	}

	if f.ClosedSince != nil {
		argN++
		b.WriteString(fmt.Sprintf(` AND t.closed_at >= $%d`, argN))
		args = append(args, *f.ClosedSince)
	}

	b.WriteString(` ORDER BY CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 END, t.due_date ASC NULLS LAST, t.created_at DESC`)
	b.WriteString(` LIMIT 50`)

	return b.String(), args
}

func queryTodos(ctx context.Context, pool *pgxpool.Pool, query string, args []any) ([]Todo, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing todos: %w", err)
	}
	defer rows.Close()

	var todos []Todo
	for rows.Next() {
		t, err := scanTodo(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning todo: %w", err)
		}
		todos = append(todos, *t)
	}
	return todos, rows.Err()
}

func updateTodo(ctx context.Context, pool *pgxpool.Pool, tenantID, todoID uuid.UUID, u TodoUpdates) error {
	var sets []string
	var args []any
	argN := 2
	args = append(args, tenantID, todoID)

	if u.Title != nil {
		argN++
		sets = append(sets, fmt.Sprintf("title = $%d", argN))
		args = append(args, *u.Title)
	}
	if u.Description != nil {
		argN++
		sets = append(sets, fmt.Sprintf("description = $%d", argN))
		args = append(args, *u.Description)
	}
	if u.Status != nil {
		argN++
		sets = append(sets, fmt.Sprintf("status = $%d", argN))
		args = append(args, *u.Status)
		if *u.Status == "done" || *u.Status == "cancelled" {
			sets = append(sets, "closed_at = now()")
		} else {
			sets = append(sets, "closed_at = NULL")
		}
	}
	if u.Priority != nil {
		argN++
		sets = append(sets, fmt.Sprintf("priority = $%d", argN))
		args = append(args, *u.Priority)
	}
	if u.BlockedReason != nil {
		argN++
		sets = append(sets, fmt.Sprintf("blocked_reason = $%d", argN))
		args = append(args, *u.BlockedReason)
	} else if u.Status != nil && *u.Status != "blocked" {
		sets = append(sets, "blocked_reason = NULL")
	}
	if u.ScopeID != nil {
		argN++
		sets = append(sets, fmt.Sprintf("scope_id = $%d", argN))
		args = append(args, *u.ScopeID)
	}
	if u.Visibility != nil {
		argN++
		sets = append(sets, fmt.Sprintf("visibility = $%d", argN))
		args = append(args, *u.Visibility)
	}
	if u.DueDate != nil {
		argN++
		sets = append(sets, fmt.Sprintf("due_date = $%d", argN))
		args = append(args, *u.DueDate)
	}
	if u.ClearDueDate {
		sets = append(sets, "due_date = NULL")
	}
	if u.SnoozedUntil != nil {
		argN++
		sets = append(sets, fmt.Sprintf("snoozed_until = $%d", argN))
		args = append(args, *u.SnoozedUntil)
	}
	if u.ClearSnooze {
		sets = append(sets, "snoozed_until = NULL")
	}

	if len(sets) == 0 {
		return nil
	}

	sets = append(sets, "updated_at = now()")
	query := fmt.Sprintf(`UPDATE app_todos SET %s WHERE tenant_id = $1 AND id = $2`, strings.Join(sets, ", "))
	_, err := pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating todo: %w", err)
	}
	return nil
}

func appendEvent(ctx context.Context, pool *pgxpool.Pool, tenantID, todoID uuid.UUID, authorID *uuid.UUID, eventType, content, oldValue, newValue string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_todo_events (tenant_id, todo_id, author_id, event_type, content, old_value, new_value)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		tenantID, todoID, authorID, eventType,
		nilIfEmpty(content), nilIfEmpty(oldValue), nilIfEmpty(newValue),
	)
	if err != nil {
		return fmt.Errorf("appending todo event: %w", err)
	}
	return nil
}

func getRecentEvents(ctx context.Context, pool *pgxpool.Pool, tenantID, todoID uuid.UUID, limit int) ([]TodoEvent, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, todo_id, author_id, event_type, content, old_value, new_value, created_at
		FROM app_todo_events
		WHERE tenant_id = $1 AND todo_id = $2
		ORDER BY created_at DESC
		LIMIT $3`,
		tenantID, todoID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting todo events: %w", err)
	}
	defer rows.Close()

	var events []TodoEvent
	for rows.Next() {
		var e TodoEvent
		var content, oldValue, newValue *string
		if err := rows.Scan(&e.ID, &e.TenantID, &e.TodoID, &e.AuthorID, &e.EventType, &content, &oldValue, &newValue, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning todo event: %w", err)
		}
		if content != nil {
			e.Content = *content
		}
		if oldValue != nil {
			e.OldValue = *oldValue
		}
		if newValue != nil {
			e.NewValue = *newValue
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// getScopeRow returns the scope row for a single scope_id, used for
// in-memory access checks via services.Caller.CanSee.
func getScopeRow(ctx context.Context, pool *pgxpool.Pool, tenantID, scopeID uuid.UUID) (models.ScopeRow, error) {
	var r models.ScopeRow
	err := pool.QueryRow(ctx, `
		SELECT id, role_id, user_id FROM scopes WHERE tenant_id = $1 AND id = $2`,
		tenantID, scopeID,
	).Scan(&r.ID, &r.RoleID, &r.UserID)
	if err != nil {
		return models.ScopeRow{}, fmt.Errorf("loading scope %s: %w", scopeID, err)
	}
	return r, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
