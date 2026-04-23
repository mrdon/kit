// Package builder: meta_diagnostics_test.go exercises the Phase 4e
// diagnostic meta-tools (app_script_logs + app_script_stats) end-to-end against
// Postgres. Each test seeds its own tenant + admin user + app via
// newScriptFixture so tests parallelise without cross-contamination.
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// insertStatsRun inserts a script_runs row with the given status/duration.
// Centralised here because multiple stats tests need to fabricate runs
// without going through app_run_script (which always yields completed runs in
// happy-path scenarios).
func insertStatsRun(
	t *testing.T,
	ctx context.Context,
	f *scriptFixture,
	scriptID, revID uuid.UUID,
	status string,
	durationMs int64,
	startedAgo time.Duration,
) uuid.UUID {
	t.Helper()
	startedInterval := fmt.Sprintf("%d seconds", int(startedAgo.Seconds()))
	durationInterval := fmt.Sprintf("%d milliseconds", durationMs)
	var runID uuid.UUID
	err := f.pool.QueryRow(ctx, `
		INSERT INTO script_runs (
			tenant_id, script_id, revision_id, status, duration_ms,
			started_at, finished_at, triggered_by, caller_user_id
		) VALUES (
			$1, $2, $3, $4, $5,
			now() - $6::interval,
			now() - $6::interval + $7::interval,
			'manual', $8
		)
		RETURNING id
	`, f.tenant.ID, scriptID, revID, status, durationMs,
		startedInterval, durationInterval, f.user.ID).Scan(&runID)
	if err != nil {
		t.Fatalf("insert script_run: %v", err)
	}
	return runID
}

// seedScriptForStats creates a script + revision for stats tests and
// returns both ids so tests can attach runs directly without going
// through app_run_script.
func seedScriptForStats(t *testing.T, ctx context.Context, f *scriptFixture, name string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	dto, err := createScript(ctx, f.pool, f.admin, f.app.Name, name, "def main(): return 1\n", "")
	if err != nil {
		t.Fatalf("createScript %q: %v", name, err)
	}
	if dto.CurrentRevID == nil {
		t.Fatalf("script %q has nil current_rev_id", name)
	}
	return dto.ID, *dto.CurrentRevID
}

// insertLLMCall writes an llm_call_log row attached to a script_run for
// stats aggregation assertions.
func insertLLMCall(
	t *testing.T,
	ctx context.Context,
	f *scriptFixture,
	runID uuid.UUID,
	tokensIn, tokensOut, costCents int,
) {
	t.Helper()
	_, err := f.pool.Exec(ctx, `
		INSERT INTO llm_call_log (tenant_id, script_run_id, fn, model_tier, args_hash,
		                          tokens_in, tokens_out, cost_cents)
		VALUES ($1, $2, 'classify', 'haiku', $3, $4, $5, $6)
	`, f.tenant.ID, runID, "hash-"+uuid.NewString()[:6], tokensIn, tokensOut, costCents)
	if err != nil {
		t.Fatalf("insert llm_call_log: %v", err)
	}
}

// TestScriptLogs_HappyPath runs a script that calls log("info", "hi",
// user="jane") and verifies app_script_logs surfaces the row with fields
// intact.
func TestScriptLogs_HappyPath(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	body := `
def main():
    log("info", "hi", user="jane", count=3)
    return "ok"
`
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "logger", body, ""); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "logger", "main", nil, nil)
	if err != nil {
		t.Fatalf("app_run_script: %v", err)
	}
	if resp.Status != RunStatusCompleted {
		t.Fatalf("status=%q err=%q", resp.Status, resp.Error)
	}

	out, err := handleScriptLogs(f.ec(ctx), mustJSON(map[string]any{
		"run_id": resp.RunID.String(),
	}))
	if err != nil {
		t.Fatalf("app_script_logs: %v", err)
	}
	var rows []scriptLogRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("unmarshal: %v raw=%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1 (rows=%+v)", len(rows), rows)
	}
	if rows[0].Level != "info" {
		t.Errorf("level = %q, want info", rows[0].Level)
	}
	if rows[0].Message != "hi" {
		t.Errorf("message = %q, want hi", rows[0].Message)
	}
	if got, want := rows[0].Fields["user"], "jane"; got != want {
		t.Errorf("fields[user] = %v, want %v", got, want)
	}
	if got, want := rows[0].Fields["count"], float64(3); got != want {
		t.Errorf("fields[count] = %v, want %v", got, want)
	}
}

