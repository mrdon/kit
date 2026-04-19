// Package builder: acceptance_isolation_test.go holds the Phase 5 task
// 5c app-isolation acceptance test. Split from acceptance_test.go so
// each file stays under the 500-LOC soft cap.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestAcceptance_AppIsolation proves that two apps in the same tenant
// sharing a collection name are isolated: inserts don't mix, list
// returns only the invoking app's rows, delete_app respects RESTRICT,
// and purge_app_data scopes cleanly. Everything drives through the
// meta-tool handlers the same way an admin would via the LLM or MCP.
func TestAcceptance_AppIsolation(t *testing.T) {
	f := newAcceptanceFixture(t, "unused_role")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	deps := acceptanceDeps(t, f, &stubSender{respText: "stub", model: "haiku", inTokens: 1, outTokens: 1})
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	// 1. Two apps sharing a collection name.
	if _, err := handleCreateApp(f.adminEC(ctx), mustJSON(map[string]any{"name": "app_a"})); err != nil {
		t.Fatalf("create app_a: %v", err)
	}
	if _, err := handleCreateApp(f.adminEC(ctx), mustJSON(map[string]any{"name": "app_b"})); err != nil {
		t.Fatalf("create app_b: %v", err)
	}

	scriptBody := "" +
		"def put():\n" +
		"    return db_insert_one(\"items\", {\"source\": SOURCE})\n" +
		"\n" +
		"def list_items():\n" +
		"    return db_find(\"items\", {})\n"
	bodyA := strings.Replace(scriptBody, "SOURCE", `"a"`, 1)
	bodyB := strings.Replace(scriptBody, "SOURCE", `"b"`, 1)

	if _, err := handleCreateScript(f.adminEC(ctx), mustJSON(map[string]any{
		"app": "app_a", "name": "core", "body": bodyA,
	})); err != nil {
		t.Fatalf("create script in app_a: %v", err)
	}
	if _, err := handleCreateScript(f.adminEC(ctx), mustJSON(map[string]any{
		"app": "app_b", "name": "core", "body": bodyB,
	})); err != nil {
		t.Fatalf("create script in app_b: %v", err)
	}

	// 2. Run put() in both apps.
	if _, err := handleRunScript(f.adminEC(ctx), mustJSON(map[string]any{
		"app": "app_a", "script": "core", "fn": "put",
	})); err != nil {
		t.Fatalf("put in app_a: %v", err)
	}
	if _, err := handleRunScript(f.adminEC(ctx), mustJSON(map[string]any{
		"app": "app_b", "script": "core", "fn": "put",
	})); err != nil {
		t.Fatalf("put in app_b: %v", err)
	}

	// 3. list_items() in each app returns only that app's row.
	listAOut, err := handleRunScript(f.adminEC(ctx), mustJSON(map[string]any{
		"app": "app_a", "script": "core", "fn": "list_items",
	}))
	if err != nil {
		t.Fatalf("list app_a: %v", err)
	}
	checkSingleSourceResult(t, "app_a", listAOut, "a")

	listBOut, err := handleRunScript(f.adminEC(ctx), mustJSON(map[string]any{
		"app": "app_b", "script": "core", "fn": "list_items",
	}))
	if err != nil {
		t.Fatalf("list app_b: %v", err)
	}
	checkSingleSourceResult(t, "app_b", listBOut, "b")

	// 4. Verify DB state directly: two rows, one per app, same collection.
	var totalItems int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items WHERE tenant_id = $1 AND collection = 'items'
	`, f.tenant.ID).Scan(&totalItems); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if totalItems != 2 {
		t.Errorf("total items = %d, want 2", totalItems)
	}

	// 5. delete_app on app_a blocked by RESTRICT: items still live.
	_, err = handleDeleteApp(f.adminEC(ctx), mustJSON(map[string]any{"name": "app_a", "confirm": true}))
	if err == nil {
		t.Fatal("expected delete_app(app_a) to fail because of items, got nil")
	}

	// 6. purge_app_data on app_a deletes its row but leaves app_b's.
	purgeOut, err := handlePurgeAppData(f.adminEC(ctx), mustJSON(map[string]any{"name": "app_a", "confirm": true}))
	if err != nil {
		t.Fatalf("purge_app_data(app_a): %v", err)
	}
	if !strings.Contains(purgeOut, `"purged":1`) {
		t.Errorf("purge result = %q, want purged:1", purgeOut)
	}
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items WHERE tenant_id = $1 AND collection = 'items'
	`, f.tenant.ID).Scan(&totalItems); err != nil {
		t.Fatalf("count items post-purge: %v", err)
	}
	if totalItems != 1 {
		t.Errorf("total items after purge = %d, want 1", totalItems)
	}

	// 7. Now delete_app(app_a) succeeds, and app_b survives.
	if _, err := handleDeleteApp(f.adminEC(ctx), mustJSON(map[string]any{"name": "app_a", "confirm": true})); err != nil {
		t.Fatalf("delete_app(app_a) post-purge: %v", err)
	}
	if _, err := loadBuilderAppByName(ctx, f.pool, f.tenant.ID, "app_a"); !errors.Is(err, ErrAppNotFound) {
		t.Errorf("app_a should be gone, got err=%v", err)
	}
	if _, err := loadBuilderAppByName(ctx, f.pool, f.tenant.ID, "app_b"); err != nil {
		t.Errorf("app_b should still exist, got err=%v", err)
	}
	var bCount int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items WHERE tenant_id = $1 AND collection = 'items'
	`, f.tenant.ID).Scan(&bCount); err != nil {
		t.Fatalf("count items final: %v", err)
	}
	if bCount != 1 {
		t.Errorf("app_b item survived? count=%d, want 1", bCount)
	}
}

// checkSingleSourceResult asserts the handleRunScript list_items result
// is a list of exactly one document whose "source" field matches the
// expected value — the core of the app-isolation assertion.
func checkSingleSourceResult(t *testing.T, label, rawResp, wantSource string) {
	t.Helper()
	var resp runScriptResponse
	if err := json.Unmarshal([]byte(rawResp), &resp); err != nil {
		t.Fatalf("%s: parse run resp: %v\nraw=%s", label, err, rawResp)
	}
	if resp.Status != RunStatusCompleted {
		t.Fatalf("%s: status=%q err=%q", label, resp.Status, resp.Error)
	}
	items, ok := resp.Result.([]any)
	if !ok {
		t.Fatalf("%s: result is not []any: %T", label, resp.Result)
	}
	if len(items) != 1 {
		t.Fatalf("%s: result len=%d, want 1", label, len(items))
	}
	row, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("%s: items[0] not map: %T", label, items[0])
	}
	if got, _ := row["source"].(string); got != wantSource {
		t.Errorf("%s: source=%q, want %q", label, got, wantSource)
	}
}
