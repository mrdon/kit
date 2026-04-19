// Phase 2g integration tests: fill the gaps not already covered by
// service_items_test.go, service_items_concurrent_test.go, db_builtins_test.go,
// and the runtime mongo_update/filter unit tests. Rather than duplicate
// per-operator coverage that already exists (either end-to-end for $set/$push
// or at the translator level for all 6 operators), this file focuses on:
//
//   - all 6 atomic operators exercised end-to-end via ItemService in one flow,
//     so the temporal trigger is observed firing for $unset/$addToSet/$pull/$inc
//   - builder_apps DELETE should be blocked by ON DELETE RESTRICT when
//     dependent app_items rows exist
//   - INSERT must NOT emit an app_items_history row (trigger is UPDATE OR DELETE)
//
// Reuses newItemFixture/scope/pool helpers from service_items_test.go.
package builder

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestItemService_AllOperators_EndToEnd drives one row through all six
// atomic update operators and verifies (a) the final document matches the
// expected state after each operator and (b) every UPDATE produced a
// history row, i.e. the temporal trigger fires for every operator kind.
func TestItemService_AllOperators_EndToEnd(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	runID := uuid.New()
	s := f.scope("everything")
	s.ScriptRunID = &runID

	ins, err := f.svc.InsertOne(ctx, s, map[string]any{
		"name":    "seed",
		"stale":   "gone-soon",
		"tags":    []any{"a"},
		"members": []any{"x"},
		"count":   1,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id := ins["_id"]
	idFilter := map[string]any{"_id": id}

	// $set
	if _, err := f.svc.UpdateOne(ctx, s, idFilter,
		map[string]any{"$set": map[string]any{"name": "renamed"}}); err != nil {
		t.Fatalf("$set: %v", err)
	}
	// $unset
	if _, err := f.svc.UpdateOne(ctx, s, idFilter,
		map[string]any{"$unset": map[string]any{"stale": ""}}); err != nil {
		t.Fatalf("$unset: %v", err)
	}
	// $push
	if _, err := f.svc.UpdateOne(ctx, s, idFilter,
		map[string]any{"$push": map[string]any{"tags": "b"}}); err != nil {
		t.Fatalf("$push: %v", err)
	}
	// $addToSet — "a" already there, should NOT duplicate
	if _, err := f.svc.UpdateOne(ctx, s, idFilter,
		map[string]any{"$addToSet": map[string]any{"tags": "a"}}); err != nil {
		t.Fatalf("$addToSet (dup): %v", err)
	}
	// $addToSet — "c" is new, should append
	if _, err := f.svc.UpdateOne(ctx, s, idFilter,
		map[string]any{"$addToSet": map[string]any{"tags": "c"}}); err != nil {
		t.Fatalf("$addToSet (new): %v", err)
	}
	// $pull
	if _, err := f.svc.UpdateOne(ctx, s, idFilter,
		map[string]any{"$pull": map[string]any{"members": "x"}}); err != nil {
		t.Fatalf("$pull: %v", err)
	}
	// $inc
	if _, err := f.svc.UpdateOne(ctx, s, idFilter,
		map[string]any{"$inc": map[string]any{"count": 5}}); err != nil {
		t.Fatalf("$inc: %v", err)
	}

	got, err := f.svc.FindOne(ctx, s, idFilter)
	if err != nil {
		t.Fatalf("find after updates: %v", err)
	}
	if got["name"] != "renamed" {
		t.Errorf("name = %v, want renamed", got["name"])
	}
	if _, present := got["stale"]; present {
		t.Errorf("stale should be removed by $unset, got %v", got["stale"])
	}
	tags, ok := got["tags"].([]any)
	if !ok {
		t.Fatalf("tags not []any: %T", got["tags"])
	}
	// Expected: ["a","b","c"] — no duplicate "a".
	seen := map[string]int{}
	for _, v := range tags {
		if s, ok := v.(string); ok {
			seen[s]++
		}
	}
	if seen["a"] != 1 || seen["b"] != 1 || seen["c"] != 1 || len(tags) != 3 {
		t.Errorf("tags = %v, want [a b c] once each", tags)
	}
	members, ok := got["members"].([]any)
	if !ok {
		t.Fatalf("members not []any: %T", got["members"])
	}
	if len(members) != 0 {
		t.Errorf("members after $pull = %v, want empty", members)
	}
	if got["count"].(float64) != 6 {
		t.Errorf("count = %v, want 6", got["count"])
	}

	// 7 UPDATEs were issued; the temporal trigger should have written 7
	// history rows, one per operator invocation.
	rowID, _ := uuid.Parse(id.(string))
	var updateHist int
	err = f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items_history
		WHERE tenant_id = $1 AND id = $2 AND operation = 'UPDATE'
	`, f.tenant.ID, rowID).Scan(&updateHist)
	if err != nil {
		t.Fatalf("history count: %v", err)
	}
	if updateHist != 7 {
		t.Errorf("UPDATE history rows = %d, want 7 (one per operator call)", updateHist)
	}

	// And every history row should carry the script_run_id and caller_user_id
	// that were active at the time of the triggering UPDATE (i.e. the run
	// persisted on the OLD row).
	var runMatches, callerMatches int
	err = f.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE script_run_id = $3),
		  COUNT(*) FILTER (WHERE caller_user_id = $4)
		FROM app_items_history
		WHERE tenant_id = $1 AND id = $2 AND operation = 'UPDATE'
	`, f.tenant.ID, rowID, runID, f.userID).Scan(&runMatches, &callerMatches)
	if err != nil {
		t.Fatalf("provenance query: %v", err)
	}
	if runMatches != 7 || callerMatches != 7 {
		t.Errorf("history provenance: runMatches=%d callerMatches=%d, want 7 each",
			runMatches, callerMatches)
	}
}

// TestItemService_BuilderApp_DeleteRestrict verifies the ON DELETE RESTRICT
// foreign key on app_items.builder_app_id: you cannot delete a builder_apps
// row while items still point to it. This is a guardrail for admins — they
// must empty the app before tearing it down.
func TestItemService_BuilderApp_DeleteRestrict(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()

	// Create at least one app_items row so the FK is exercised.
	if _, err := f.svc.InsertOne(ctx, f.scope("anything"), map[string]any{"k": "v"}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Attempting to delete the builder_apps row must fail with a foreign-key
	// violation because app_items.builder_app_id -> builder_apps.id is
	// ON DELETE RESTRICT.
	_, err := f.pool.Exec(ctx, "DELETE FROM builder_apps WHERE id = $1", f.appID)
	if err == nil {
		t.Fatal("expected FK violation deleting builder_apps with live items, got nil")
	}
	msg := err.Error()
	// Postgres error text for RESTRICT/NO ACTION violations includes the
	// constraint phrasing; any of these spellings satisfies the assertion.
	if !strings.Contains(msg, "violates foreign key constraint") &&
		!strings.Contains(msg, "still referenced") {
		t.Fatalf("error does not look like a RESTRICT violation: %v", err)
	}

	// Builder_apps row should still be there.
	var cnt int
	if err := f.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM builder_apps WHERE id = $1", f.appID).Scan(&cnt); err != nil {
		t.Fatalf("count builder_apps: %v", err)
	}
	if cnt != 1 {
		t.Errorf("builder_apps row count = %d, want 1 (delete should have been blocked)", cnt)
	}

	// And after clearing dependent items, the delete should succeed —
	// confirms the block was strictly about outstanding references.
	if _, err := f.pool.Exec(ctx,
		"DELETE FROM app_items WHERE tenant_id = $1 AND builder_app_id = $2",
		f.tenant.ID, f.appID); err != nil {
		t.Fatalf("clear app_items: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		"DELETE FROM builder_apps WHERE id = $1", f.appID); err != nil {
		t.Fatalf("delete builder_apps after clearing items: %v", err)
	}
}

// TestItemService_InsertDoesNotEmitHistory pins the spec'd trigger behavior:
// app_items_history_trg fires only on UPDATE or DELETE, never on INSERT.
// A fresh row has no prior state worth recording, and emitting a history
// row on insert would double-count the first version in downstream reports.
func TestItemService_InsertDoesNotEmitHistory(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	s := f.scope("fresh")

	ins, err := f.svc.InsertOne(ctx, s, map[string]any{"name": "brandnew"})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	rowID, err := uuid.Parse(ins["_id"].(string))
	if err != nil {
		t.Fatalf("_id not uuid: %v", err)
	}

	var histCount int
	err = f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items_history
		WHERE tenant_id = $1 AND id = $2
	`, f.tenant.ID, rowID).Scan(&histCount)
	if err != nil {
		t.Fatalf("history count: %v", err)
	}
	if histCount != 0 {
		t.Errorf("INSERT produced %d history rows, want 0 — trigger should not fire on INSERT",
			histCount)
	}
}
