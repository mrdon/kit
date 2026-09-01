package squaresales

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// registerScheduledTasks declares the app's recurring work.
//
// All three land in the FUNCTION lane: this is HTTP and SQL, with no model
// call anywhere, so it must not queue behind LLMBound agent work. That is
// the point of the whole design -- the card it produces cannot be skipped
// by a model deciding not to call a tool.
//
// Minute fields are offset from squareshifts' 8/23/38/53 sweep and the
// events reconcile at 4:41 so a tenant's outbound Square calls don't stack.
func (a *App) registerScheduledTasks() {
	// Hourly rather than daily. The card only needs yesterday, but the
	// same rollups answer "how are we doing tonight" mid-service, and an
	// hourly tick means a failed pull retries within the hour instead of
	// leaving a hole in the baseline until tomorrow.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "squaresales.sync",
		Description: "Pull Square sales rollups",
		DefaultCron: "12 * * * *",
		AppliesTo:   a.squareConnected,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunSync(ctx, job.TenantID, "schedule")
			return ignoreUnconfigured(err)
		},
	})

	// Nightly, after close. Card refunds and disputes land days after the
	// sale and silently move a past day's net, which the 3-day incremental
	// window would never revisit -- leaving a wrong number sitting in the
	// baseline every future comparison is measured against.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "squaresales.resettle",
		Description: "Re-pull the last 30 days of Square sales",
		DefaultCron: "27 5 * * *",
		AppliesTo:   a.squareConnected,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunResettle(ctx, job.TenantID)
			return ignoreUnconfigured(err)
		},
	})

	// The card itself, in the slot the retired LLM recap used to occupy.
	// Evaluated in the tenant's timezone by the scheduler.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "squaresales.post_card",
		Description: "Post the daily Square sales card",
		DefaultCron: "0 6 * * *",
		AppliesTo:   a.squareConnected,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunPostCard(ctx, job.TenantID)
			return ignoreUnconfigured(err)
		},
	})
}

// squareConnected reports whether this tenant has the app enabled and a
// Square integration to pull sales from. Cheap by contract -- AppliesTo
// runs for every tenant on every reconcile pass.
func (a *App) squareConnected(ctx context.Context, tenantID uuid.UUID) bool {
	if a.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	var exists bool
	err := a.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM integrations
			WHERE tenant_id = $1 AND provider = 'square'
		)`, tenantID).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// ignoreUnconfigured swallows "nothing wired up here" so it doesn't land in
// last_error; AppliesTo filters most of these, but the integration can be
// removed between the reconcile pass and the run.
//
// ErrMissingScope is deliberately NOT swallowed. A token that can't read
// sales is actionable and someone has to go re-paste one -- burying it
// would leave the card silently absent with a green-looking job.
func ignoreUnconfigured(err error) error {
	if err == nil || errors.Is(err, square.ErrNotConfigured) {
		return nil
	}
	return fmt.Errorf("square sales schedule: %w", err)
}
