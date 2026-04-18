package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

type Task struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	CreatedBy   uuid.UUID
	Description string
	CronExpr    string
	Timezone    string
	ChannelID   string
	RunOnce     bool
	TaskType    string // "agent" or "builtin"
	Status      string
	NextRunAt   time.Time
	LastRunAt   *time.Time
	LastError   *string
	CreatedAt   time.Time
}

// NextCronRun computes the next run time for a cron expression in the given timezone.
func NextCronRun(cronExpr, tz string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("loading timezone %q: %w", tz, err)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing cron %q: %w", cronExpr, err)
	}
	return sched.Next(after.In(loc)).UTC(), nil
}

// CreateTask creates a scheduled task with a scope row.
// For recurring tasks, provide cronExpr. For one-time tasks, provide runAt and set runOnce=true.
func CreateTask(ctx context.Context, pool *pgxpool.Pool, tenantID, createdBy uuid.UUID, description, cronExpr, tz, channelID string, runOnce bool, runAt *time.Time, scopeType, scopeValue string) (*Task, error) {
	var nextRun time.Time
	if runOnce && runAt != nil {
		nextRun = runAt.UTC()
	} else {
		var err error
		nextRun, err = NextCronRun(cronExpr, tz, time.Now())
		if err != nil {
			return nil, err
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	task := &Task{}
	taskID := uuid.New()
	err = tx.QueryRow(ctx, `
		INSERT INTO tasks (id, tenant_id, created_by, description, cron_expr, timezone, channel_id, run_once, next_run_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, tenant_id, created_by, description, cron_expr, timezone, channel_id, run_once, task_type, status, next_run_at, last_run_at, last_error, created_at
	`, taskID, tenantID, createdBy, description, cronExpr, tz, channelID, runOnce, nextRun).Scan(
		&task.ID, &task.TenantID, &task.CreatedBy, &task.Description, &task.CronExpr,
		&task.Timezone, &task.ChannelID, &task.RunOnce, &task.TaskType, &task.Status, &task.NextRunAt, &task.LastRunAt, &task.LastError, &task.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO task_scopes (tenant_id, task_id, scope_type, scope_value)
		VALUES ($1, $2, $3, $4)
	`, tenantID, taskID, scopeType, scopeValue)
	if err != nil {
		return nil, fmt.Errorf("creating task scope: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	return task, nil
}

// GetTask returns a single task by ID.
func GetTask(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID) (*Task, error) {
	t := &Task{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, created_by, description, cron_expr, timezone, channel_id, run_once, task_type, status, next_run_at, last_run_at, last_error, created_at
		FROM tasks WHERE tenant_id = $1 AND id = $2
	`, tenantID, taskID).Scan(
		&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
		&t.Timezone, &t.ChannelID, &t.RunOnce, &t.TaskType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting task: %w", err)
	}
	return t, nil
}

// ListTasksForContext returns tasks visible to the user via scope filtering.
func ListTasksForContext(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string, roleNames []string) ([]Task, error) {
	scopeSQL, scopeArgs := ScopeFilter("ts", 2, slackUserID, roleNames)
	args := append([]any{tenantID}, scopeArgs...)
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT t.id, t.tenant_id, t.created_by, t.description, t.cron_expr, t.timezone,
			t.channel_id, t.run_once, t.task_type, t.status, t.next_run_at, t.last_run_at, t.last_error, t.created_at
		FROM tasks t
		JOIN task_scopes ts ON ts.task_id = t.id AND ts.tenant_id = t.tenant_id
		WHERE t.tenant_id = $1
		AND (`+scopeSQL+`)
		ORDER BY t.created_at
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
			&t.Timezone, &t.ChannelID, &t.RunOnce, &t.TaskType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// ListAllTenantTasks returns all tasks for a tenant (admin view, no scope filtering).
func ListAllTenantTasks(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Task, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, created_by, description, cron_expr, timezone,
			channel_id, run_once, task_type, status, next_run_at, last_run_at, last_error, created_at
		FROM tasks WHERE tenant_id = $1
		ORDER BY created_at
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing all tenant tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
			&t.Timezone, &t.ChannelID, &t.RunOnce, &t.TaskType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// UpdateTaskDescription updates a task's description. Builtin tasks cannot be updated.
func UpdateTaskDescription(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, description string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE tasks SET description = $3 WHERE tenant_id = $1 AND id = $2 AND task_type != 'builtin'
	`, tenantID, taskID, description)
	if err != nil {
		return fmt.Errorf("updating task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		t, err := GetTask(ctx, pool, tenantID, taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return errors.New("task not found")
		}
		if t.TaskType == "builtin" {
			return errors.New("builtin tasks cannot be updated")
		}
	}
	return nil
}

// DeleteTask deletes a task and its scope rows (via CASCADE). Builtin tasks cannot be deleted.
func DeleteTask(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM tasks WHERE tenant_id = $1 AND id = $2 AND task_type != 'builtin'`, tenantID, taskID)
	if err != nil {
		return fmt.Errorf("deleting task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either not found or builtin — check which
		t, err := GetTask(ctx, pool, tenantID, taskID)
		if err != nil {
			return err
		}
		if t != nil && t.TaskType == "builtin" {
			return errors.New("builtin tasks cannot be deleted")
		}
	}
	return nil
}

// EnsureBuiltinTask creates a builtin task if it doesn't already exist for the tenant.
// Uses the description as a unique key per tenant (builtin tasks have fixed descriptions).
func EnsureBuiltinTask(ctx context.Context, pool *pgxpool.Pool, tenantID, createdBy uuid.UUID, description, cronExpr, tz string) error {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM tasks WHERE tenant_id = $1 AND task_type = 'builtin' AND description = $2)
	`, tenantID, description).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking builtin task: %w", err)
	}
	if exists {
		return nil
	}

	nextRun, err := NextCronRun(cronExpr, tz, time.Now())
	if err != nil {
		return fmt.Errorf("computing next run: %w", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO tasks (id, tenant_id, created_by, description, cron_expr, timezone, channel_id, task_type, next_run_at)
		VALUES ($1, $2, $3, $4, $5, $6, '', 'builtin', $7)
	`, uuid.New(), tenantID, createdBy, description, cronExpr, tz, nextRun)
	if err != nil {
		return fmt.Errorf("creating builtin task: %w", err)
	}
	return nil
}

// GetDueTasks returns all active tasks across tenants that are due to run.
func GetDueTasks(ctx context.Context, pool *pgxpool.Pool) ([]Task, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, created_by, description, cron_expr, timezone, channel_id, run_once, task_type, status, next_run_at, last_run_at, last_error, created_at
		FROM tasks
		WHERE status = 'active' AND next_run_at <= now()
		ORDER BY next_run_at
	`)
	if err != nil {
		return nil, fmt.Errorf("getting due tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
			&t.Timezone, &t.ChannelID, &t.RunOnce, &t.TaskType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning due task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// CompleteTask marks a one-time task as completed after execution.
func CompleteTask(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, lastError *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE tasks SET last_run_at = now(), status = 'completed', last_error = $3
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, taskID, lastError)
	if err != nil {
		return fmt.Errorf("completing task: %w", err)
	}
	return nil
}

// UpdateTaskAfterRun updates last_run_at, next_run_at, and last_error after execution.
func UpdateTaskAfterRun(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, nextRun time.Time, lastError *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE tasks SET last_run_at = now(), next_run_at = $3, last_error = $4
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, taskID, nextRun, lastError)
	if err != nil {
		return fmt.Errorf("updating task after run: %w", err)
	}
	return nil
}