// TestScriptLogs_TenantIsolation: logs written in tenant A are invisible
// to an admin in tenant B, surfacing as "run not found" rather than an
// empty list (which would leak the existence of the run id).
func TestScriptLogs_TenantIsolation(t *testing.T) {
	fA := newScriptFixture(t)
	fB := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, fA)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	body := `
def main():
    log("info", "secret")
`
	if _, err := createScript(ctx, fA.pool, fA.admin, fA.app.Name, "tenantA_logger", body, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, err := invokeRunScript(ctx, fA.pool, fA.admin, deps, fA.app.Name, "tenantA_logger", "main", nil, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Tenant A: visible.
	if _, err := handleScriptLogs(fA.ec(ctx), mustJSON(map[string]any{
		"run_id": resp.RunID.String(),
	})); err != nil {
		t.Fatalf("tenant A should see own logs: %v", err)
	}

	// Tenant B: "run not found" — not an empty list.
	_, err = handleScriptLogs(fB.ec(ctx), mustJSON(map[string]any{
		"run_id": resp.RunID.String(),
	}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("tenant B err = %v, want not found", err)
	}
}

// TestScriptLogs_Limit inserts 150 rows against a single run and verifies
// a limit=50 call returns exactly 50.
func TestScriptLogs_Limit(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	scriptID, revID := seedScriptForStats(t, ctx, f, "limit_seeder")
	runID := insertStatsRun(t, ctx, f, scriptID, revID, RunStatusCompleted, 10, 1*time.Second)

	for i := range 150 {
		_, err := f.pool.Exec(ctx, `
			INSERT INTO script_logs (tenant_id, script_run_id, level, message)
			VALUES ($1, $2, 'info', $3)
		`, f.tenant.ID, runID, fmt.Sprintf("msg-%d", i))
		if err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	out, err := handleScriptLogs(f.ec(ctx), mustJSON(map[string]any{
		"run_id": runID.String(),
		"limit":  float64(50),
	}))
	if err != nil {
		t.Fatalf("app_script_logs: %v", err)
	}
	var rows []scriptLogRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 50 {
		t.Errorf("len = %d, want 50", len(rows))
	}
}

// TestScriptLogs_AdminOnly: non-admin caller gets ErrForbidden (surfaced
// as the friendly "permission" message via handleScriptLogs' parent).
func TestScriptLogs_AdminOnly(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()

	nonAdmin := &services.Caller{TenantID: f.tenant.ID, UserID: f.user.ID, IsAdmin: false}
	ec := &execContextLike{Ctx: ctx, Pool: f.pool, Caller: nonAdmin}
	_, err := handleScriptLogs(ec, mustJSON(map[string]any{
		"run_id": uuid.New().String(),
	}))
	if err == nil {
		t.Fatal("expected ErrForbidden, got nil")
	}
	if !isForbidden(err) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

// isForbidden reports whether err unwraps to ErrForbidden. Helper because
// errors.Is(err, ErrForbidden) is used across several tests here.
func isForbidden(err error) bool {
	return err != nil && strings.Contains(err.Error(), "admin role required")
}

// TestScriptStats_BasicAggregates: seed a mix of statuses + llm rows
// and assert the aggregate counters.
func TestScriptStats_BasicAggregates(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	scriptID, revID := seedScriptForStats(t, ctx, f, "stats_base")
	r1 := insertStatsRun(t, ctx, f, scriptID, revID, RunStatusCompleted, 100, 1*time.Hour)
	insertStatsRun(t, ctx, f, scriptID, revID, RunStatusCompleted, 200, 2*time.Hour)
	r3 := insertStatsRun(t, ctx, f, scriptID, revID, RunStatusError, 150, 3*time.Hour)
	insertStatsRun(t, ctx, f, scriptID, revID, RunStatusLimitExceeded, 50, 4*time.Hour)
	insertStatsRun(t, ctx, f, scriptID, revID, RunStatusCancelled, 80, 5*time.Hour)

	insertLLMCall(t, ctx, f, r1, 100, 20, 3)
	insertLLMCall(t, ctx, f, r3, 50, 10, 2)

	out, err := handleScriptStats(f.ec(ctx), mustJSON(map[string]any{}))
	if err != nil {
		t.Fatalf("app_script_stats: %v", err)
	}
	var resp scriptStatsResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Completed != 2 {
		t.Errorf("completed = %d, want 2", resp.Completed)
	}
	if resp.Errors != 1 {
		t.Errorf("errors = %d, want 1", resp.Errors)
	}
	if resp.Limits != 1 {
		t.Errorf("limits = %d, want 1", resp.Limits)
	}
	if resp.Cancelled != 1 {
		t.Errorf("cancelled = %d, want 1", resp.Cancelled)
	}
	// AVG across 100/200/150/50/80 = 116.
	if resp.AvgDurationMs < 100 || resp.AvgDurationMs > 130 {
		t.Errorf("avg_duration_ms = %d, want ~116", resp.AvgDurationMs)
	}
	if resp.MaxDurationMs != 200 {
		t.Errorf("max_duration_ms = %d, want 200", resp.MaxDurationMs)
	}
	// Tokens = (100+20) + (50+10) = 180. Cost = 3 + 2 = 5.
	if resp.Tokens != 180 {
		t.Errorf("tokens = %d, want 180", resp.Tokens)
	}
	if resp.CostCents != 5 {
		t.Errorf("cost_cents = %d, want 5", resp.CostCents)
	}
	if resp.Days != scriptStatsDefaultDays {
		t.Errorf("days = %d, want %d", resp.Days, scriptStatsDefaultDays)
	}
	if resp.Scope != "all apps" {
		t.Errorf("scope = %q, want all apps", resp.Scope)
	}
}

// TestScriptStats_AppFilter: two apps in the same tenant, stats filtered
// to one must only count that app's runs.
func TestScriptStats_AppFilter(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Second app in the same tenant.
	other, err := createApp(ctx, f.pool, f.admin, "other-"+uuid.NewString()[:6], "")
	if err != nil {
		t.Fatalf("create other app: %v", err)
	}

	scriptA, revA := seedScriptForStats(t, ctx, f, "in_app_a")
	insertStatsRun(t, ctx, f, scriptA, revA, RunStatusCompleted, 10, 1*time.Hour)
	insertStatsRun(t, ctx, f, scriptA, revA, RunStatusCompleted, 20, 2*time.Hour)

	// Script in the "other" app.
	dto, err := createScript(ctx, f.pool, f.admin, other.Name, "in_app_b", "def main(): return 1\n", "")
	if err != nil {
		t.Fatalf("createScript: %v", err)
	}
	insertStatsRun(t, ctx, f, dto.ID, *dto.CurrentRevID, RunStatusCompleted, 999, 1*time.Hour)

	// Filtering to app A should show only 2 completed runs.
	out, err := handleScriptStats(f.ec(ctx), mustJSON(map[string]any{"app": f.app.Name}))
	if err != nil {
		t.Fatalf("app_script_stats: %v", err)
	}
	var resp scriptStatsResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Completed != 2 {
		t.Errorf("completed (app filter) = %d, want 2", resp.Completed)
	}
	if resp.MaxDurationMs != 20 {
		t.Errorf("max_duration_ms (app filter) = %d, want 20", resp.MaxDurationMs)
	}
	if !strings.Contains(resp.Scope, "app="+f.app.Name) {
		t.Errorf("scope = %q, want contain app=%s", resp.Scope, f.app.Name)
	}

	// Filtering to "other" sees only that one run.
	out2, err := handleScriptStats(f.ec(ctx), mustJSON(map[string]any{"app": other.Name}))
	if err != nil {
		t.Fatalf("app_script_stats other: %v", err)
	}
	var resp2 scriptStatsResponse
	if err := json.Unmarshal([]byte(out2), &resp2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp2.Completed != 1 || resp2.MaxDurationMs != 999 {
		t.Errorf("other app filter = %+v, want completed=1 max=999", resp2)
	}
}

// TestScriptStats_ScriptFilter: within one app, narrowing by script name
// isolates that script's runs.
func TestScriptStats_ScriptFilter(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	script1, rev1 := seedScriptForStats(t, ctx, f, "targeted")
	script2, rev2 := seedScriptForStats(t, ctx, f, "ignored")
	insertStatsRun(t, ctx, f, script1, rev1, RunStatusCompleted, 50, 1*time.Hour)
	insertStatsRun(t, ctx, f, script1, rev1, RunStatusError, 60, 2*time.Hour)
	insertStatsRun(t, ctx, f, script2, rev2, RunStatusCompleted, 77, 1*time.Hour)

	out, err := handleScriptStats(f.ec(ctx), mustJSON(map[string]any{
		"app":    f.app.Name,
		"script": "targeted",
	}))
	if err != nil {
		t.Fatalf("app_script_stats: %v", err)
	}
	var resp scriptStatsResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Completed != 1 {
		t.Errorf("completed = %d, want 1", resp.Completed)
	}
	if resp.Errors != 1 {
		t.Errorf("errors = %d, want 1", resp.Errors)
	}
	if resp.MaxDurationMs != 60 {
		t.Errorf("max_duration_ms = %d, want 60", resp.MaxDurationMs)
	}
	expect := fmt.Sprintf("app=%s/script=targeted", f.app.Name)
	if resp.Scope != expect {
		t.Errorf("scope = %q, want %q", resp.Scope, expect)
	}

	// Script filter without app is an error.
	_, err = handleScriptStats(f.ec(ctx), mustJSON(map[string]any{"script": "targeted"}))
	if err == nil || !strings.Contains(err.Error(), "requires an app") {
		t.Errorf("err = %v, want 'requires an app filter'", err)
	}
}

// TestScriptStats_DaysWindow: a run older than the requested days window
// must not be counted.
func TestScriptStats_DaysWindow(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	scriptID, revID := seedScriptForStats(t, ctx, f, "window_probe")
	// One run 30 days old — outside a 7-day window, inside a 90-day one.
	insertStatsRun(t, ctx, f, scriptID, revID, RunStatusCompleted, 42, 30*24*time.Hour)
	// One run 1 hour old — inside any window.
	insertStatsRun(t, ctx, f, scriptID, revID, RunStatusCompleted, 99, 1*time.Hour)

	// Default (7 days) — excludes the 30-day-old run.
	out, err := handleScriptStats(f.ec(ctx), mustJSON(map[string]any{}))
	if err != nil {
		t.Fatalf("app_script_stats default: %v", err)
	}
	var week scriptStatsResponse
	if err := json.Unmarshal([]byte(out), &week); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if week.Completed != 1 {
		t.Errorf("7-day completed = %d, want 1", week.Completed)
	}
	if week.MaxDurationMs != 99 {
		t.Errorf("7-day max = %d, want 99", week.MaxDurationMs)
	}

	// 90 days — includes both.
	out, err = handleScriptStats(f.ec(ctx), mustJSON(map[string]any{"days": float64(90)}))
	if err != nil {
		t.Fatalf("app_script_stats 90d: %v", err)
	}
	var wide scriptStatsResponse
	if err := json.Unmarshal([]byte(out), &wide); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wide.Completed != 2 {
		t.Errorf("90-day completed = %d, want 2", wide.Completed)
	}
	if wide.Days != 90 {
		t.Errorf("days = %d, want 90", wide.Days)
	}

	// Exceeding the cap gets clamped back to 90.
	out, err = handleScriptStats(f.ec(ctx), mustJSON(map[string]any{"days": float64(5000)}))
	if err != nil {
		t.Fatalf("app_script_stats huge: %v", err)
	}
	var clamped scriptStatsResponse
	if err := json.Unmarshal([]byte(out), &clamped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if clamped.Days != scriptStatsMaxDays {
		t.Errorf("clamped days = %d, want %d", clamped.Days, scriptStatsMaxDays)
	}
}

// TestScriptStats_AdminOnly: non-admin gets forbidden.
func TestScriptStats_AdminOnly(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()

	nonAdmin := &services.Caller{TenantID: f.tenant.ID, UserID: f.user.ID, IsAdmin: false}
	ec := &execContextLike{Ctx: ctx, Pool: f.pool, Caller: nonAdmin}
	_, err := handleScriptStats(ec, mustJSON(map[string]any{}))
	if err == nil {
		t.Fatal("expected ErrForbidden, got nil")
	}
	if !isForbidden(err) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}
