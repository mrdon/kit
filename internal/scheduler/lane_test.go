// Package scheduler: lane_test.go pins lane isolation — the reason lanes
// exist. Before them, a single claim loop ordered by next_run_at with LIMIT 1
// meant one long agent run held up every other kind of scheduled work in the
// entire fleet.
package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/models"
)

// TestLaneBoundsConcurrencyNotBatchSize pins what MaxParallel means. Claiming
// MaxParallel rows every poll regardless of what is still running would let a
// lane with slow jobs accumulate work without limit; the in-flight count is
// what makes it a ceiling on concurrent work instead.
func TestLaneBoundsConcurrencyNotBatchSize(t *testing.T) {
	f := newRegistryFixture(t)

	// dispatchTask resolves a runner by job_type; the fixture's bare
	// Scheduler has none registered.
	RegisterJobRunner(&builtinRunner{s: f.sched})
	t.Cleanup(func() { ClearJobRunnerForTest(string(models.JobTypeBuiltin)) })

	var started atomic.Int64
	release := make(chan struct{})
	for _, key := range []string{"test.slow.a", "test.slow.b", "test.slow.c"} {
		RegisterScheduledTask(ScheduledTask{
			Key: key, Description: "Slow " + key, DefaultCron: "*/5 * * * *",
			Run: func(context.Context, models.Job) error {
				started.Add(1)
				<-release
				return nil
			},
		})
	}
	f.reconcile(t)
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE jobs SET next_run_at = now() - interval '1 minute'
		WHERE tenant_id = $1 AND builtin_key IS NOT NULL
	`, f.tenant.ID); err != nil {
		t.Fatalf("backdating: %v", err)
	}

	lane := newLaneRunner(f.sched, ExecPolicy{
		Lane: models.JobLaneFunction, MaxParallel: 2, PollInterval: time.Hour,
	})

	lane.claimAndDispatch(f.ctx, &f.tenant.ID)
	waitFor(t, func() bool { return started.Load() == 2 }, "two jobs to start")

	// A second poll while both slots are occupied must claim nothing.
	lane.claimAndDispatch(f.ctx, &f.tenant.ID)
	if free := lane.free(); free != 0 {
		t.Fatalf("free = %d while at capacity, want 0", free)
	}
	if n := started.Load(); n != 2 {
		t.Fatalf("%d jobs started while MaxParallel is 2 — batch size, not a bound", n)
	}

	close(release)
	lane.drain()

	// With the slots freed, the third row is picked up.
	lane.claimAndDispatch(f.ctx, &f.tenant.ID)
	waitFor(t, func() bool { return started.Load() == 3 }, "the third job to start")
	lane.drain()
}

// waitFor polls cond until it holds or the test times out.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestClaimIsLaneIsolated asserts each lane claims only its own rows, so a
// saturated agent lane is invisible to the function lane.
//
// Claims are tenant-scoped: the production query is global, and a shared test
// Postgres would otherwise hand us other packages' fixtures.
func TestClaimIsLaneIsolated(t *testing.T) {
	f := newRegistryFixture(t)

	// A function-lane row, via the registry.
	RegisterScheduledTask(ScheduledTask{
		Key: "test.lane.function", Description: "Function work",
		DefaultCron: "*/5 * * * *", Run: noopRun,
	})
	// An agent-lane row. LLMBound is what puts native work in the
	// serialized lane, and is the routing most at risk of silently
	// regressing.
	RegisterScheduledTask(ScheduledTask{
		Key: "test.lane.agent", Description: "LLM work",
		DefaultCron: "*/5 * * * *", LLMBound: true, Run: noopRun,
	})
	f.reconcile(t)

	// Both are due now. FirstRegistryRun deliberately pushes a new row's
	// first run past a grace window, so backdate to make them claimable.
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE jobs SET next_run_at = now() - interval '1 minute'
		WHERE tenant_id = $1 AND builtin_key IS NOT NULL
	`, f.tenant.ID); err != nil {
		t.Fatalf("backdating: %v", err)
	}

	agentClaimed, err := models.ClaimDueTasksForTenant(f.ctx, f.pool, f.tenant.ID, models.JobLaneAgent, 10)
	if err != nil {
		t.Fatalf("claiming agent lane: %v", err)
	}
	if len(agentClaimed) != 1 {
		t.Fatalf("agent lane claimed %d rows, want 1", len(agentClaimed))
	}
	if *agentClaimed[0].BuiltinKey != "test.lane.agent" {
		t.Errorf("agent lane claimed %q", *agentClaimed[0].BuiltinKey)
	}

	// The agent lane is now saturated — its row is 'running'. The function
	// lane must be entirely unaffected.
	fnClaimed, err := models.ClaimDueTasksForTenant(f.ctx, f.pool, f.tenant.ID, models.JobLaneFunction, 10)
	if err != nil {
		t.Fatalf("claiming function lane: %v", err)
	}
	if len(fnClaimed) != 1 {
		t.Fatalf("function lane claimed %d rows, want 1 — a busy agent lane must not starve it", len(fnClaimed))
	}
	if *fnClaimed[0].BuiltinKey != "test.lane.function" {
		t.Errorf("function lane claimed %q", *fnClaimed[0].BuiltinKey)
	}
}

// TestClaimStampsClaimedAt guards the stuck-row recovery fix: a row that has
// never completed has last_run_at NULL, so recovery has to age off
// claimed_at or it treats every new row as infinitely stale.
func TestClaimStampsClaimedAt(t *testing.T) {
	f := newRegistryFixture(t)
	RegisterScheduledTask(ScheduledTask{
		Key: "test.claimed.at", Description: "Never run",
		DefaultCron: "*/5 * * * *", Run: noopRun,
	})
	f.reconcile(t)
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE jobs SET next_run_at = now() - interval '1 minute'
		WHERE tenant_id = $1 AND builtin_key = 'test.claimed.at'
	`, f.tenant.ID); err != nil {
		t.Fatalf("backdating: %v", err)
	}

	claimed, err := models.ClaimDueTasksForTenant(f.ctx, f.pool, f.tenant.ID, models.JobLaneFunction, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v (%d rows)", err, len(claimed))
	}
	if claimed[0].ClaimedAt == nil {
		t.Fatal("claimed_at not stamped; recovery would treat this row as stale")
	}
	if claimed[0].LastRunAt != nil {
		t.Fatal("fixture assumption broken: row should never have run")
	}

	// A generous cutoff must leave the just-claimed row alone.
	if _, err := models.RecoverStuckTasks(f.ctx, f.pool, time.Hour); err != nil {
		t.Fatalf("recover: %v", err)
	}
	row := f.row(t, "test.claimed.at")
	if row.Status != models.JobStatusRunning {
		t.Errorf("status = %q, want running — recovery reset a freshly-claimed row", row.Status)
	}
}
