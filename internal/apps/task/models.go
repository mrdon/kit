package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// Task priority levels, highest first. The DB CHECK constraint (migration
// 058) is the source of truth for what's storable; these constants keep
// Go-side defaults and comparisons from drifting into raw string literals.
const (
	PriorityBlocker = "blocker"
	PriorityHigh    = "high"
	PriorityNormal  = "normal"
)

// DefaultPriority is applied when a task is created without one.
const DefaultPriority = PriorityNormal

// Priorities lists the valid priority values, highest first.
var Priorities = []string{PriorityBlocker, PriorityHigh, PriorityNormal}

// ValidPriority reports whether p is one of the storable priority values.
// Callers validate before insert/update so a stale value (e.g. the old
// low/medium/urgent scale) surfaces as a clean 400 instead of a raw DB
// CHECK-constraint violation surfaced as a 500.
func ValidPriority(p string) bool {
	return slices.Contains(Priorities, p)
}

// Task represents a task item.
type Task struct {
	ID             uuid.UUID    `json:"id"`
	TenantID       uuid.UUID    `json:"tenant_id"`
	Title          string       `json:"title"`
	Description    string       `json:"description,omitempty"`
	Status         string       `json:"status"`
	Priority       string       `json:"priority"`
	Category       string       `json:"category,omitempty"`
	BlockedReason  string       `json:"blocked_reason,omitempty"`
	ScopeID        uuid.UUID    `json:"scope_id"`
	AssigneeUserID *uuid.UUID   `json:"assignee_user_id,omitempty"`
	DueDate        *time.Time   `json:"due_date,omitempty"`
	SnoozedUntil   *time.Time   `json:"snoozed_until,omitempty"`
	Resolutions    []Resolution `json:"resolutions,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	ClosedAt       *time.Time   `json:"closed_at,omitempty"`
	// DedupKey, when set, makes creation idempotent via a tenant-wide unique
	// index that spans all statuses. Set by the email-intake path (derived
	// from the source email) so repeated scans never spawn duplicate tasks.
	// Not selected back by list/get queries — it is write-only provenance.
	DedupKey string `json:"dedup_key,omitempty"`
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
	Category       string     // filter by exact category label
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
	// Limit caps the number of rows returned. 0 means the default
	// (defaultListLimit); values above maxListLimit are clamped. Raising it is
	// how the email-intake dedup read sees the full task set — including
	// cancelled rows — instead of a truncated first page.
	Limit int
}

const (
	// defaultListLimit bounds a normal list_tasks call so browsing doesn't drag
	// the full history into agent context.
	defaultListLimit = 50
	// maxListLimit is the hard ceiling any caller can request — high enough for
	// a complete dedup view of a real tenant, low enough to stay sane.
	maxListLimit = 500
)

// TaskUpdates holds optional fields for updating a task.
type TaskUpdates struct {
	Title          *string
	Description    *string
	Status         *string
	Priority       *string
	Category       *string
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
const taskColumns = `t.id, t.tenant_id, t.title, t.description, t.status, t.priority, t.category, t.blocked_reason, t.scope_id, t.assignee_user_id, t.due_date, t.snoozed_until, t.resolutions, t.created_at, t.updated_at, t.closed_at`

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	var description, category, blockedReason *string
	var dueDate, snoozedUntil *time.Time
	var resolutionsJSON []byte
	err := row.Scan(
		&t.ID, &t.TenantID, &t.Title, &description,
		&t.Status, &t.Priority, &category, &blockedReason,
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
	if category != nil {
		t.Category = *category
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

// createTask inserts a new task. When t.DedupKey is set the insert is
// idempotent: the app_tasks_dedup_key_unique index (migration 068) spans every
// status, so a source that already produced a task — even one since cancelled —
// collides. On collision DO NOTHING suppresses the insert; we load the existing
// row into t and report existed=true so the caller points at the original
// instead of creating a duplicate. Tasks without a key are never constrained.
func createTask(ctx context.Context, pool *pgxpool.Pool, t *Task) (existed bool, err error) {
	err = pool.QueryRow(ctx, `
		INSERT INTO app_tasks (tenant_id, title, description, status, priority, scope_id, assignee_user_id, due_date, dedup_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING
		RETURNING id, created_at, updated_at`,
		t.TenantID, t.Title, nilIfEmpty(t.Description), t.Status, t.Priority,
		t.ScopeID, t.AssigneeUserID, t.DueDate, nilIfEmpty(t.DedupKey),
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// DO NOTHING suppressed the insert: a task with this dedup_key already
		// exists. Load it so the caller can reference the original.
		row := pool.QueryRow(ctx,
			`SELECT `+taskColumns+` FROM app_tasks t WHERE t.tenant_id = $1 AND t.dedup_key = $2`,
			t.TenantID, t.DedupKey)
		existing, e := scanTask(row)
		if e != nil {
			return false, fmt.Errorf("loading existing task for dedup key: %w", e)
		}
		*t = *existing
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
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
			fmt.Fprintf(&b, ` AND sc.role_id = ANY($%d)`, argN)
			args = append(args, roleIDs)
		} else {
			b.WriteString(` AND FALSE`)
		}
	}

	if f.Status != "" {
		argN++
		fmt.Fprintf(&b, ` AND t.status = $%d`, argN)
		args = append(args, f.Status)
	} else if !f.IncludeClosed && f.ClosedSince == nil {
		b.WriteString(` AND t.status NOT IN ('done','cancelled')`)
	}

	if f.Priority != "" {
		argN++
		fmt.Fprintf(&b, ` AND t.priority = $%d`, argN)
		args = append(args, f.Priority)
	}

	if f.Category != "" {
		argN++
		fmt.Fprintf(&b, ` AND t.category = $%d`, argN)
		args = append(args, f.Category)
	}

	if f.AssignedToMe && userID != nil {
		argN++
		fmt.Fprintf(&b, ` AND t.assignee_user_id = $%d`, argN)
		args = append(args, *userID)
	} else if f.AssigneeUserID != nil {
		argN++
		fmt.Fprintf(&b, ` AND t.assignee_user_id = $%d`, argN)
		args = append(args, *f.AssigneeUserID)
	}
	if f.Unassigned {
		b.WriteString(` AND t.assignee_user_id IS NULL`)
	}

	if f.RoleName != "" {
		argN++
		fmt.Fprintf(&b, ` AND sc.role_id = (SELECT id FROM roles WHERE tenant_id = $1 AND name = $%d)`, argN)
		args = append(args, f.RoleName)
	}

	if f.Search != "" {
		argN++
		fmt.Fprintf(&b, ` AND to_tsvector('english', coalesce(t.title, '') || ' ' || coalesce(t.description, '')) @@ plainto_tsquery('english', $%d)`, argN)
		args = append(args, f.Search)
	}

	if f.Overdue {
		b.WriteString(` AND t.due_date < CURRENT_DATE AND t.status NOT IN ('done','cancelled')`)
	}

	if f.ClosedSince != nil {
		argN++
		fmt.Fprintf(&b, ` AND t.closed_at >= $%d`, argN)
		args = append(args, *f.ClosedSince)
	}

	b.WriteString(` ORDER BY CASE t.priority WHEN 'blocker' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 END, t.due_date ASC NULLS LAST, t.created_at DESC`)

	limit := f.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	argN++
	fmt.Fprintf(&b, ` LIMIT $%d`, argN)
	args = append(args, limit)

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
	if u.Category != nil {
		argN++
		sets = append(sets, fmt.Sprintf("category = $%d", argN))
		args = append(args, nilIfEmpty(*u.Category))
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

// listCategories returns the distinct non-empty category labels in use across
// a tenant's active tasks, most-used first. Fed to the categorizer so it
// reuses an existing label before coining a new one (keeps the set from
// sprawling) and to the console as the chip's picklist.
func listCategories(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT category FROM app_tasks
		WHERE tenant_id = $1 AND category IS NOT NULL AND category <> ''
		  AND status NOT IN ('done','cancelled')
		GROUP BY category
		ORDER BY count(*) DESC, category ASC
		LIMIT 50`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing categories: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("scanning category: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// setTaskCategory writes a single task's category. Used by the async
// categorizer; a no-op (zero rows) when the task has since been deleted.
func setTaskCategory(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, category string) error {
	if _, err := pool.Exec(ctx,
		`UPDATE app_tasks SET category = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenantID, taskID, nilIfEmpty(category),
	); err != nil {
		return fmt.Errorf("writing category: %w", err)
	}
	return nil
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
