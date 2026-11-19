package menu

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

// registerScheduledTasks declares the minute check that keeps Square in step
// with happy hour, and the nightly check that Square can sell the menu. Function lane: SQL and HTTP, no model call.
//
// Every minute, because that is how late the register is allowed to be at
// 3pm. It is cheap when nothing changes -- one row read and a comparison --
// and it only calls Square when the state Square should be in has moved.
func (a *App) registerScheduledTasks() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "menu.happy_hour",
		Description: "Keep Square's happy hour discount in step with the schedule",
		DefaultCron: "* * * * *",
		AppliesTo:   a.hasHappyHour,
		Run: func(ctx context.Context, job models.Job) error {
			err := a.reconcileHappyHour(ctx, job.TenantID)
			if errors.Is(err, square.ErrNotConfigured) {
				return nil
			}
			return err
		},
	})
	// Nightly, after close: a drink added to Untappd during service is in
	// Square, the flight and the online shop by opening, and anything that
	// needs a person is a decision card waiting with the morning.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "menu.square_check",
		Description: "Make sure Square can sell everything on the menu: items, flight options and crowlers",
		DefaultCron: "0 4 * * *",
		AppliesTo:   a.followsABoard,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.checkSquare(ctx, job.TenantID, true)
			if errors.Is(err, square.ErrNotConfigured) || errors.Is(err, ErrNotSynced) {
				return nil
			}
			return err
		},
	})
}

// followsABoard is cheap by contract, like hasHappyHour: a menu with a tap
// list to check.
func (a *App) followsABoard(ctx context.Context, tenantID uuid.UUID) bool {
	if a.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	var ok bool
	err := a.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app_menu_boards WHERE tenant_id = $1 AND source_kind <> '')`, tenantID).Scan(&ok)
	return err == nil && ok
}

// hasHappyHour is cheap by contract: AppliesTo runs for every tenant on every
// reconcile pass.
func (a *App) hasHappyHour(ctx context.Context, tenantID uuid.UUID) bool {
	if a.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	var ok bool
	err := a.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app_menu_happy_hour WHERE tenant_id = $1)`, tenantID).Scan(&ok)
	return err == nil && ok
}

// setHappyLive is Start now / End now, for the console and the tool. It saves
// the press, then pushes Square at once rather than waiting for the minute
// check -- whoever pressed it is standing at the bar about to ring a pint.
func (a *App) setHappyLive(ctx context.Context, tenantID uuid.UUID, on bool) (string, bool, error) {
	state, err := LoadHappyHour(ctx, a.pool, tenantID)
	if err != nil {
		return "", false, err
	}
	if !state.Configured || (on && len(state.Config.Beers) == 0) {
		return "", false, fmt.Errorf("%w: set up a happy hour with some beers first", ErrPayloadInvalid)
	}
	if err := SaveHappyLive(ctx, a.pool, tenantID, HappyLive{On: on, At: timeNow()}); err != nil {
		return "", false, err
	}
	if state, err = LoadHappyHour(ctx, a.pool, tenantID); err != nil {
		return "", false, err
	}
	log, ok := a.pushHappyHour(ctx, tenantID, state, false)
	return log, ok, nil
}
