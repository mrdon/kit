package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

func registerTaskTools(r *Registry, isAdmin bool) {
	r.Register(Def{
		Name:        "create_task",
		Description: "Schedule a recurring task. Kit will run the task description through the full agent at each scheduled time.",
		Schema: propsReq(map[string]any{
			"description": field("string", "What to do each time the task runs (e.g. 'Send a daily sales summary to this channel')"),
			"cron_expr":   field("string", "Cron expression: minute hour day-of-month month day-of-week (e.g. '0 9 * * 1-5' for weekdays at 9am)"),
			"channel_id":  field("string", "Slack channel ID where output should be posted"),
			"scope":       field("string", "Scope: 'user' (default, current user only), 'tenant' (everyone, admin only), or a role name"),
		}, "description", "cron_expr", "channel_id"),
		Handler: handleCreateTask,
	})

	r.Register(Def{
		Name:        "list_tasks",
		Description: "List scheduled tasks visible to the current user.",
		Schema:      props(map[string]any{}),
		Handler:     handleListTasks(isAdmin),
	})

	r.Register(Def{
		Name:        "delete_task",
		Description: "Delete a scheduled task by ID.",
		Schema:      propsReq(map[string]any{"task_id": field("string", "The task UUID")}, "task_id"),
		Handler:     handleDeleteTask,
	})
}

func handleCreateTask(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Description string `json:"description"`
		CronExpr    string `json:"cron_expr"`
		ChannelID   string `json:"channel_id"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}

	scopeType, scopeValue, errMsg := resolveTaskScope(ec, inp.Scope)
	if errMsg != "" {
		return errMsg, nil
	}

	tz := resolveTimezone(ec)

	task, err := models.CreateTask(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID,
		inp.Description, inp.CronExpr, tz, inp.ChannelID, scopeType, scopeValue)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Task created (ID: %s). Next run: %s (%s)",
		task.ID, task.NextRunAt.Format("Mon Jan 2 3:04 PM"), tz), nil
}

func resolveTaskScope(ec *ExecContext, scope string) (scopeType, scopeValue, errMsg string) {
	if scope == "" || scope == "user" {
		return "user", ec.User.SlackUserID, ""
	}
	if scope == "tenant" {
		if !ec.User.IsAdmin {
			return "", "", "Only admins can create tenant-scoped tasks."
		}
		return "tenant", "*", ""
	}

	// Role scope — validate role exists
	exists, err := models.RoleExists(ec.Ctx, ec.Pool, ec.Tenant.ID, scope)
	if err != nil {
		return "", "", fmt.Sprintf("Error checking role: %s", err)
	}
	if !exists {
		return "", "", fmt.Sprintf("Role %q does not exist.", scope)
	}

	// Non-admins must belong to the role
	if !ec.User.IsAdmin {
		userRoles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
		if !slices.Contains(userRoles, scope) {
			return "", "", fmt.Sprintf("You don't have the %q role.", scope)
		}
	}
	return "role", scope, ""
}

func resolveTimezone(ec *ExecContext) string {
	if ec.User.Timezone != "" {
		return ec.User.Timezone
	}
	// Fetch from Slack if not cached
	info, err := ec.Slack.GetUserInfo(ec.Ctx, ec.User.SlackUserID)
	if err == nil && info.Timezone != "" {
		return info.Timezone
	}
	if ec.Tenant.Timezone != "" {
		return ec.Tenant.Timezone
	}
	return "UTC"
}

func handleListTasks(isAdmin bool) HandlerFunc {
	return func(ec *ExecContext, _ json.RawMessage) (string, error) {
		var tasks []models.Task
		var err error

		if isAdmin {
			tasks, err = models.ListAllTenantTasks(ec.Ctx, ec.Pool, ec.Tenant.ID)
		} else {
			userRoles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
			tasks, err = models.ListTasksForContext(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.SlackUserID, userRoles)
		}
		if err != nil {
			return "", err
		}
		if len(tasks) == 0 {
			return "No scheduled tasks.", nil
		}

		var b strings.Builder
		b.WriteString("Scheduled tasks:\n")
		for _, t := range tasks {
			status := t.Status
			if t.LastError != nil {
				status += " (last error: " + *t.LastError + ")"
			}
			next := t.NextRunAt.Format("Mon Jan 2 3:04 PM")
			fmt.Fprintf(&b, "- [%s] %s | cron: `%s` | next: %s | status: %s\n",
				t.ID, t.Description, t.CronExpr, next, status)
		}
		return b.String(), nil
	}
}

func handleDeleteTask(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	taskID, err := uuid.Parse(inp.TaskID)
	if err != nil {
		return "Invalid task ID.", nil
	}

	if !ec.User.IsAdmin {
		// Non-admin: check task is visible via scope filtering
		userRoles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
		visible, err := models.ListTasksForContext(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.SlackUserID, userRoles)
		if err != nil {
			return "", err
		}
		found := false
		for _, t := range visible {
			if t.ID == taskID {
				found = true
				break
			}
		}
		if !found {
			return "Task not found or you don't have permission to delete it.", nil
		}
	} else {
		task, err := models.GetTask(ec.Ctx, ec.Pool, ec.Tenant.ID, taskID)
		if err != nil {
			return "", err
		}
		if task == nil {
			return "Task not found.", nil
		}
	}

	if err := models.DeleteTask(ec.Ctx, ec.Pool, ec.Tenant.ID, taskID); err != nil {
		return "", err
	}
	return "Task deleted.", nil
}
