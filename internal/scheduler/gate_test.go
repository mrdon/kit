package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/models"
)

type gateStub struct{ open atomic.Bool }

func (g *gateStub) Active() bool { return g.open.Load() }

// A closed gate is a superseded build: it must neither claim due work nor
// retire the registrations it doesn't know about, and it picks back up when
// the gate reopens.
func TestClosedGateClaimsAndReconcilesNothing(t *testing.T) {
	f := newRegistryFixture(t)
	RegisterJobRunner(&builtinRunner{s: f.sched})
	t.Cleanup(func() { ClearJobRunnerForTest(string(models.JobTypeBuiltin)) })

	var ran atomic.Int64
	RegisterScheduledTask(ScheduledTask{
		Key: "test.gated", Description: "Gated", DefaultCron: "*/5 * * * *",
		Run: func(context.Context, models.Job) error { ran.Add(1); return nil },
	})
	f.reconcile(t)
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE jobs SET next_run_at = now() - interval '1 minute'
		WHERE tenant_id = $1 AND builtin_key IS NOT NULL
	`, f.tenant.ID); err != nil {
		t.Fatalf("backdating: %v", err)
	}

	gate := &gateStub{}
	f.sched.SetGate(gate)
	if f.sched.active() {
		t.Fatal("closed gate reads as active")
	}

	// An older build's registry lacks this task; a reconcile from it
	// would retire the row.
	ClearScheduledTasksForTest()
	f.sched.reconcileRegistry(f.ctx)
	if row := f.row(t, "test.gated"); row == nil || row.Status != models.JobStatusActive {
		t.Fatalf("closed gate reconciled: row = %+v", row)
	}

	lane := newLaneRunner(f.sched, ExecPolicy{Lane: models.JobLaneFunction, MaxParallel: 2, PollInterval: time.Hour})
	lane.claimAndDispatch(f.ctx, &f.tenant.ID)
	lane.drain()
	if n := ran.Load(); n != 0 {
		t.Fatalf("closed gate ran %d jobs", n)
	}

	RegisterScheduledTask(ScheduledTask{
		Key: "test.gated", Description: "Gated", DefaultCron: "*/5 * * * *",
		Run: func(context.Context, models.Job) error { ran.Add(1); return nil },
	})
	gate.open.Store(true)
	lane.claimAndDispatch(f.ctx, &f.tenant.ID)
	lane.drain()
	if n := ran.Load(); n != 1 {
		t.Fatalf("open gate ran %d jobs, want 1", n)
	}
}
