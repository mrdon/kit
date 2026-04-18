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

// TaskStatus is the lifecycle state of a task row.
type TaskStatus string

const (
	TaskStatusActive    TaskStatus = "active"    // due to run next
	TaskStatusRunning   TaskStatus = "running"   // claimed by a scheduler, currently executing
	TaskStatusCompleted TaskStatus = "completed" // one-time task that finished
)

// TaskType discriminates between native handlers and full agent runs.
type TaskType string

const (
	TaskTypeAgent   TaskType = "agent"
	TaskTypeBuiltin TaskType = "builtin"
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
	TaskType    TaskType
	Status      TaskStatus
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

// CreateTask creates a scheduled task with a scope row in its own transaction.
// For recurring tasks, provide cronExpr. For one-time tasks, provide runAt and set runOnce=true.
func CreateTask(ctx context.Context, pool *pgxpool.Pool, tenantID, createdBy uuid.UUID, description, cronExpr, tz, channelID string, runOnce bool, runAt *time.Time, scopeType ScopeType, scopeValue string) (*Task, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	task, err := CreateTaskTx(ctx, tx, tenantID, createdBy, description, cronExpr, tz, channelID, runOnce, runAt, scopeType, scopeValue)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	return task, nil
}

// CreateTaskTx inserts a task and its scope row into the supplied transaction.
// The caller is responsible for Begin / Commit / Rollback. Use this when task
// creation must be atomic with other writes (e.g. resolving a decision card).
func CreateTaskTx(ctx context.Context, tx pgx.Tx, tenantID, createdBy uuid.UUID, description, cronExpr, tz, channelID string, runOnce bool, runAt *time.Time, scopeType ScopeType, scopeValue string) (*Task, error) {
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

	task := &Task{}
	taskID := uuid.New()
	err := tx.QueryRow(ctx, `
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
		UPDATE tasks SET description = $3 WHERE tenant_id = $1 AND id = $2 AND task_type != $4
	`, tenantID, taskID, description, TaskTypeBuiltin)
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
		if t.TaskType == TaskTypeBuiltin {
			return errors.New("builtin tasks cannot be updated")
		}
	}
	return nil
}

// DeleteTask deletes a task and its scope rows (via CASCADE). Builtin tasks cannot be deleted.
func DeleteTask(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM tasks WHERE tenant_id = $1 AND id = $2 AND task_type != $3`, tenantID, taskID, TaskTypeBuiltin)
	if err != nil {
		return fmt.Errorf("deleting task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either not found or builtin — check which
		t, err := GetTask(ctx, pool, tenantID, taskID)
		if err != nil {
			return err
		}
		if t != nil && t.TaskType == TaskTypeBuiltin {
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
		SELECT EXISTS(SELECT 1 FROM tasks WHERE tenant_id = $1 AND task_type = $3 AND description = $2)
	`, tenantID, description, TaskTypeBuiltin).Scan(&exists)
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
		VALUES ($1, $2, $3, $4, $5, $6, '', $8, $7)
	`, uuid.New(), tenantID, createdBy, description, cronExpr, tz, nextRun, TaskTypeBuiltin)
	if err != nil {
		return fmt.Errorf("creating builtin task: %w", err)
	}
	return nil
}

// ClaimDueTasks atomically claims up to `limit` active tasks whose
// next_run_at has passed. Claim = flip status from 'active' to 'running'
// under SELECT FOR UPDATE SKIP LOCKED, so multiple scheduler instances
// (e.g. during a rolling deploy) never pick up the same task.
//
// After the returned tasks finish, the caller must set status to
// 'completed' (one-time) or back to 'active' with a new next_run_at
// (recurring) — see CompleteTask / UpdateTaskAfterRun.
func ClaimDueTasks(ctx context.Context, pool *pgxpool.Pool, limit int) ([]Task, error) {
	rows, err := pool.Query(ctx, `
		WITH claimed AS (
			SELECT id FROM tasks
			WHERE status = $2 AND next_run_at <= now()
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE tasks t
		SET status = $3
		FROM claimed c
		WHERE t.id = c.id
		RETURNING t.id, t.tenant_id, t.created_by, t.description, t.cron_expr, t.timezone, t.channel_id, t.run_once, t.task_type, t.status, t.next_run_at, t.last_run_at, t.last_error, t.created_at
	`, limit, TaskStatusActive, TaskStatusRunning)
	if err != nil {
		return nil, fmt.Errorf("claiming due tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
			&t.Timezone, &t.ChannelID, &t.RunOnce, &t.TaskType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning claimed task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// RecoverStuckTasks resets any task stuck in 'running' older than the
// cutoff back to 'active' so another scheduler can re-claim it. Runs at
// scheduler startup to handle crashes where a previous run didn't reach
// CompleteTask / UpdateTaskAfterRun.
func RecoverStuckTasks(ctx context.Context, pool *pgxpool.Pool, olderThan time.Duration) (int64, error) {
	cmd, err := pool.Exec(ctx, `
		UPDATE tasks SET status = $1
		WHERE status = $2 AND (last_run_at IS NULL OR last_run_at < now() - $3::interval)
	`, TaskStatusActive, TaskStatusRunning, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("recovering stuck tasks: %w", err)
	}
	return cmd.RowsAffected(), nil
}

// CompleteTask marks a one-time task as completed after execution.
func CompleteTask(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, lastError *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE tasks SET last_run_at = now(), status = $3, last_error = $4
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, taskID, TaskStatusCompleted, lastError)
	if err != nil {
		return fmt.Errorf("completing task: %w", err)
	}
	return nil
}

// UpdateTaskAfterRun updates last_run_at, next_run_at, and last_error
// after execution. Flips status back to 'active' so the next cron tick
// can re-claim this recurring task.
func UpdateTaskAfterRun(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID, nextRun time.Time, lastError *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE tasks SET status = $5, last_run_at = now(), next_run_at = $3, last_error = $4
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, taskID, nextRun, lastError, TaskStatusActive)
	if err != nil {
		return fmt.Errorf("updating task after run: %w", err)
	}
	return nil
}
