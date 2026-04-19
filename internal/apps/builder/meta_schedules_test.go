// Package builder: meta_schedules_test.go drives the Phase 4c schedule
// meta-tools end-to-end. Reuses the scriptFixture from
// meta_scripts_test.go so each test starts with a seeded (tenant, admin,
// app, script) and can jump straight to exercising schedule_script /
// unschedule_script / list_schedules.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// seedScheduleScript builds a script suitable for scheduling tests. Every
// schedule test needs a backing script because the DB enforces the FK,
// so the helper keeps per-test boilerplate focused on the scheduling
// behaviour under test.
func seedScheduleScript(t *testing.T, f *scriptFixture, name string) {
	t.Helper()
	_, err := createScript(context.Background(), f.pool, f.admin, f.app.Name, name, "def tick():\n    return 1\n", "")
	if err != nil {
		t.Fatalf("seed script %s: %v", name, err)
	}
}

// TestScheduleScript_HappyPath verifies schedule_script inserts the row,
// populates next_run_at, and returns a dto the LLM can consume.
func TestScheduleScript_HappyPath(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	seedScheduleScript(t, f, "tick")

	out, err := handleScheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app":      f.app.Name,
		"script":   "tick",
		"fn":       "tick",
		"cron":     "*/5 * * * *",
		"timezone": "UTC",
	}))
	if err != nil {
		t.Fatalf("schedule_script: %v", err)
	}
	var dto scheduleDTO
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if dto.ID == uuid.Nil {
		t.Error("id is nil UUID")
	}
	if dto.Cron != "*/5 * * * *" {
		t.Errorf("cron = %q", dto.Cron)
	}
	if !dto.Active {
		t.Error("active = false; want true")
	}
	if dto.NextRunAt.IsZero() {
		t.Error("next_run_at not set")
	}

	// Verify one row exists with status='active' and created_by pointing at the admin.
	var (
		rowCount  int
		createdBy uuid.UUID
	)
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM tasks
		WHERE tenant_id = $1 AND task_type = 'builder_script' AND status = 'active'
	`, f.tenant.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count row: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("row count = %d, want 1", rowCount)
	}
	if err := f.pool.QueryRow(ctx, `
		SELECT created_by FROM tasks
		WHERE tenant_id = $1 AND task_type = 'builder_script' AND status = 'active'
	`, f.tenant.ID).Scan(&createdBy); err != nil {
		t.Fatalf("fetch created_by: %v", err)
	}
	if createdBy != f.admin.UserID {
		t.Errorf("created_by = %v, want %v", createdBy, f.admin.UserID)
	}
}

// TestScheduleScript_InvalidCron surfaces parse errors so admins know at
// schedule time, not at 03:00 when the tick fails.
func TestScheduleScript_InvalidCron(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	seedScheduleScript(t, f, "tick")

	_, err := handleScheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app":    f.app.Name,
		"script": "tick",
		"fn":     "tick",
		"cron":   "not a real cron",
	}))
	if err == nil {
		t.Fatal("want error for invalid cron")
	}
	if !strings.Contains(err.Error(), "parsing cron") {
		t.Errorf("err = %v, want parse failure", err)
	}

	// No row should have been inserted.
	var n int
	_ = f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM tasks
		WHERE tenant_id = $1 AND task_type = 'builder_script'
	`, f.tenant.ID).Scan(&n)
	if n != 0 {
		t.Errorf("row leaked despite parse error: %d rows", n)
	}
}

// TestScheduleScript_UnknownScript errors cleanly when the script doesn't
// exist under (tenant, app).
func TestScheduleScript_UnknownScript(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()

	_, err := handleScheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app":    f.app.Name,
		"script": "ghost",
		"fn":     "tick",
		"cron":   "*/5 * * * *",
	}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want 'not found'", err)
	}
}

// TestScheduleScript_DuplicateActive rejects a second active schedule for
// the same (script, fn). An inactive row may be revived, but two active
// rows for the same fn would double-fire every tick.
func TestScheduleScript_DuplicateActive(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	seedScheduleScript(t, f, "tick")

	input := mustJSON(map[string]any{
		"app":    f.app.Name,
		"script": "tick",
		"fn":     "tick",
		"cron":   "*/5 * * * *",
	})
	if _, err := handleScheduleScript(f.ec(ctx), input); err != nil {
		t.Fatalf("first schedule: %v", err)
	}
	_, err := handleScheduleScript(f.ec(ctx), input)
	if err == nil || !strings.Contains(err.Error(), "already scheduled") {
		t.Errorf("err = %v, want 'already scheduled'", err)
	}
}

