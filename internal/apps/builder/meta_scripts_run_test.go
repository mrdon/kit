// Package builder: meta_scripts_run_test.go covers the app_run_script meta-tool
// + its interactions with the Phase 3 builtin bundles (db_*, llm_*). Split
// from meta_scripts_test.go to keep each file under the 500-LOC cap.
package builder

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// TestRunScript_HappyPath: simple pure-Python script returns an int.
func TestRunScript_HappyPath(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "math", "def add(x, y):\n    return x + y\n", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "math", "add", map[string]any{"x": float64(2), "y": float64(3)}, nil)
	if err != nil {
		t.Fatalf("app_run_script: %v", err)
	}
	if resp.Status != RunStatusCompleted {
		t.Errorf("status = %q, want completed (err=%q)", resp.Status, resp.Error)
	}
	// Monty returns floats for numeric results.
	if fv, ok := resp.Result.(float64); !ok || fv != 5 {
		t.Errorf("result = %v (%T), want 5", resp.Result, resp.Result)
	}
	if resp.MutationSummary["inserts"] != 0 {
		t.Errorf("unexpected mutations: %+v", resp.MutationSummary)
	}
}

// TestRunScript_DBInsert: a script that calls db_insert_one lands a
// row in app_items tagged with the run's id.
func TestRunScript_DBInsert(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	body := `
def main():
    doc = db_insert_one("notes", {"body": "hi"})
    return doc["_id"]
`
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "noter", body, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "noter", "main", nil, nil)
	if err != nil {
		t.Fatalf("app_run_script: %v", err)
	}
	if resp.Status != RunStatusCompleted {
		t.Fatalf("status = %q err=%q", resp.Status, resp.Error)
	}

	// app_items got the row; its script_run_id should equal resp.RunID.
	var n int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items
		WHERE tenant_id = $1 AND builder_app_id = $2 AND script_run_id = $3
	`, f.tenant.ID, f.app.ID, resp.RunID).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if n != 1 {
		t.Errorf("row count = %d, want 1", n)
	}
}

// TestRunScript_LLMTokensTracked: a script that calls llm_classify
// bumps tokens_used and cost_cents on the script_runs row.
func TestRunScript_LLMTokensTracked(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	// Swap in a sender that reports non-trivial usage.
	deps.Sender = &stubSender{respText: "positive", model: "haiku", inTokens: 123, outTokens: 45}
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	body := `
def main():
    label = llm_classify("great product!", ["positive", "negative"])
    return label
`
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "classifier", body, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "classifier", "main", nil, nil)
	if err != nil {
		t.Fatalf("app_run_script: %v", err)
	}
	if resp.Status != RunStatusCompleted {
		t.Fatalf("status=%q err=%q", resp.Status, resp.Error)
	}
	if resp.TokensUsed != 168 { // 123 + 45
		t.Errorf("tokens = %d, want 168", resp.TokensUsed)
	}
	if resp.CostCents <= 0 {
		t.Errorf("cost_cents = %d, want > 0", resp.CostCents)
	}
}

// TestRunScript_ScriptException: script raises; status=error, result nil.
func TestRunScript_ScriptException(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	body := "def main():\n    raise Exception('boom')\n"
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "boomer", body, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "boomer", "main", nil, nil)
	if err != nil {
		t.Fatalf("app_run_script returned Go error: %v", err)
	}
	if resp.Status != RunStatusError {
		t.Errorf("status = %q, want error", resp.Status)
	}
	if resp.Error == "" {
		t.Error("error field empty")
	}
}

// TestRunScript_LimitExceeded: short wall-clock limit + infinite loop
// should classify as limit_exceeded or cancelled.
func TestRunScript_LimitExceeded(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	body := "def main():\n    while True:\n        x = 1\n"
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "looper", body, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "looper", "main", nil,
		map[string]any{"max_duration_ms": float64(250)})
	if err != nil {
		t.Fatalf("app_run_script: %v", err)
	}
	if resp.Status == RunStatusCompleted {
		t.Fatalf("status = completed; expected cancelled/limit_exceeded")
	}
	if resp.Status != RunStatusLimitExceeded && resp.Status != RunStatusCancelled {
		t.Errorf("status = %q, want limit_exceeded or cancelled (err=%q)", resp.Status, resp.Error)
	}
}

// TestRunScript_Concurrent: two goroutines running scripts for the same
// tenant+app should both succeed with independent run_ids.
func TestRunScript_Concurrent(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "concur",
		"def main():\n    return 1\n", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	runs := make([]uuid.UUID, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "concur", "main", nil, nil)
			if err != nil {
				errs[i] = err
				return
			}
			runs[i] = resp.RunID
			if resp.Status != RunStatusCompleted {
				errs[i] = fmt.Errorf("status=%q err=%q", resp.Status, resp.Error)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if runs[0] == runs[1] {
		t.Errorf("concurrent runs shared run_id: %v", runs[0])
	}
}

// scriptTestDeps wires app_run_script's dependencies for tests: the shared
// testEngine, a null-ish sender, and seeds a generous
// tenant_builder_config row so the LLM budget pre-check doesn't
// short-circuit.
func scriptTestDeps(t *testing.T, f *scriptFixture) *scriptRunDeps {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO tenant_builder_config (tenant_id, llm_daily_cent_cap, max_db_calls_per_run)
		VALUES ($1, 10000, 1000)
		ON CONFLICT (tenant_id) DO NOTHING
	`, f.tenant.ID); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return &scriptRunDeps{
		Services:   services.New(f.pool, nil),
		Engine:     testEngine,
		Sender:     &stubSender{respText: "stub", model: "haiku", inTokens: 1, outTokens: 1},
		BuildSlack: nil,
	}
}

// countItems returns the current live row count for a tenant+app.
func countItems(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, appID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items
		WHERE tenant_id = $1 AND builder_app_id = $2
	`, tenantID, appID).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return n
}
