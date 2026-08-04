// Package scheduler: registry_test.go covers convergence of code-registered
// jobs — the property the old description-keyed builtin mechanism got wrong.
//
// Tests drive reconcileTenantRegistry directly rather than reconcileRegistry:
// the latter sweeps every tenant in the database, which in a shared test
// Postgres means trampling fixtures belonging to other packages.
//
// The registration map is process-global, so these run sequentially.
package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// registryFixture is a throwaway tenant with one user to own system rows.
type registryFixture struct {
	pool   *pgxpool.Pool
	sched  *Scheduler
	tenant models.Tenant
	ctx    context.Context
}

func newRegistryFixture(t *testing.T) *registryFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_reg_" + uuid.NewString()
	slug := models.SanitizeSlug("reg-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "registry-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	if _, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_"+uuid.NewString()[:8], "Registry Tester", ""); err != nil {
		t.Fatalf("creating user: %v", err)
	}

	ClearScheduledTasksForTest()
	t.Cleanup(ClearScheduledTasksForTest)

	return &registryFixture{pool: pool, sched: &Scheduler{pool: pool}, tenant: *tenant, ctx: ctx}
}

// reconcile runs one pass over this fixture's tenant only.
func (f *registryFixture) reconcile(t *testing.T) {
	t.Helper()
	f.sched.reconcileTenantRegistry(f.ctx, f.tenant, snapshotScheduledTasks())
}

// row returns the registry row for a key, or nil.
func (f *registryFixture) row(t *testing.T, key string) *models.Job {
	t.Helper()
	rows, err := models.ListRegistryJobs(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatalf("listing registry jobs: %v", err)
	}
	for i := range rows {
		if rows[i].BuiltinKey != nil && *rows[i].BuiltinKey == key {
			return &rows[i]
		}
	}
	return nil
}

func noopRun(context.Context, models.Job) error { return nil }

// TestRegistryReconcile_CreatesRow is the baseline: a registration with no
// preconditions materialises exactly one active row in the right lane.
func TestRegistryReconcile_CreatesRow(t *testing.T) {
	f := newRegistryFixture(t)
	RegisterScheduledTask(ScheduledTask{
		Key: "test.create", Description: "Create me",
		DefaultCron: "0 4 * * *", Run: noopRun,
	})

	f.reconcile(t)

	got := f.row(t, "test.create")
	if got == nil {
		t.Fatal("expected a row for test.create")
	}
	if got.Status != models.JobStatusActive {
		t.Errorf("status = %q, want active", got.Status)
	}
	if got.Lane != models.JobLaneFunction {
		t.Errorf("lane = %q, want function", got.Lane)
	}
	if got.Description != "Create me" {
		t.Errorf("description = %q", got.Description)
	}
	if !got.IsSystem() {
		t.Error("row should report IsSystem")
	}

	// Idempotent: a second pass must not duplicate. The unique index would
	// reject it, but assert the count so a widened index can't slip by.
	f.reconcile(t)
	rows, err := models.ListRegistryJobs(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after two passes, got %d", len(rows))
	}
}

// TestRegistryReconcile_LLMBoundLane pins the routing that keeps LLM work
// serialized: a builtin registration that calls Anthropic must land in the
// agent lane, not the wide function lane.
func TestRegistryReconcile_LLMBoundLane(t *testing.T) {
	f := newRegistryFixture(t)
	RegisterScheduledTask(ScheduledTask{
		Key: "test.llm", Description: "Talks to Claude",
		DefaultCron: "*/5 * * * *", LLMBound: true, Run: noopRun,
	})

	f.reconcile(t)

	got := f.row(t, "test.llm")
	if got == nil {
		t.Fatal("expected a row for test.llm")
	}
	if got.Lane != models.JobLaneAgent {
		t.Errorf("lane = %q, want agent — LLM-bound work must not run wide", got.Lane)
	}
}

// TestRegistryReconcile_RenameKeepsSchedule is the regression the old
// description-keyed mechanism failed: renaming a task must update the label
// in place, not orphan the row and insert a duplicate — and must not push
// out the next run.
func TestRegistryReconcile_RenameKeepsSchedule(t *testing.T) {
	f := newRegistryFixture(t)
	RegisterScheduledTask(ScheduledTask{
		Key: "test.rename", Description: "Original name",
		DefaultCron: "0 4 * * *", Run: noopRun,
	})
	f.reconcile(t)
	before := f.row(t, "test.rename")
	if before == nil {
		t.Fatal("expected a row")
	}

	RegisterScheduledTask(ScheduledTask{
		Key: "test.rename", Description: "Renamed in code",
		DefaultCron: "0 4 * * *", Run: noopRun,
	})
	f.reconcile(t)

	after := f.row(t, "test.rename")
	if after == nil {
		t.Fatal("row vanished after rename")
	}
	if after.ID != before.ID {
		t.Error("rename created a new row instead of updating in place")
	}
	if after.Description != "Renamed in code" {
		t.Errorf("description = %q, want the new label", after.Description)
	}
	if !after.NextRunAt.Equal(before.NextRunAt) {
		t.Errorf("next_run_at moved on a rename: %v -> %v", before.NextRunAt, after.NextRunAt)
	}
}

