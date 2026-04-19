// Package builder: meta_scripts_rollback_test.go covers the rollback_script_run
// meta-tool end-to-end against Postgres. Split from meta_scripts_test.go to
// keep each file under the 500-LOC cap.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRollback_UpdateAndInsertAndDelete drives the combined case: the
// script inserts 2 new rows, updates 1 pre-existing row, and deletes 1
// pre-existing row. Rollback must restore the pre-run state exactly.
func TestRollback_UpdateAndInsertAndDelete(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	svc := NewItemService(f.pool)
	scope := Scope{TenantID: f.tenant.ID, BuilderAppID: f.app.ID, Collection: "items", CallerUserID: f.user.ID}

	// Seed two pre-existing rows (outside any script run).
	alphaDoc, err := svc.InsertOne(ctx, scope, map[string]any{"label": "alpha", "v": float64(1)})
	if err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	betaDoc, err := svc.InsertOne(ctx, scope, map[string]any{"label": "beta", "v": float64(2)})
	if err != nil {
		t.Fatalf("seed beta: %v", err)
	}
	alphaID := alphaDoc["_id"].(string)
	betaID := betaDoc["_id"].(string)

	body := fmt.Sprintf(`
def main():
    db_insert_one("items", {"label": "gamma"})
    db_insert_one("items", {"label": "delta"})
    db_update_one("items", {"_id": %q}, {"$set": {"v": 999}})
    db_delete_one("items", {"_id": %q})
    return "ok"
`, alphaID, betaID)
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "mutator", body, ""); err != nil {
		t.Fatalf("seed script: %v", err)
	}

	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "mutator", "main", nil, nil)
	if err != nil {
		t.Fatalf("run_script: %v", err)
	}
	if resp.Status != RunStatusCompleted {
		t.Fatalf("status=%q err=%q", resp.Status, resp.Error)
	}

	// Pre-rollback: 3 rows (alpha+gamma+delta; beta deleted).
	if got := countItems(t, ctx, f.pool, f.tenant.ID, f.app.ID); got != 3 {
		t.Errorf("pre-rollback rows = %d, want 3", got)
	}

	rb, err := rollbackScriptRun(ctx, f.pool, f.admin, resp.RunID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", rb.Deleted)
	}
	if rb.Restored != 1 {
		t.Errorf("restored = %d, want 1", rb.Restored)
	}
	if rb.Reinserted != 1 {
		t.Errorf("reinserted = %d, want 1", rb.Reinserted)
	}
	if rb.RolledBack != 4 {
		t.Errorf("rolled_back = %d, want 4", rb.RolledBack)
	}

	// Post-rollback: exactly alpha (v=1) + beta (v=2).
	if got := countItems(t, ctx, f.pool, f.tenant.ID, f.app.ID); got != 2 {
		t.Errorf("post-rollback rows = %d, want 2", got)
	}
	alpha, err := svc.FindOne(ctx, scope, map[string]any{"_id": alphaID})
	if err != nil {
		t.Fatalf("find alpha: %v", err)
	}
	if v, _ := alpha["v"].(float64); v != 1 {
		t.Errorf("alpha.v = %v, want 1", alpha["v"])
	}
	beta, err := svc.FindOne(ctx, scope, map[string]any{"_id": betaID})
	if err != nil {
		t.Fatalf("find beta: %v", err)
	}
	if beta == nil {
		t.Fatal("beta not reinserted")
	}
	if v, _ := beta["v"].(float64); v != 2 {
		t.Errorf("beta.v = %v, want 2", beta["v"])
	}
}

// TestRollback_OnlyDeletes: a script that just deletes rows is the
// smallest case exercising phase 3 (reinsert) in isolation.
func TestRollback_OnlyDeletes(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	svc := NewItemService(f.pool)
	scope := Scope{TenantID: f.tenant.ID, BuilderAppID: f.app.ID, Collection: "things", CallerUserID: f.user.ID}
	doc, err := svc.InsertOne(ctx, scope, map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := doc["_id"].(string)

	body := fmt.Sprintf(`
def main():
    return db_delete_one("things", {"_id": %q})
`, id)
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "deleter", body, ""); err != nil {
		t.Fatalf("seed script: %v", err)
	}

	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "deleter", "main", nil, nil)
	if err != nil {
		t.Fatalf("run_script: %v", err)
	}
	if resp.Status != RunStatusCompleted {
		t.Fatalf("status=%q err=%q", resp.Status, resp.Error)
	}

	rb, err := rollbackScriptRun(ctx, f.pool, f.admin, resp.RunID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Reinserted != 1 {
		t.Errorf("reinserted = %d, want 1", rb.Reinserted)
	}
	back, err := svc.FindOne(ctx, scope, map[string]any{"_id": id})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if back == nil {
		t.Fatal("row not reinserted")
	}
}

// TestRollback_OnlyInserts: a script that only inserts. Phase 1 should
// clean every inserted row on rollback.
func TestRollback_OnlyInserts(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	body := `
def main():
    db_insert_one("widgets", {"n": 1})
    db_insert_one("widgets", {"n": 2})
    db_insert_one("widgets", {"n": 3})
`
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "triplet", body, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, f.app.Name, "triplet", "main", nil, nil)
	if err != nil {
		t.Fatalf("run_script: %v", err)
	}
	if resp.Status != RunStatusCompleted {
		t.Fatalf("status=%q err=%q", resp.Status, resp.Error)
	}

	if got := countItems(t, ctx, f.pool, f.tenant.ID, f.app.ID); got != 3 {
		t.Errorf("pre-rollback = %d, want 3", got)
	}
	rb, err := rollbackScriptRun(ctx, f.pool, f.admin, resp.RunID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Deleted != 3 {
		t.Errorf("deleted = %d, want 3", rb.Deleted)
	}
	if got := countItems(t, ctx, f.pool, f.tenant.ID, f.app.ID); got != 0 {
		t.Errorf("post-rollback = %d, want 0", got)
	}
}

// TestRollback_RequiresConfirm: no confirm=true → ErrMissingConfirm.
func TestRollback_RequiresConfirm(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	_, err := handleRollbackScriptRun(f.ec(ctx), mustJSON(map[string]any{
		"run_id": uuid.New().String(),
	}))
	if !errors.Is(err, ErrMissingConfirm) {
		t.Errorf("err = %v, want ErrMissingConfirm", err)
	}
}

// TestRollback_RunNotFound: unknown run id → clean error.
func TestRollback_RunNotFound(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	_, err := handleRollbackScriptRun(f.ec(ctx), mustJSON(map[string]any{
		"run_id":  uuid.New().String(),
		"confirm": true,
	}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not found", err)
	}
}

// TestRollback_InvalidRunID: non-UUID run_id → clean error.
func TestRollback_InvalidRunID(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	_, err := handleRollbackScriptRun(f.ec(ctx), json.RawMessage(`{"run_id":"not-a-uuid","confirm":true}`))
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Errorf("err = %v, want UUID parse error", err)
	}
}
