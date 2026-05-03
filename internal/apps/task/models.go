package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// Task represents a task item.
type Task struct {
	ID             uuid.UUID    `json:"id"`
	TenantID       uuid.UUID    `json:"tenant_id"`
	Title          string       `json:"title"`
	Description    string       `json:"description,omitempty"`
	Status         string       `json:"status"`
	Priority       string       `json:"priority"`
	BlockedReason  string       `json:"blocked_reason,omitempty"`
	ScopeID        uuid.UUID    `json:"scope_id"`
	AssigneeUserID *uuid.UUID   `json:"assignee_user_id,omitempty"`
	DueDate        *time.Time   `json:"due_date,omitempty"`
	SnoozedUntil   *time.Time   `json:"snoozed_until,omitempty"`
	Resolutions    []Resolution `json:"resolutions,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	ClosedAt       *time.Time   `json:"closed_at,omitempty"`
}

// TaskEvent represents an entry in the activity log.
type TaskEvent struct {
	ID        uuid.UUID  `json:"id"`
	TenantID  uuid.UUID  `json:"tenant_id"`
	TaskID    uuid.UUID  `json:"task_id"`
	AuthorID  *uuid.UUID `json:"author_id,omitempty"`
	EventType string     `json:"event_type"`
	Content   string     `json:"content,omitempty"`
	OldValue  string     `json:"old_value,omitempty"`
	NewValue  string     `json:"new_value,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// TaskFilters holds optional filters for listing tasks.
type TaskFilters struct {
	Status         string
	Priority       string
	AssignedToMe   bool       // sugar for AssigneeUserID = caller
	AssigneeUserID *uuid.UUID // filter by exact assignee
	Unassigned     bool       // filter to assignee_user_id IS NULL
	RoleName       string     // human-friendly role name; resolved at query time
	Search         string
	Overdue        bool
	ClosedSince    *time.Time
	// IncludeClosed admits done/cancelled rows when Status is empty. Default
	// (false) excludes them so a generic list_tasks call doesn't drag the
	// full history into agent context. An explicit Status filter or a
	// ClosedSince window already implies the caller wants closed rows, so
	// this flag is only consulted for the unfiltered case.
	IncludeClosed bool
}

// TaskUpdates holds optional fields for updating a task.
type TaskUpdates struct {
	Title          *string
	Description    *string
	Status         *string
	Priority       *string
	BlockedReason  *string
	ScopeID        *uuid.UUID
	AssigneeUserID *uuid.UUID
	ClearAssignee  bool
	DueDate        *time.Time
	ClearDueDate   bool
	SnoozedUntil   *time.Time
	ClearSnooze    bool
}

// taskColumns is the SELECT list for app_tasks, always aliased as t. in the
// query — the alias is required because list queries JOIN scopes which has
// its own id/tenant_id columns.
const taskColumns = `t.id, t.tenant_id, t.title, t.description, t.status, t.priority, t.blocked_reason, t.scope_id, t.assignee_user_id, t.due_date, t.snoozed_until, t.resolutions, t.created_at, t.updated_at, t.closed_at`

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	var description, blockedReason *string
	var dueDate, snoozedUntil *time.Time
	var resolutionsJSON []byte
	err := row.Scan(
		&t.ID, &t.TenantID, &t.Title, &description,
		&t.Status, &t.Priority, &blockedReason,
		&t.ScopeID, &t.AssigneeUserID, &dueDate, &snoozedUntil,
		&resolutionsJSON,
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
	if len(resolutionsJSON) > 0 {
		if err := json.Unmarshal(resolutionsJSON, &t.Resolutions); err != nil {
			return nil, fmt.Errorf("decoding resolutions: %w", err)
		}
	}
	return &t, nil
}

func createTask(ctx context.Context, pool *pgxpool.Pool, t *Task) error {
	return pool.QueryRow(ctx, `
		INSERT INTO app_tasks (tenant_id, title, description, status, priority, scope_id, assignee_user_id, due_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at`,
		t.TenantID, t.Title, nilIfEmpty(t.Description), t.Status, t.Priority,
		t.ScopeID, t.AssigneeUserID, t.DueDate,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

func getTask(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID) (*Task, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM app_tasks t WHERE t.tenant_id = $1 AND t.id = $2`,
		tenantID, taskID,
	)
	t, err := scanTask(row)
	if err != nil {
		return nil, fmt.Errorf("getting task: %w", err)
	}
	return t, nil
}

// listTasks returns tasks visible to the caller. Visibility is now purely
// role membership: a non-admin caller sees a task iff they're in the role
// the task is scoped to. Admin (userID == nil) bypasses the scope filter.
func listTasks(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, userID *uuid.UUID, roleIDs []uuid.UUID, f TaskFilters) ([]Task, error) {
	query, args := buildListQuery(tenantID, userID, roleIDs, f)
	return queryTasks(ctx, pool, query, args)
}

func buildListQuery(tenantID uuid.UUID, userID *uuid.UUID, roleIDs []uuid.UUID, f TaskFilters) (string, []any) {
	var b strings.Builder
	args := []any{tenantID}
	argN := 1

	b.WriteString(`SELECT ` + taskColumns + ` FROM app_tasks t`)

	needScopeJoin := userID != nil || f.RoleName != ""
	if needScopeJoin {
		b.WriteString(` JOIN scopes sc ON sc.id = t.scope_id`)
	}
	b.WriteString(` WHERE t.tenant_id = $1`)

	if userID != nil {
		// Role membership = visibility. Without any roles the caller sees
		// nothing scoped (default deny).
		if len(roleIDs) > 0 {
			argN++
			b.WriteString(fmt.Sprintf(` AND sc.role_id = ANY($%d)`, argN))
			args = append(args, roleIDs)
		} else {
			b.WriteString(` AND FALSE`)
		}
	}

	if f.Status != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND t.status = $%d`, argN))
		args = append(args, f.Status)
	} else if !f.IncludeClosed && f.ClosedSince == nil {
		b.WriteString(` AND t.status NOT IN ('done','cancelled')`)
	}

	if f.Priority != "" {
		argN++
		b.WriteString(fmt.Sprintf(` AND t.priority = $%d`, argN))
		args = append(args, f.Priority)
	}

	if f.AssignedToMe && userID != nil {
		argN++
		b.WriteString(fmt.Sprintf(` AND t.assignee_user_id = $%d`, argN))
		args = append(args, *userID)
	} else if f.AssigneeUserID != nil {
		argN++
		b.WriteString(fmt.Sprintf(` AND t.assignee_user_id = $%d`, argN))
		args = append(args, *f.AssigneeUserID)
	}
	if f.Unassigned {
		b.WriteString(` AND t.assignee_user_id IS NULL`)
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

func queryTasks(ctx context.Context, pool *pgxpool.Pool, query string, args []any) ([]Task, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func updateTask(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, u TaskUpdates) error {
	var sets []string
	var args []any
	argN := 2
	args = append(args, tenantID, taskID)

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
	if u.AssigneeUserID != nil {
		argN++
		sets = append(sets, fmt.Sprintf("assignee_user_id = $%d", argN))
		args = append(args, *u.AssigneeUserID)
	} else if u.ClearAssignee {
		sets = append(sets, "assignee_user_id = NULL")
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
	query := fmt.Sprintf(`UPDATE app_tasks SET %s WHERE tenant_id = $1 AND id = $2`, strings.Join(sets, ", "))
	_, err := pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating task: %w", err)
	}
	return nil
}

func appendEvent(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, authorID *uuid.UUID, eventType, content, oldValue, newValue string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_task_events (tenant_id, task_id, author_id, event_type, content, old_value, new_value)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		tenantID, taskID, authorID, eventType,
		nilIfEmpty(content), nilIfEmpty(oldValue), nilIfEmpty(newValue),
	)
	if err != nil {
		return fmt.Errorf("appending task event: %w", err)
	}
	return nil
}

func getRecentEvents(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, limit int) ([]TaskEvent, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, task_id, author_id, event_type, content, old_value, new_value, created_at
		FROM app_task_events
		WHERE tenant_id = $1 AND task_id = $2
		ORDER BY created_at DESC
		LIMIT $3`,
		tenantID, taskID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting task events: %w", err)
	}
	defer rows.Close()

	var events []TaskEvent
	for rows.Next() {
		var e TaskEvent
		var content, oldValue, newValue *string
		if err := rows.Scan(&e.ID, &e.TenantID, &e.TaskID, &e.AuthorID, &e.EventType, &content, &oldValue, &newValue, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning task event: %w", err)
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

// setTaskResolutions writes the full resolutions array for a task, replacing
// whatever was there. Writing an empty slice stores JSON [] (not NULL) so
// callers can distinguish "Haiku ran and nothing fit" from "not yet run".
// A no-op (zero rows affected) when the task has been deleted.
func setTaskResolutions(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, resolutions []Resolution) error {
	if resolutions == nil {
		resolutions = []Resolution{}
	}
	payload, err := json.Marshal(resolutions)
	if err != nil {
		return fmt.Errorf("encoding resolutions: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE app_tasks SET resolutions = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenantID, taskID, payload,
	); err != nil {
		return fmt.Errorf("writing resolutions: %w", err)
	}
	return nil
}

// removeTaskResolution drops the resolution with the matching id from the
// stored array. Uses a jsonb subselect so concurrent removes of different
// ids don't race on array indices.
func removeTaskResolution(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, resolutionID string) error {
	if _, err := pool.Exec(ctx, `
		UPDATE app_tasks
		SET resolutions = COALESCE(
			(SELECT jsonb_agg(elem)
			 FROM jsonb_array_elements(resolutions) AS elem
			 WHERE elem->>'id' <> $3),
			'[]'::jsonb),
		    updated_at = now()
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, taskID, resolutionID,
	); err != nil {
		return fmt.Errorf("removing resolution: %w", err)
	}
	return nil
}
