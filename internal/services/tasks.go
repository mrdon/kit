package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// TaskTools defines the shared tool metadata for task operations.
var TaskTools = []ToolMeta{
	{Name: "create_task", Description: "Schedule a recurring or one-time task. Kit will run the task description through the full agent at the scheduled time.", Schema: propsReq(map[string]any{
		"description": field("string", "What to do when the task runs"),
		"cron_expr":   field("string", "Cron expression for recurring tasks: minute hour day-of-month month day-of-week"),
		"run_at":      field("string", "ISO 8601 datetime for one-time tasks (e.g. '2026-04-05T21:20:00'). Use this OR cron_expr, not both."),
		"channel_id":  field("string", "Slack channel ID where output should be posted"),
		"scope":       field("string", "Scope: 'user' (default), 'tenant' (admin only), or a role name"),
	}, "description")},
	{Name: "list_tasks", Description: "List scheduled tasks visible to the current user.", Schema: props(map[string]any{})},
	{Name: "update_task", Description: "Update or delete a scheduled task. Provide description to change it, or set delete=true to remove the task.", Schema: propsReq(map[string]any{
		"task_id":     field("string", "The task UUID"),
		"description": field("string", "New task description (optional)"),
		"delete":      field("boolean", "Set to true to delete the task (optional)"),
	}, "task_id")},
}

// TaskService handles task operations with authorization.
type TaskService struct {
	pool *pgxpool.Pool
}

// Create creates a scheduled task with scope resolution.
// scope: "user" (default), "tenant" (admin only), or a role name.
func (s *TaskService) Create(ctx context.Context, c *Caller, description, cronExpr, timezone, channelID, scope string, runOnce bool, runAt *time.Time) (*models.Task, error) {
	if scope == "" {
		scope = string(models.ScopeTypeUser)
	}
	var roleID, userID *uuid.UUID
	switch scope {
	case string(models.ScopeTypeUser):
		userID = &c.UserID
	case string(models.ScopeTypeTenant):
		if !c.IsAdmin {
			return nil, ErrForbidden
		}
		// roleID and userID stay nil → tenant-wide
	default:
		if !c.IsAdmin && !hasRole(c, scope) {
			return nil, ErrForbidden
		}
		rid, _, err := resolveScopeTarget(ctx, s.pool, c.TenantID, string(models.ScopeTypeRole), scope)
		if err != nil {
			return nil, err
		}
		roleID = rid
	}
	return models.CreateTask(ctx, s.pool, c.TenantID, c.UserID, description, cronExpr, timezone, channelID, runOnce, runAt, roleID, userID)
}

// List returns tasks visible to the caller. Admins see all tenant tasks.
func (s *TaskService) List(ctx context.Context, c *Caller) ([]models.Task, error) {
	if c.IsAdmin {
		return models.ListAllTenantTasks(ctx, s.pool, c.TenantID)
	}
	return models.ListTasksForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
}

// Update updates a task's description. Admins can update any; non-admins only their visible tasks.
func (s *TaskService) Update(ctx context.Context, c *Caller, taskID uuid.UUID, description string) error {
	if !c.IsAdmin {
		visible, err := models.ListTasksForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
		if err != nil {
			return fmt.Errorf("listing visible tasks: %w", err)
		}
		found := false
		for _, t := range visible {
			if t.ID == taskID {
				found = true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
	}
	return models.UpdateTaskDescription(ctx, s.pool, c.TenantID, taskID, description)
}

// Delete deletes a task. Admins can delete any; non-admins only visible tasks.
func (s *TaskService) Delete(ctx context.Context, c *Caller, taskID uuid.UUID) error {
	if c.IsAdmin {
		task, err := models.GetTask(ctx, s.pool, c.TenantID, taskID)
		if err != nil {
			return fmt.Errorf("getting task: %w", err)
		}
		if task == nil {
			return ErrNotFound
		}
		return models.DeleteTask(ctx, s.pool, c.TenantID, taskID)
	}
	// Non-admin: check task is visible via scope filtering
	visible, err := models.ListTasksForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
	if err != nil {
		return fmt.Errorf("listing visible tasks: %w", err)
	}
	for _, t := range visible {
		if t.ID == taskID {
			return models.DeleteTask(ctx, s.pool, c.TenantID, taskID)
		}
	}
	return ErrNotFound
}