// TestRegistryReconcile_CronChangeRecomputes is the other half: changing the
// schedule in code must actually take effect, not wait out the old one.
func TestRegistryReconcile_CronChangeRecomputes(t *testing.T) {
	f := newRegistryFixture(t)
	RegisterScheduledTask(ScheduledTask{
		Key: "test.cron", Description: "Retimed",
		DefaultCron: "0 4 * * *", Run: noopRun,
	})
	f.reconcile(t)
	before := f.row(t, "test.cron")

	RegisterScheduledTask(ScheduledTask{
		Key: "test.cron", Description: "Retimed",
		DefaultCron: "*/10 * * * *", Run: noopRun,
	})
	f.reconcile(t)

	after := f.row(t, "test.cron")
	if after.CronExpr != "*/10 * * * *" {
		t.Errorf("cron_expr = %q, want the new expression", after.CronExpr)
	}
	if !after.NextRunAt.Before(before.NextRunAt) {
		t.Errorf("next_run_at should have moved earlier: %v -> %v", before.NextRunAt, after.NextRunAt)
	}
}

// TestRegistryReconcile_RetireAndRevive covers a tenant falling out of and
// back into scope — app disabled, calendar disconnected, hook removed. The
// row must park rather than vanish, so its history outlives the outage.
func TestRegistryReconcile_RetireAndRevive(t *testing.T) {
	f := newRegistryFixture(t)
	applies := true
	reg := func() {
		RegisterScheduledTask(ScheduledTask{
			Key: "test.conditional", Description: "Conditional",
			DefaultCron: "0 4 * * *", Run: noopRun,
			AppliesTo: func(context.Context, uuid.UUID) bool { return applies },
		})
	}
	reg()
	f.reconcile(t)
	created := f.row(t, "test.conditional")
	if created == nil {
		t.Fatal("expected a row while applicable")
	}

	applies = false
	reg()
	f.reconcile(t)

	retired := f.row(t, "test.conditional")
	if retired == nil {
		t.Fatal("row was deleted; retiring must preserve it for audit")
	}
	if retired.Status != models.JobStatusInactive {
		t.Errorf("status = %q, want inactive", retired.Status)
	}
	if retired.LastError == nil || *retired.LastError != retiredNotApplicable {
		t.Errorf("last_error = %v, want the not-applicable reason", retired.LastError)
	}

	applies = true
	reg()
	f.reconcile(t)

	revived := f.row(t, "test.conditional")
	if revived.Status != models.JobStatusActive {
		t.Errorf("status = %q, want active after revival", revived.Status)
	}
	if revived.LastError != nil {
		t.Errorf("last_error = %v, want cleared on revival", *revived.LastError)
	}
	if revived.ID != created.ID {
		t.Error("revival should reuse the parked row, not mint a new one")
	}
}

// TestRegistryReconcile_RetiresUnregistered covers deleting a registration
// from the code entirely. Previously such a row had no handler and the claim
// loop re-queued it forever.
func TestRegistryReconcile_RetiresUnregistered(t *testing.T) {
	f := newRegistryFixture(t)
	RegisterScheduledTask(ScheduledTask{
		Key: "test.gone", Description: "Soon to be deleted",
		DefaultCron: "0 4 * * *", Run: noopRun,
	})
	f.reconcile(t)
	if f.row(t, "test.gone") == nil {
		t.Fatal("expected a row")
	}

	ClearScheduledTasksForTest()
	f.reconcile(t)

	got := f.row(t, "test.gone")
	if got == nil {
		t.Fatal("row was deleted; an unregistered task should park, not vanish")
	}
	if got.Status != models.JobStatusInactive {
		t.Errorf("status = %q, want inactive", got.Status)
	}
	if got.LastError == nil || *got.LastError != retiredUnregistered {
		t.Errorf("last_error = %v, want the unregistered reason", got.LastError)
	}
}

// TestFirstRegistryRun_GracePeriod pins the rolling-deploy mitigation: a
// frequent job skips a cycle so it can't double-run against the outgoing
// container's ticker, while an infrequent one is not delayed at all.
func TestFirstRegistryRun_GracePeriod(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	everyMinute, err := models.FirstRegistryRun("* * * * *", "UTC", now)
	if err != nil {
		t.Fatalf("every-minute: %v", err)
	}
	if everyMinute.Before(now.Add(5 * time.Minute)) {
		t.Errorf("first run %v is inside the grace window from %v", everyMinute, now)
	}

	// A daily job's next occurrence is already well clear, so it must be
	// taken as-is rather than skipped to the following day.
	daily, err := models.FirstRegistryRun("0 3 * * *", "UTC", now)
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	want := time.Date(2026, 8, 5, 3, 0, 0, 0, time.UTC)
	if !daily.Equal(want) {
		t.Errorf("daily first run = %v, want %v (grace must not skip a whole day)", daily, want)
	}
}
