// Package voting: schedule.go declares the app's recurring work.
package voting

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// registerScheduledTasks declares the vote sweep.
//
// Every minute, unstaggered — the same expression for every tenant, which is
// unavoidable at minute granularity. AppliesTo keeps the row off tenants with
// no vote running, so in practice very few fire at once.
func (a *VotingApp) registerScheduledTasks() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "voting.sweep",
		Description: "Close votes that have reached their deadline",
		DefaultCron: "* * * * *",
		AppliesTo:   a.hasActiveVotes,
		Run: func(ctx context.Context, job models.Job) error {
			if a.engine == nil {
				return nil
			}
			return a.engine.Tick(ctx, job.TenantID)
		},
	})
}

// hasActiveVotes keeps the every-minute row off tenants with nothing open.
func (a *VotingApp) hasActiveVotes(ctx context.Context, tenantID uuid.UUID) bool {
	if a.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	var exists bool
	err := a.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM app_votes
			WHERE tenant_id = $1 AND status = 'active'
		)`, tenantID).Scan(&exists)
	return err == nil && exists
}
