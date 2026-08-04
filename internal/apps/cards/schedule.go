// Package cards: schedule.go declares the app's housekeeping.
//
// These three passes used to hang off scheduler.RegisterPeriodicSweep — a
// hook that fired on every scheduler tick with no schedule of its own, no
// row, and no record that it ran. They are now ordinary registered tasks.
package cards

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// RegisterScheduledTasks declares the card housekeeping passes. Called from
// main.go after the app is initialised, since the sweeps read the package
// singleton's pool.
func RegisterScheduledTasks() {
	// Recovery and expiry are latency-sensitive — a card stuck mid-resolve
	// is a user staring at a spinner — so they run every minute. Both are
	// single indexed statements.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "cards.sweep",
		Description: "Recover stuck cards and archive expired ones",
		DefaultCron: "* * * * *",
		Run: func(ctx context.Context, job models.Job) error {
			if instance == nil || instance.pool == nil {
				return nil
			}
			// Independent passes: errors.Join so one failure doesn't hide
			// the other, and neither stops the other from running.
			return errors.Join(
				SweepStuckResolvingCards(ctx, instance.pool, job.TenantID),
				archiveExpiredCards(ctx, job.TenantID),
			)
		},
	})

	// The purge is a full-table delete and nothing depends on the 90-day
	// boundary landing to the minute, so it runs nightly. It used to carry
	// an in-process 24h throttle to approximate this; a cron expression
	// says it directly and survives restarts.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "cards.purge",
		Description: "Delete archived cards past their retention window",
		DefaultCron: "34 3 * * *",
		Run: func(ctx context.Context, job models.Job) error {
			if instance == nil || instance.pool == nil {
				return nil
			}
			n, err := purgeArchivedCards(ctx, instance.pool, job.TenantID, ArchivedCardRetention)
			if err != nil {
				return err
			}
			if n > 0 {
				slog.Info("purged archived cards", "tenant_id", job.TenantID,
					"count", n, "older_than", ArchivedCardRetention)
			}
			return nil
		},
	})
}

// archiveExpiredCards retires every pending card past its expires_at, so a
// card with a stated shelf life leaves the stack on its own. Cards without
// an expires_at are untouched, which is the default.
func archiveExpiredCards(ctx context.Context, tenantID uuid.UUID) error {
	n, err := sweepExpiredCards(ctx, instance.pool, tenantID)
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("archived expired cards", "tenant_id", tenantID, "count", n)
	}
	return nil
}
