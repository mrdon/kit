package voting

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Engine is the cron-driven sweep that surfaces the organizer digest
// card on completion or deadline.
type Engine struct {
	pool *pgxpool.Pool
	app  *VotingApp

	now func() time.Time // test hook
}

func newEngine(pool *pgxpool.Pool, app *VotingApp) *Engine {
	return &Engine{pool: pool, app: app, now: time.Now}
}

// Tick runs the two voting sweeps. Cheap when there are no active
// votes — both queries hit indexed paths.
func (e *Engine) Tick(ctx context.Context) error {
	now := e.now()
	if err := e.sweepDeadlines(ctx, now); err != nil {
		slog.Error("vote deadline sweep", "error", err)
	}
	if err := e.sweepCompletion(ctx); err != nil {
		slog.Error("vote completion sweep", "error", err)
	}
	return nil
}

// sweepDeadlines finds active votes whose deadline has passed and
// surfaces the organizer digest card. The card is idempotent — it
// won't be surfaced twice if outcome.tally is already set.
func (e *Engine) sweepDeadlines(ctx context.Context, now time.Time) error {
	votes, err := ListActiveVotes(ctx, e.pool)
	if err != nil {
		return err
	}
	for i := range votes {
		v := votes[i]
		if v.DeadlineAt.After(now) {
			continue
		}
		if err := e.app.surfaceVoteOrganizerCard(ctx, &v); err != nil {
			slog.Error("surfacing vote organizer card on deadline", "error", err, "vote", v.ID)
		}
	}
	return nil
}

// sweepCompletion finds active votes where every participant has
// resolved their card and surfaces the organizer digest card. Same
// idempotency as the deadline path.
func (e *Engine) sweepCompletion(ctx context.Context) error {
	votes, err := ListActiveVotes(ctx, e.pool)
	if err != nil {
		return err
	}
	for i := range votes {
		v := votes[i]
		parts, err := ListParticipants(ctx, e.pool, v.TenantID, v.ID)
		if err != nil {
			slog.Error("listing participants for completion check", "error", err, "vote", v.ID)
			continue
		}
		if !allResolved(parts) {
			continue
		}
		if err := e.app.surfaceVoteOrganizerCard(ctx, &v); err != nil {
			slog.Error("surfacing vote organizer card on completion", "error", err, "vote", v.ID)
		}
	}
	return nil
}

// allResolved returns true if every participant has a verdict set.
// Empty participant list returns false — degenerate but defensive.
func allResolved(parts []Participant) bool {
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if p.Verdict == "" {
			return false
		}
	}
	return true
}
