// Package scheduler: lane.go owns the execution pools.
//
// Lanes exist because the reason scheduled work was serialized applies to
// only some of it. Every tenant's agent shares one Anthropic org-wide rate
// limit, so two agent runs racing in the same minute can mutually 429 each
// other on requests that would individually fit. A calendar sync has no such
// constraint and spent years queued behind one anyway.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// ExecPolicy is how one lane is executed.
//
// Policy hangs off the lane rather than the JobRunner because lane is a
// property of the row, not of job_type: a builtin registration that calls
// the LLM has to be serialized alongside agent runs even though its handler
// is native Go. ScheduledTask.LLMBound is what puts it there.
type ExecPolicy struct {
	Lane         models.JobLane
	MaxParallel  int
	PollInterval time.Duration
}

// lanePolicies is the execution plan, one entry per lane.
//
// The agent lane stays at 1 until there is a rate limiter in
// internal/anthropic. Today this number is the only thing bounding LLM
// concurrency, and it never covered interactive Slack or card-chat traffic
// competing for the same org limit — so raising it here would be trading an
// invisible backlog for user-visible 429s.
//
// The function lane is bounded by Postgres and outbound HTTP instead, and
// polls faster so a minute-granularity sweep lands close to its cron minute.
var lanePolicies = []ExecPolicy{
	{Lane: models.JobLaneAgent, MaxParallel: 1, PollInterval: 60 * time.Second},
	{Lane: models.JobLaneFunction, MaxParallel: 6, PollInterval: 15 * time.Second},
}

// laneRunner drives one lane: claim what fits, dispatch it, and get back to
// polling without waiting for it to finish.
//
// The previous loop blocked on every claimed job before polling again, so a
// single slow job stalled its whole lane for the job's full duration —
// which is why claiming and waiting are separated here.
type laneRunner struct {
	s      *Scheduler
	policy ExecPolicy
	kick   chan struct{}

	// inFlight is what makes MaxParallel a concurrency bound rather than a
	// per-poll batch size. Claiming MaxParallel every tick regardless of
	// what is still running would let a slow lane accumulate work without
	// limit.
	inFlight atomic.Int64
	// wg exists so tests can wait for a tick's work to finish. Production
	// never waits.
	wg sync.WaitGroup
}

func newLaneRunner(s *Scheduler, policy ExecPolicy) *laneRunner {
	// Buffered so Kick never blocks; extra kicks coalesce into the pending one.
	return &laneRunner{s: s, policy: policy, kick: make(chan struct{}, 1)}
}

// free reports how many more jobs this lane may start right now.
func (l *laneRunner) free() int {
	return l.policy.MaxParallel - int(l.inFlight.Load())
}

// claimAndDispatch runs one poll. tenantID scopes the claim for tests; nil
// claims across the fleet, which is what production does.
func (l *laneRunner) claimAndDispatch(ctx context.Context, tenantID *uuid.UUID) {
	free := l.free()
	if free <= 0 {
		return
	}

	var (
		jobs []models.Job
		err  error
	)
	if tenantID != nil {
		jobs, err = models.ClaimDueTasksForTenant(ctx, l.s.pool, *tenantID, l.policy.Lane, free)
	} else {
		jobs, err = models.ClaimDueTasks(ctx, l.s.pool, l.policy.Lane, free)
	}
	if err != nil {
		slog.Error("claiming due jobs", "lane", l.policy.Lane, "error", err)
		return
	}

	for i := range jobs {
		l.inFlight.Add(1)
		l.wg.Add(1)
		go func(job models.Job) {
			defer l.wg.Done()
			defer l.inFlight.Add(-1)
			l.s.dispatchTask(ctx, &job)
		}(jobs[i])
	}
}

// drain waits for everything this lane has started. Tests only.
func (l *laneRunner) drain() { l.wg.Wait() }

// run polls until ctx ends.
func (l *laneRunner) run(ctx context.Context) {
	ticker := time.NewTicker(l.policy.PollInterval)
	defer ticker.Stop()

	poll := func() {
		l.claimAndDispatch(ctx, nil)
		// Reset so a kick both runs now AND pushes the next natural tick a
		// full interval out, guaranteeing ≥ PollInterval between polls.
		ticker.Reset(l.policy.PollInterval)
	}

	poll()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		case <-l.kick:
			poll()
		}
	}
}
