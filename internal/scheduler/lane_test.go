// Package scheduler: lane_test.go pins lane isolation — the reason lanes
// exist. Before them, a single claim loop ordered by next_run_at with LIMIT 1
// meant one long agent run held up every other kind of scheduled work in the
// entire fleet.
package scheduler

import (
	"testing"
	"time"

	"github.com/mrdon/kit/internal/models"
)

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
