package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/mrdon/kit/internal/models"
)

// BuiltinTask defines a task that runs native Go code instead of the LLM agent.
type BuiltinTask struct {
	Description string
	CronExpr    string
	Handler     func(ctx context.Context, s *Scheduler, task models.Task)
}

// builtinTasks is the registry of all builtin tasks.
var builtinTasks = []BuiltinTask{
	{
		Description: "Sync user profiles from Slack",
		CronExpr:    "0 3 * * *",
		Handler: func(ctx context.Context, s *Scheduler, task models.Task) {
			tenant, err := models.GetTenantByID(ctx, s.pool, task.TenantID)
			if err != nil || tenant == nil {
				slog.Error("looking up tenant for profile sync", "error", err)
				return
			}
			s.syncTenantProfiles(ctx, *tenant)
		},
	},
}

// ensureBuiltinTasks creates builtin task rows for all tenants on startup.
func (s *Scheduler) ensureBuiltinTasks(ctx context.Context) {
	tenants, err := models.ListAllTenants(ctx, s.pool)
	if err != nil {
		slog.Error("listing tenants for builtin tasks", "error", err)
		return
	}

	for _, tenant := range tenants {
		// Find any admin user to use as created_by
		users, err := models.ListUsersByTenant(ctx, s.pool, tenant.ID)
		if err != nil || len(users) == 0 {
			slog.Warn("no users for tenant, skipping builtin tasks", "tenant_id", tenant.ID)
			continue
		}
		var adminID = users[0].ID
		for _, u := range users {
			if u.IsAdmin {
				adminID = u.ID
				break
			}
		}

		for _, bt := range builtinTasks {
			if err := models.EnsureBuiltinTask(ctx, s.pool, tenant.ID, adminID, bt.Description, bt.CronExpr, tenant.Timezone); err != nil {
				slog.Error("ensuring builtin task", "description", bt.Description, "tenant_id", tenant.ID, "tenant_name", tenant.Name, "error", err)
			} else {
				slog.Info("ensured builtin task", "description", bt.Description, "tenant_id", tenant.ID, "tenant_name", tenant.Name)
			}
		}
	}
}

// findBuiltinHandler returns the handler for a builtin task, matched by description.
func findBuiltinHandler(description string) func(ctx context.Context, s *Scheduler, task models.Task) {
	for _, bt := range builtinTasks {
		if bt.Description == description {
			return bt.Handler
		}
	}
	return nil
}

// ExecuteBuiltinTask runs a builtin task's native handler and updates run metadata.
// Exported so the MCP run_task tool can trigger builtin tasks directly.
func (s *Scheduler) ExecuteBuiltinTask(ctx context.Context, task models.Task) {
	handler := findBuiltinHandler(task.Description)
	if handler == nil {
		slog.Error("no handler for builtin task", "task_id", task.ID, "description", task.Description)
		return
	}

	handler(ctx, s, task)

	nextRun, err := models.NextCronRun(task.CronExpr, task.Timezone, time.Now())
	if err != nil {
		slog.Error("computing next run for builtin task", "task_id", task.ID, "error", err)
		return
	}
	if err := models.UpdateTaskAfterRun(ctx, s.pool, task.TenantID, task.ID, nextRun, nil); err != nil {
		slog.Error("updating builtin task after run", "task_id", task.ID, "error", err)
	}
}