// TestScheduleScript_ReviveInactive re-schedules a row that was previously
// unscheduled. active flips back to true; cron/tz get updated.
func TestScheduleScript_ReviveInactive(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	seedScheduleScript(t, f, "tick")

	if _, err := handleScheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app":    f.app.Name,
		"script": "tick",
		"fn":     "tick",
		"cron":   "*/5 * * * *",
	})); err != nil {
		t.Fatalf("first schedule: %v", err)
	}
	if _, err := handleUnscheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app":    f.app.Name,
		"script": "tick",
		"fn":     "tick",
	})); err != nil {
		t.Fatalf("unschedule: %v", err)
	}

	// Reschedule with a different cron — the ON CONFLICT DO UPDATE should
	// flip active=true and swap in the new cron.
	out, err := handleScheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app":    f.app.Name,
		"script": "tick",
		"fn":     "tick",
		"cron":   "0 12 * * *",
	}))
	if err != nil {
		t.Fatalf("revive: %v", err)
	}
	var dto scheduleDTO
	_ = json.Unmarshal([]byte(out), &dto)
	if !dto.Active {
		t.Error("revived row not active")
	}
	if dto.Cron != "0 12 * * *" {
		t.Errorf("cron = %q, want updated", dto.Cron)
	}
}

// TestUnscheduleScript_FlipsActive confirms the row survives with
// active=false so history is preserved.
func TestUnscheduleScript_FlipsActive(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	seedScheduleScript(t, f, "tick")

	if _, err := handleScheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app": f.app.Name, "script": "tick", "fn": "tick", "cron": "*/5 * * * *",
	})); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if _, err := handleUnscheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app": f.app.Name, "script": "tick", "fn": "tick",
	})); err != nil {
		t.Fatalf("unschedule: %v", err)
	}

	var status string
	if err := f.pool.QueryRow(ctx, `
		SELECT status FROM tasks
		WHERE tenant_id = $1 AND task_type = 'builder_script'
	`, f.tenant.ID).Scan(&status); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "inactive" {
		t.Errorf("status = %q, want inactive after unschedule", status)
	}
}

// TestUnscheduleScript_NotScheduled returns a readable error when there's
// nothing to unschedule.
func TestUnscheduleScript_NotScheduled(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	seedScheduleScript(t, f, "tick")

	_, err := handleUnscheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app": f.app.Name, "script": "tick", "fn": "tick",
	}))
	if err == nil || !strings.Contains(err.Error(), "not scheduled") {
		t.Errorf("err = %v, want 'not scheduled'", err)
	}
}

// TestListSchedules_FilterByApp shows the app filter excludes schedules
// from sibling apps in the same tenant.
func TestListSchedules_FilterByApp(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	seedScheduleScript(t, f, "tick")

	if _, err := handleScheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app": f.app.Name, "script": "tick", "fn": "tick", "cron": "*/5 * * * *",
	})); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	// Second app + scheduled script in the same tenant.
	otherApp, err := createApp(ctx, f.pool, f.admin, "other-"+uuid.NewString()[:4], "")
	if err != nil {
		t.Fatalf("create other app: %v", err)
	}
	if _, err := createScript(ctx, f.pool, f.admin, otherApp.Name, "beat", "def tick(): return 1\n", ""); err != nil {
		t.Fatalf("create other script: %v", err)
	}
	if _, err := handleScheduleScript(f.ec(ctx), mustJSON(map[string]any{
		"app": otherApp.Name, "script": "beat", "fn": "tick", "cron": "0 0 * * *",
	})); err != nil {
		t.Fatalf("schedule other: %v", err)
	}

	// Filtered by f.app: only the "tick" schedule.
	out, err := handleListSchedules(f.ec(ctx), mustJSON(map[string]any{"app": f.app.Name}))
	if err != nil {
		t.Fatalf("list_schedules: %v", err)
	}
	var list []scheduleDTO
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(list) != 1 || list[0].App != f.app.Name {
		t.Errorf("filtered list = %+v, want exactly the first app's schedule", list)
	}

	// Unfiltered: both schedules show up.
	outAll, err := handleListSchedules(f.ec(ctx), mustJSON(map[string]any{}))
	if err != nil {
		t.Fatalf("list_schedules all: %v", err)
	}
	var all []scheduleDTO
	_ = json.Unmarshal([]byte(outAll), &all)
	if len(all) != 2 {
		t.Errorf("unfiltered list len = %d, want 2", len(all))
	}
}

// TestScheduleTools_NonAdminForbidden confirms non-admin callers get
// ErrForbidden across all three tools.
func TestScheduleTools_NonAdminForbidden(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	seedScheduleScript(t, f, "tick")

	nonAdmin := &services.Caller{TenantID: f.tenant.ID, UserID: f.user.ID, IsAdmin: false}
	ec := &execContextLike{Ctx: ctx, Pool: f.pool, Caller: nonAdmin}

	cases := []struct {
		name  string
		fn    func(*execContextLike, json.RawMessage) (string, error)
		input string
	}{
		{"schedule_script", handleScheduleScript, `{"app":"` + f.app.Name + `","script":"tick","fn":"tick","cron":"*/5 * * * *"}`},
		{"unschedule_script", handleUnscheduleScript, `{"app":"` + f.app.Name + `","script":"tick","fn":"tick"}`},
		{"list_schedules", handleListSchedules, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.fn(ec, json.RawMessage(c.input))
			if !errors.Is(err, ErrForbidden) {
				t.Errorf("err = %v, want ErrForbidden", err)
			}
		})
	}
}
