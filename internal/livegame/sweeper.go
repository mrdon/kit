package livegame

import (
	"context"
	"log/slog"
	"time"
)

// MinWait floors the sleep so a deadline the database has not quite reached
// yet cannot turn the loop into a spin.
const MinWait = 250 * time.Millisecond

// RetryWait is how long to back off after the next-deadline lookup fails.
const RetryWait = 5 * time.Second

// Sweeper is one game app's deadline loop.
//
// CLAUDE.md CARVE-OUT, considered rather than overlooked. The rule is
// "recurring work is declared with RegisterScheduledTask ... never a goroutine
// ticker", and it exists so tenant-scoped business work has run history,
// last_error and audit. This loop exists because a countdown people are
// watching on a wall needs to end within about a second, and cron's floor is
// a minute.
//
// It is NOT a ticker. It sleeps until the earliest due time anywhere, and
// with no game live -- all week but an hour or so -- it sleeps indefinitely
// and issues no queries at all. Any publish (local or relayed from another
// process) wakes it to re-read the next due time, because every write that
// sets, shortens, extends, pauses or clears a deadline publishes. It re-reads
// from the database on every wake, so it cannot drift from what is stored.
//
// What makes it safe is that the loop owns nothing. All game state is in
// Postgres; Sweep must issue the same guarded conditional write every other
// path issues, so two processes both waking is a no-op for the loser, and a
// crashed process leaves nothing to reconstruct.
type Sweeper struct {
	// Name labels the loop's log lines ("trivia").
	Name string
	// Wake fires on every publish; normally Broker.Changed().
	Wake <-chan struct{}
	// Next reports how long until the earliest due time, or ok=false when
	// nothing anywhere is waiting on the clock.
	Next func(ctx context.Context) (wait time.Duration, ok bool, err error)
	// Sweep does everything that is due now, and publishes what it moved.
	Sweep func(ctx context.Context)
}

// Start runs the loop in its own goroutine until ctx ends.
func (s Sweeper) Start(ctx context.Context) {
	go s.Run(ctx)
}

// Run is the loop itself. One sweep runs at once on entry, which heals
// anything that came due while the process was down.
func (s Sweeper) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.Wake:
		}
		s.Sweep(ctx)
		s.rearm(ctx, timer)
	}
}

// rearm points the timer at the next due time, or parks it when there is
// none. Go 1.23+ timers need no drain before Stop/Reset.
func (s Sweeper) rearm(ctx context.Context, timer *time.Timer) {
	wait, ok, err := s.Next(ctx)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			slog.Warn("live game next-deadline lookup failed", "game", s.Name, "error", err)
		}
		timer.Reset(RetryWait)
	case !ok:
		timer.Stop()
	default:
		timer.Reset(max(wait, MinWait))
	}
}
