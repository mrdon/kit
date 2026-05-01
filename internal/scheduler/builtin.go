package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// BuiltinTask defines a job that runs native Go code instead of the LLM agent.
type BuiltinTask struct {
	Description string
	CronExpr    string
	Handler     func(ctx context.Context, s *Scheduler, job models.Job)
}

// builtinTasks is the registry of all builtin jobs.
var builtinTasks = []BuiltinTask{
	{
		Description: "Sync user profiles from Slack",
		CronExpr:    "0 3 * * *",
		Handler: func(ctx context.Context, s *Scheduler, job models.Job) {
			tenant, err := models.GetTenantByID(ctx, s.pool, job.TenantID)
			if err != nil || tenant == nil {
				slog.Error("looking up tenant for profile sync", "error", err)
				return
			}
			s.syncTenantProfiles(ctx, *tenant)
		},
	},
}

// ensureBuiltinTasks creates builtin job rows for all tenants on startup.
func (s *Scheduler) ensureBuiltinTasks(ctx context.Context) {
	tenants, err := models.ListAllTenants(ctx, s.pool)
	if err != nil {
		slog.Error("listing tenants for builtin jobs", "error", err)
		return
	}

	for _, tenant := range tenants {
		// Use any admin user as created_by; fall back to any user if no admin.
		adminUser, err := models.FindAdminUser(ctx, s.pool, tenant.ID)
		if err != nil {
			slog.Warn("finding admin user for tenant builtin jobs", "tenant_id", tenant.ID, "error", err)
			continue
		}
		var adminID uuid.UUID
		if adminUser != nil {
			adminID = adminUser.ID
		} else {
			users, err := models.ListUsersByTenant(ctx, s.pool, tenant.ID)
			if err != nil || len(users) == 0 {
				slog.Warn("no users for tenant, skipping builtin jobs", "tenant_id", tenant.ID)
				continue
			}
			adminID = users[0].ID
		}

		for _, bt := range builtinTasks {
			if err := models.EnsureBuiltinTask(ctx, s.pool, tenant.ID, adminID, bt.Description, bt.CronExpr, tenant.Timezone); err != nil {
				slog.Error("ensuring builtin job", "description", bt.Description, "tenant_id", tenant.ID, "tenant_name", tenant.Name, "error", err)
			} else {
				slog.Info("ensured builtin job", "description", bt.Description, "tenant_id", tenant.ID, "tenant_name", tenant.Name)
			}
		}
	}
}

// findBuiltinHandler returns the handler for a builtin job, matched by description.
func findBuiltinHandler(description string) func(ctx context.Context, s *Scheduler, job models.Job) {
	for _, bt := range builtinTasks {
		if bt.Description == description {
			return bt.Handler
		}
	}
	return nil
}

// ExecuteBuiltinTask runs a builtin job's native handler and updates run metadata.
// Exported so the MCP run_task tool can trigger builtin jobs directly.
func (s *Scheduler) ExecuteBuiltinTask(ctx context.Context, job models.Job) {
	handler := findBuiltinHandler(job.Description)
	if handler == nil {
		slog.Error("no handler for builtin job", "job_id", job.ID, "description", job.Description)
		return
	}

	handler(ctx, s, job)

	nextRun, err := models.NextCronRun(job.CronExpr, job.Timezone, time.Now())
	if err != nil {
		slog.Error("computing next run for builtin job", "job_id", job.ID, "error", err)
		return
	}
	if err := models.UpdateJobAfterRun(ctx, s.pool, job.TenantID, job.ID, nextRun, nil); err != nil {
		slog.Error("updating builtin job after run", "job_id", job.ID, "error", err)
	}
}
