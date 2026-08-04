// Package coordination: schedule.go declares the app's recurring work.
package coordination

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// registerScheduledTasks declares the coordination sweep.
//
// LLMBound: the sweep drafts outreach and interprets replies through the
// Messages API, so it belongs in the serialized lane rather than running
// six-wide alongside IO-bound syncs.
//
// Every minute, unstaggered — it is the same expression for every tenant,
// which is unavoidable at minute granularity. At Kit's tenant count that is
// a handful of index-backed queries landing together, which the function
// lane absorbs; it would want revisiting an order of magnitude larger.
func (a *CoordinationApp) registerScheduledTasks() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "coordination.sweep",
		Description: "Advance in-flight coordinations",
		DefaultCron: "* * * * *",
		LLMBound:    true,
		AppliesTo:   a.hasActiveCoordinations,
		Run: func(ctx context.Context, job models.Job) error {
			if a.engine == nil {
				return nil
			}
			return a.engine.Tick(ctx, job.TenantID)
		},
	})
}

// hasActiveCoordinations keeps the every-minute row off tenants with nothing
// in flight — which is most of them, most of the time.
func (a *CoordinationApp) hasActiveCoordinations(ctx context.Context, tenantID uuid.UUID) bool {
	if a.engine == nil || a.engine.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	var exists bool
	err := a.engine.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM app_coordinations
			WHERE tenant_id = $1 AND status = 'active'
		)`, tenantID).Scan(&exists)
	return err == nil && exists
}
