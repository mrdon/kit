// Package builder: builder_runner_test.go exercises the scheduler bridge
// end-to-end: given a job_type='builder_script' row with next_run_at in
// the past, running one scheduler tick must claim the row, call the
// builder runner's Run, mutate app_items, write a script_runs audit row
// tagged triggered_by='schedule', and advance next_run_at past now().
//
// Lives in the builder package so the shared testEngine (from
// db_builtins_test.go TestMain) is reusable — standing up a second Monty
// engine just for the scheduler package would add ~5s to every
// `go test ./...` invocation and duplicate bring-up code.
package builder

import (
	"context"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// TestBuilderRunner_Tick exercises the full flow: schedule a script,
// push next_run_at into the past, run one scheduler tick, assert
// app_items row + script_runs audit row land with the right scope.
func TestBuilderRunner_Tick(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })
	runner := builderRunnerForTest(f.pool, deps)

	body := `
def beat():
    db_insert_one("pings", {"at": "tick"})
    return "ok"
`
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "beater", body, ""); err != nil {
		t.Fatalf("create script: %v", err)
	}
	if _, err := scheduleScript(ctx, f.pool, f.admin, f.app.Name, "beater", "beat", "0 * * * *", "UTC"); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	// Push next_run_at into the past so our simulated tick claims it.
	if _, err := f.pool.Exec(ctx, `
		UPDATE jobs SET next_run_at = now() - interval '1 minute'
		WHERE tenant_id = $1 AND job_type = $2
	`, f.tenant.ID, models.JobTypeBuilderScript); err != nil {
		t.Fatalf("backdate next_run_at: %v", err)
	}

	runOneSchedulerTick(t, ctx, f, runner)

	// app_items row exists in the pings collection under f.app.
	var itemCount int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items
		WHERE tenant_id = $1 AND builder_app_id = $2 AND collection = 'pings'
	`, f.tenant.ID, f.app.ID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 1 {
		t.Errorf("pings rows = %d, want 1", itemCount)
	}

	// script_runs row exists with status=completed and triggered_by=schedule.
	var runStatus, triggeredBy string
	if err := f.pool.QueryRow(ctx, `
		SELECT status, triggered_by FROM script_runs
		WHERE tenant_id = $1
		ORDER BY started_at DESC
		LIMIT 1
	`, f.tenant.ID).Scan(&runStatus, &triggeredBy); err != nil {
		t.Fatalf("query script_runs: %v", err)
	}
	if runStatus != RunStatusCompleted {
		t.Errorf("run status = %q, want completed", runStatus)
	}
	if triggeredBy != TriggerSchedule {
		t.Errorf("triggered_by = %q, want %q", triggeredBy, TriggerSchedule)
	}

	// next_run_at advanced past now(), and the job is back to status='active'
	// so the next tick can pick it up again.
	var nextRunAt time.Time
	var status string
	if err := f.pool.QueryRow(ctx, `
		SELECT next_run_at, status FROM jobs
		WHERE tenant_id = $1 AND job_type = $2
	`, f.tenant.ID, models.JobTypeBuilderScript).Scan(&nextRunAt, &status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if !nextRunAt.After(time.Now()) {
		t.Errorf("next_run_at = %v, want after now", nextRunAt)
	}
	if status != string(models.JobStatusActive) {
		t.Errorf("status = %q, want active", status)
	}
}

// TestBuilderRunner_DemotedAdmin verifies the claim-time admin check.
// Unassigning the creator's admin role between schedule time and tick
// time must deactivate the builder_script job row and skip execution.
func TestBuilderRunner_DemotedAdmin(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })
	runner := builderRunnerForTest(f.pool, deps)

	body := "def beat():\n    db_insert_one(\"pings\", {\"at\": \"should_not\"})\n    return 1\n"
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "beater", body, ""); err != nil {
		t.Fatalf("create script: %v", err)
	}
	if _, err := scheduleScript(ctx, f.pool, f.admin, f.app.Name, "beater", "beat", "0 * * * *", "UTC"); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE jobs SET next_run_at = now() - interval '1 minute'
		WHERE tenant_id = $1 AND job_type = $2
	`, f.tenant.ID, models.JobTypeBuilderScript); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// Demote the admin by unassigning the admin role.
	if err := models.UnassignRole(ctx, f.pool, f.tenant.ID, f.admin.UserID, models.RoleAdmin); err != nil {
		t.Fatalf("demote: %v", err)
	}

	runOneSchedulerTick(t, ctx, f, runner)

	// No app_items row should have been written.
	var itemCount int
	_ = f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items
		WHERE tenant_id = $1 AND builder_app_id = $2 AND collection = 'pings'
	`, f.tenant.ID, f.app.ID).Scan(&itemCount)
	if itemCount != 0 {
		t.Errorf("pings written despite demotion: %d rows", itemCount)
	}

	// Job row marked inactive.
	var status string
	if err := f.pool.QueryRow(ctx, `
		SELECT status FROM jobs WHERE tenant_id = $1 AND job_type = $2
	`, f.tenant.ID, models.JobTypeBuilderScript).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != string(models.JobStatusInactive) {
		t.Errorf("status = %q, want inactive", status)
	}
}

// TestBuilderRunner_NoRunnerSkipsWork confirms the scheduler returns the
// claimed job to 'active' when no runner is registered. This matters for
// the bootstrap window during a rolling deploy where the new binary has
// started but the builder's Init hasn't wired the hook yet.
func TestBuilderRunner_NoRunnerSkipsWork(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "silent", "def beat():\n    return 1\n", ""); err != nil {
		t.Fatalf("create script: %v", err)
	}
	if _, err := scheduleScript(ctx, f.pool, f.admin, f.app.Name, "silent", "beat", "0 * * * *", "UTC"); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	// Push into the past, but DO NOT register a runner.
	if _, err := f.pool.Exec(ctx, `
		UPDATE jobs SET next_run_at = now() - interval '1 minute'
		WHERE tenant_id = $1 AND job_type = $2
	`, f.tenant.ID, models.JobTypeBuilderScript); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	scheduler.ClearJobRunnerForTest(string(models.JobTypeBuilderScript))
	s := scheduler.New(f.pool, nil, nil)
	// scheduler.New re-registers agent+builtin runners but we only want
	// to prove the claim loop handles a missing builder runner gracefully.
	scheduler.ClearJobRunnerForTest(string(models.JobTypeBuilderScript))
	s.ProcessDueTasksForTenantForTest(ctx, f.tenant.ID)

	// No script_runs row should exist for this tenant.
	var runCount int
	_ = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM script_runs WHERE tenant_id = $1`, f.tenant.ID).Scan(&runCount)
	if runCount != 0 {
		t.Errorf("script_runs = %d, want 0 (no runner)", runCount)
	}
	// The claimed job should have been returned to active status for a
	// retry once the runner is wired (not wedged in running).
	var status string
	_ = f.pool.QueryRow(ctx, `
		SELECT status FROM jobs WHERE tenant_id = $1 AND job_type = $2
	`, f.tenant.ID, models.JobTypeBuilderScript).Scan(&status)
	if status != string(models.JobStatusActive) {
		t.Errorf("status = %q, want active (returned to claimable)", status)
	}
}

// runOneSchedulerTick drives the scheduler's claim loop against the
// fixture's pool. The scheduler is constructed with nil agent/enc
// because the builder_script path doesn't touch them — it only
// exercises processDueTasks, so we can bypass the full scheduler.Start
// flow.
func runOneSchedulerTick(t *testing.T, ctx context.Context, f *scriptFixture, runner *builderRunner) {
	t.Helper()

	// Register the runner globally for this tick. Restore afterward so
	// parallel tests don't cross-contaminate.
	scheduler.RegisterJobRunner(runner)
	t.Cleanup(func() { scheduler.ClearJobRunnerForTest(string(models.JobTypeBuilderScript)) })

	s := scheduler.New(f.pool, nil, nil)
	s.ProcessDueTasksForTenantForTest(ctx, f.tenant.ID)
}
