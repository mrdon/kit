package todo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	Private       bool       `json:"private"`
	AssignedTo    *uuid.UUID `json:"assigned_to,omitempty"`
	RoleScope     string     `json:"role_scope,omitempty"`
	DueDate       *time.Time `json:"due_date,omitempty"`
	CreatedBy     uuid.UUID  `json:"created_by"`
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
	AssignedToMe bool
	RoleScope    string
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
	Private       *bool
	AssignedTo    *uuid.UUID
	RoleScope     *string
	DueDate       *time.Time
	ClearDueDate  bool
}

func scanTodo(row interface{ Scan(...any) error }) (*Todo, error) {
	var t Todo
	var description, blockedReason, roleScope *string
	var dueDate *time.Time
	err := row.Scan(
		&t.ID, &t.TenantID, &t.Title, &description,
		&t.Status, &t.Priority, &blockedReason, &t.Private,
		&t.AssignedTo, &roleScope, &dueDate,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.ClosedAt,
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
	if roleScope != nil {
		t.RoleScope = *roleScope
	}
	t.DueDate = dueDate
	return &t, nil
}

const todoColumns = `id, tenant_id, title, description, status, priority, blocked_reason, private, assigned_to, role_scope, due_date, created_by, created_at, updated_at, closed_at`

func createTodo(ctx context.Context, pool *pgxpool.Pool, t *Todo) error {
	return pool.QueryRow(ctx, `
		INSERT INTO app_todos (tenant_id, title, description, status, priority, private, assigned_to, role_scope, due_date, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at, updated_at`,
		t.TenantID, t.Title, nilIfEmpty(t.Description), t.Status, t.Priority,
		t.Private, t.AssignedTo, nilIfEmpty(t.RoleScope), t.DueDate, t.CreatedBy,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

func getTodo(ctx context.Context, pool *pgxpool.Pool, tenantID, todoID uuid.UUID) (*Todo, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+todoColumns+` FROM app_todos WHERE tenant_id = $1 AND id = $2`,
		tenantID, todoID,
	)
	t, err := scanTodo(row)
	if err != nil {
		return nil, fmt.Errorf("getting todo: %w", err)
	}
	return t, nil
}

func listTodosAll(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, f TodoFilters) ([]Todo, error) {
	query, args := buildListQuery(tenantID, nil, nil, f)
	return queryTodos(ctx, pool, query, args)
}

func listTodosScoped(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roles []string, f TodoFilters) ([]Todo, error) {
	query, args := buildListQuery(tenantID, &userID, roles, f)
	return queryTodos(ctx, pool, query, args)
}

func buildListQuery(tenantID uuid.UUID, userID *uuid.UUID, roles []string, f TodoFilters) (string, []any) {
	var b strings.Builder
	args := []any{tenantID}
	argN := 1

	b.WriteString(`SELECT ` + todoColumns + ` FROM app_todos WHERE tenant_id = $1`)

	// Visibility filter for non-admins
	if userID != nil {
		argN++
		// Non-private: visible to all. Private: only creator/assignee.
		b.WriteString(fmt.Sprintf(` AND (private = false OR created_by = $%d OR assigned_to = $%d)`, argN, argN))
		args = append(args, *userID)
	}

	if f.Status != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND status = $%d`, argN))
		args = append(args, f.Status)
	}

	if f.Priority != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND priority = $%d`, argN))
		args = append(args, f.Priority)
	}

	if f.AssignedToMe && userID != nil {
		argN++
		b.WriteString(fmt.Sprintf(` AND assigned_to = $%d`, argN))
		args = append(args, *userID)
	}

	if f.RoleScope != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND role_scope = $%d`, argN))
		args = append(args, f.RoleScope)
	}

	if f.Search != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND to_tsvector('english', coalesce(title, '') || ' ' || coalesce(description, '')) @@ plainto_tsquery('english', $%d)`, argN))
		args = append(args, f.Search)
	}

	if f.Overdue {
		b.WriteString(` AND due_date < CURRENT_DATE AND status != 'done'`)
	}

	if f.ClosedSince != nil {
		argN++
		b.WriteString(fmt.Sprintf(` AND closed_at >= $%d`, argN))
		args = append(args, *f.ClosedSince)
	}

	b.WriteString(` ORDER BY CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 END, due_date ASC NULLS LAST, created_at DESC`)
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
		if *u.Status == "done" {
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
	if u.Private != nil {
		argN++
		sets = append(sets, fmt.Sprintf("private = $%d", argN))
		args = append(args, *u.Private)
	}
	if u.AssignedTo != nil {
		argN++
		sets = append(sets, fmt.Sprintf("assigned_to = $%d", argN))
		args = append(args, *u.AssignedTo)
	}
	if u.RoleScope != nil {
		argN++
		sets = append(sets, fmt.Sprintf("role_scope = $%d", argN))
		args = append(args, *u.RoleScope)
	}
	if u.DueDate != nil {
		argN++
		sets = append(sets, fmt.Sprintf("due_date = $%d", argN))
		args = append(args, *u.DueDate)
	}
	if u.ClearDueDate {
		sets = append(sets, "due_date = NULL")
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

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
