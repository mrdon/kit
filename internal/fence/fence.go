// Package fence stops a Kit process from doing background work once a newer
// build is running against the same database.
//
// Dokku replaces a container by starting the new one and retiring the old
// one afterwards, as a separate step. When that step never runs — a deploy
// cut off part way, a reboot at the wrong moment — the old container keeps
// going, untracked by Dokku and so invisible to `ps:report`, and Docker's
// restart policy brings it back after every reboot. The router never sends
// it traffic, so nothing looks wrong, but the scheduler claims work straight
// from the database: one such orphan ran half of every tenant's scheduled
// jobs on two-week-old code, failing each one, and retired builtins it had
// never heard of.
//
// Each process heartbeats a kit_instances row carrying its build stamp. A
// process that sees a live, strictly newer build stops scheduling at once,
// and exits if that is still true after retireAfter. The grace is what keeps
// an ordinary deploy safe: the new container comes up while the old one is
// still the router's target, and a failed healthcheck kills the new one,
// whose heartbeat then lapses and hands the work back.
package fence

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

const (
	heartbeatInterval = 20 * time.Second
	// liveWindow is how stale a heartbeat may be and still count. Several
	// intervals, so one slow beat doesn't hand the work back and forth.
	liveWindow = 90 * time.Second
	// retireAfter is how long a superseded process waits before exiting.
	// Well past Dokku's retire wait, so a container Dokku is about to stop
	// is stopped by Dokku, and only an orphan gets this far.
	retireAfter = 10 * time.Minute
)

// Fence tracks whether this process is still the newest build.
type Fence struct {
	pool    *pgxpool.Pool
	self    models.Instance
	retire  func()
	stale   atomic.Bool
	staleAt time.Time // only touched by the heartbeat goroutine
}

// New builds a fence for this process. builtAt is nil for an unstamped
// build. retire is called once, when the process has been superseded for
// retireAfter; it should shut the process down cleanly with exit status 0,
// so a restart-on-failure policy leaves it down.
func New(pool *pgxpool.Pool, build string, builtAt *time.Time, retire func()) *Fence {
	host, _ := os.Hostname()
	return &Fence{
		pool:   pool,
		self:   models.Instance{ID: uuid.New(), Build: build, BuiltAt: builtAt, Hostname: host},
		retire: retire,
	}
}

// Active reports whether this process should do background work. A nil
// Fence is always active, so tests and tools that build a scheduler without
// one are unaffected.
func (f *Fence) Active() bool {
	return f == nil || !f.stale.Load()
}

// Start records this process and checks for a newer one before returning,
// so the scheduler's startup pass already knows where it stands. It then
// keeps checking until ctx ends.
func (f *Fence) Start(ctx context.Context) {
	f.beat(ctx, time.Now())
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if f.beat(ctx, now) {
					return
				}
			}
		}
	}()
}

// beat heartbeats and re-evaluates. It returns true once retire has been
// called. A database error leaves the current state alone: losing the
// database is no reason to stop, and no evidence of a newer build.
func (f *Fence) beat(ctx context.Context, now time.Time) bool {
	if err := models.HeartbeatInstance(ctx, f.pool, f.self); err != nil {
		slog.Warn("fence heartbeat", "error", err)
		return false
	}
	newer, err := models.NewerLiveInstance(ctx, f.pool, f.self.ID, f.self.BuiltAt, liveWindow)
	if err != nil {
		slog.Warn("fence check", "error", err)
		return false
	}
	return f.observe(newer, now)
}

// observe applies one check's result.
func (f *Fence) observe(newer *models.Instance, now time.Time) bool {
	if newer == nil {
		if f.stale.Swap(false) {
			slog.Warn("newer build gone; resuming background work", "build", f.self.Build)
		}
		return false
	}
	if !f.stale.Swap(true) {
		f.staleAt = now
		slog.Warn("newer build is running; pausing background work",
			"build", f.self.Build, "built_at", f.self.BuiltAt, "host", f.self.Hostname,
			"newer_build", newer.Build, "newer_built_at", newer.BuiltAt, "newer_host", newer.Hostname)
	}
	if now.Sub(f.staleAt) < retireAfter {
		return false
	}
	slog.Error("superseded by a newer build; exiting",
		"build", f.self.Build, "host", f.self.Hostname,
		"newer_build", newer.Build, "newer_host", newer.Hostname)
	f.retire()
	return true
}
