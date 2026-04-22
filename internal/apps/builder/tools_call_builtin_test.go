// Integration tests for the tools_call bridge against a real Postgres +
// the shared MontyEngine built in db_builtins_test.go's TestMain. Each
// test seeds its own tenant, script(s), exposed_tool row, and parent
// script_runs row, then drives a script that invokes tools_call(...).
//
// Shared setup helpers (seedExposedTool, seedParentRun, runToolsCall,
// nilFactory, nilFactoryMatching) live in tools_call_builtin_helpers_test.go.
package builder

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// TestToolsCall_HappyPath exposes a pure-Python echo(msg) and asserts
// the caller script receives the dict back through tools_call.
func TestToolsCall_HappyPath(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	body := `
def echo(msg):
    return {"msg": msg}
`
	tool := seedExposedTool(t, f.pool, f.tenant.ID, f.userID, "echo", "echo", body, []string{"admin"})
	parentRunID := seedParentRun(t, f.pool, f.tenant.ID, tool.scriptID, tool.revisionID, f.userID, TriggerManual)

	var deltas []RunDelta
	bundle := BuildToolsCallBuiltin(
		f.pool, testEngine,
		f.tenant.ID, f.userID,
		[]string{"admin"},
		false,
		&parentRunID,
		func(d RunDelta) { deltas = append(deltas, d) },
		nilFactory,
	)

	src := `
def main():
    r = tools_call("echo", {"msg": "hi"})
    if r["msg"] != "hi":
        raise Exception("unexpected: " + str(r))
    return r["msg"]
`
	result, _, err := runToolsCall(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := result.(string), "hi"; got != want {
		t.Errorf("result = %q, want %q", got, want)
	}
	if len(deltas) != 1 || deltas[0].ChildRuns != 1 {
		t.Errorf("deltas = %+v, want single {ChildRuns:1}", deltas)
	}
}

// TestToolsCall_CrossAppComposition: the caller script lives in app X
// (implicit via the top-level Monty caps), and the exposed tool's
// backing script lives in app Y. The test verifies the child
// script_runs row references a different script_id than the parent,
// and that parent_run_id chains correctly.
func TestToolsCall_CrossAppComposition(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	// App Y: exposed tool returns a dict. App X: the parent caller.
	calleeBody := `
def lookup(tag):
    return {"tag": tag, "hit": True}
`
	callee := seedExposedTool(t, f.pool, f.tenant.ID, f.userID, "lookup_tag", "lookup", calleeBody, []string{"bartender"})

	// Parent app + parent run live in a DIFFERENT builder_app than the
	// exposed tool. We seed a separate script row to own the parent run.
	var parentAppID uuid.UUID
	err := f.pool.QueryRow(ctx, `
		INSERT INTO builder_apps (tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, f.tenant.ID, "parent-"+uuid.NewString()[:8], "parent app", f.userID).Scan(&parentAppID)
	if err != nil {
		t.Fatalf("insert parent app: %v", err)
	}
	var parentScriptID uuid.UUID
	err = f.pool.QueryRow(ctx, `
		INSERT INTO scripts (tenant_id, builder_app_id, name, created_by)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, f.tenant.ID, parentAppID, "parent-script", f.userID).Scan(&parentScriptID)
	if err != nil {
		t.Fatalf("insert parent script: %v", err)
	}
	var parentRev uuid.UUID
	err = f.pool.QueryRow(ctx, `
		INSERT INTO script_revisions (script_id, body, created_by)
		VALUES ($1, 'def main(): pass', $2) RETURNING id
	`, parentScriptID, f.userID).Scan(&parentRev)
	if err != nil {
		t.Fatalf("insert parent rev: %v", err)
	}
	parentRunID := seedParentRun(t, f.pool, f.tenant.ID, parentScriptID, parentRev, f.userID, TriggerManual)

	bundle := BuildToolsCallBuiltin(
		f.pool, testEngine,
		f.tenant.ID, f.userID,
		[]string{"bartender"},
		false,
		&parentRunID,
		nil,
		nilFactory,
	)

	src := `
def main():
    r = tools_call("lookup_tag", {"tag": "ipa"})
    return r["tag"]
`
	result, _, err := runToolsCall(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := result.(string), "ipa"; got != want {
		t.Errorf("result = %q, want %q", got, want)
	}

	// Verify the child run points at the callee script, parent_run_id
	// is set, triggered_by='tools_call', status='completed'.
	var (
		scriptID    uuid.UUID
		parent      *uuid.UUID
		triggeredBy string
		status      string
		duration    *int64
		resultJSON  []byte
	)
	err = f.pool.QueryRow(ctx, `
		SELECT script_id, parent_run_id, triggered_by, status, duration_ms, result
		FROM script_runs
		WHERE tenant_id = $1 AND parent_run_id = $2
	`, f.tenant.ID, parentRunID).Scan(&scriptID, &parent, &triggeredBy, &status, &duration, &resultJSON)
	if err != nil {
		t.Fatalf("select child run: %v", err)
	}
	if scriptID != callee.scriptID {
		t.Errorf("child.script_id = %s, want %s (callee)", scriptID, callee.scriptID)
	}
	if parent == nil || *parent != parentRunID {
		t.Errorf("child.parent_run_id = %v, want %s", parent, parentRunID)
	}
	if triggeredBy != TriggerToolsCall {
		t.Errorf("child.triggered_by = %q, want %q", triggeredBy, TriggerToolsCall)
	}
	if status != RunStatusCompleted {
		t.Errorf("child.status = %q, want %q", status, RunStatusCompleted)
	}
	if duration == nil || *duration < 0 {
		t.Errorf("child.duration_ms = %v, want non-negative", duration)
	}
	var parsed map[string]any
	if err := json.Unmarshal(resultJSON, &parsed); err != nil {
		t.Fatalf("result JSON: %v (%q)", err, string(resultJSON))
	}
	if parsed["tag"] != "ipa" {
		t.Errorf("child.result[tag] = %v, want ipa", parsed["tag"])
	}
}

// TestToolsCall_RoleDenied: caller has bartender but the tool requires
// manager. The bridge refuses without dispatching.
func TestToolsCall_RoleDenied(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	tool := seedExposedTool(t, f.pool, f.tenant.ID, f.userID, "mgr_only",
		"run", "def run(): return {}", []string{"manager"})
	parentRunID := seedParentRun(t, f.pool, f.tenant.ID, tool.scriptID, tool.revisionID, f.userID, TriggerManual)

	bundle := BuildToolsCallBuiltin(
		f.pool, testEngine,
		f.tenant.ID, f.userID,
		[]string{"bartender"},
		false,
		&parentRunID,
		nil,
		nilFactory,
	)

	src := `
def main():
    return tools_call("mgr_only", {})
`
	_, _, err := runToolsCall(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected role-denied error, got nil")
	}
	if !strings.Contains(err.Error(), "not accessible") || !strings.Contains(err.Error(), "mgr_only") {
		t.Errorf("error should mention not-accessible + tool name: %v", err)
	}

	// No child run should have been opened.
	var n int
	err = f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM script_runs WHERE parent_run_id = $1
	`, parentRunID).Scan(&n)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("child run count = %d, want 0", n)
	}
}

// TestToolsCall_AdminNotBypass: admin role doesn't auto-bypass. The
// tool requires "manager"; caller is "admin" only; tools_call refuses.
// Mirrors the plan: visible_to_roles is authoritative for exposed tools.
func TestToolsCall_AdminNotBypass(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	tool := seedExposedTool(t, f.pool, f.tenant.ID, f.userID, "mgr_only2",
		"run", "def run(): return {}", []string{"manager"})
	parentRunID := seedParentRun(t, f.pool, f.tenant.ID, tool.scriptID, tool.revisionID, f.userID, TriggerManual)

	bundle := BuildToolsCallBuiltin(
		f.pool, testEngine,
		f.tenant.ID, f.userID,
		[]string{"admin"},
		false,
		&parentRunID,
		nil,
		nilFactory,
	)
	src := `
def main():
    return tools_call("mgr_only2", {})
`
	_, _, err := runToolsCall(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected role-denied (admin should not auto-bypass), got nil")
	}
	if !strings.Contains(err.Error(), "not accessible") {
		t.Errorf("error should mention not-accessible: %v", err)
	}
}

// TestToolsCall_AdminExplicitGrant: when the exposing admin has included
// "admin" in visible_to_roles, an admin caller succeeds.
func TestToolsCall_AdminExplicitGrant(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	tool := seedExposedTool(t, f.pool, f.tenant.ID, f.userID, "admin_ok",
		"run", "def run(): return {\"ok\": True}", []string{"admin", "manager"})
	parentRunID := seedParentRun(t, f.pool, f.tenant.ID, tool.scriptID, tool.revisionID, f.userID, TriggerManual)

	bundle := BuildToolsCallBuiltin(
		f.pool, testEngine,
		f.tenant.ID, f.userID,
		[]string{"admin"},
		false,
		&parentRunID,
		nil,
		nilFactory,
	)
	src := `
def main():
    r = tools_call("admin_ok", {})
    return r["ok"]
`
	result, _, err := runToolsCall(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, ok := result.(bool); !ok || !got {
		t.Errorf("result = %v (%T), want true", result, result)
	}
}

// TestToolsCall_Stale: a stale exposed_tool row is rejected.
func TestToolsCall_Stale(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	tool := seedExposedTool(t, f.pool, f.tenant.ID, f.userID, "gone",
		"run", "def run(): return {}", []string{"admin"})
	_, err := f.pool.Exec(ctx, `UPDATE exposed_tools SET is_stale = true WHERE tenant_id = $1 AND tool_name = $2`,
		f.tenant.ID, "gone")
	if err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	parentRunID := seedParentRun(t, f.pool, f.tenant.ID, tool.scriptID, tool.revisionID, f.userID, TriggerManual)

	bundle := BuildToolsCallBuiltin(
		f.pool, testEngine,
		f.tenant.ID, f.userID,
		[]string{"admin"},
		false,
		&parentRunID,
		nil,
		nilFactory,
	)
	src := `
def main():
    return tools_call("gone", {})
`
	_, _, err = runToolsCall(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected stale error, got nil")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error should mention stale: %v", err)
	}
}

// TestToolsCall_Nonexistent: unknown tool name returns "tool not found".
func TestToolsCall_Nonexistent(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildToolsCallBuiltin(
		f.pool, testEngine,
		f.tenant.ID, f.userID,
		[]string{"admin"},
		false,
		nil, // no parent run; just asserts nil parent path also works
		nil,
		nilFactory,
	)
	src := `
def main():
    return tools_call("does_not_exist", {})
`
	_, _, err := runToolsCall(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "does_not_exist") {
		t.Errorf("error should name the tool: %v", err)
	}
}

// TestToolsCall_NestingBlocked: tool A's body calls tools_call("B",...).
// We build a child factory that wires tools_call into the child's
// capabilities (pointing at childRunID as the new "parent"). The
// handler's nesting check sees the child's run is triggered_by =
// tools_call and refuses.
func TestToolsCall_NestingBlocked(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	// Tool B — simple identity.
	seedExposedTool(t, f.pool, f.tenant.ID, f.userID, "leaf",
		"run", "def run(): return {\"ok\": True}", []string{"admin"})
	// Tool A — calls B. Its body MUST use tools_call inside.
	aBody := `
def run():
    r = tools_call("leaf", {})
    return r
`
	toolA := seedExposedTool(t, f.pool, f.tenant.ID, f.userID, "wrapper",
		"run", aBody, []string{"admin"})
	parentRunID := seedParentRun(t, f.pool, f.tenant.ID, toolA.scriptID, toolA.revisionID, f.userID, TriggerManual)

	// Child factory wires tools_call into the child's surface using
	// childRunID as the new parent. This is what the production wiring
	// would do to let nested calls attempt to fire — and be refused by
	// the v0.1 one-level cap.
	childFactory := func(
		tenantID, builderAppID, callerUserID uuid.UUID,
		callerRoles []string,
		childRunID uuid.UUID,
	) (map[string]runtime.GoFunc, map[string][]string) {
		nested := BuildToolsCallBuiltin(
			f.pool, testEngine,
			tenantID, callerUserID, callerRoles,
			false,
			&childRunID, // the child's own run becomes the nested-call's parent
			nil,
			nilFactoryMatching(), // stop at one more level (safety)
		)
		return nested.BuiltIns, nested.Params
	}

	bundle := BuildToolsCallBuiltin(
		f.pool, testEngine,
		f.tenant.ID, f.userID,
		[]string{"admin"},
		false,
		&parentRunID,
		nil,
		childFactory,
	)

	src := `
def main():
    return tools_call("wrapper", {})
`
	_, _, err := runToolsCall(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected nesting error, got nil")
	}
	if !strings.Contains(err.Error(), "nested tools_call not supported") {
		t.Errorf("error should mention nesting limit: %v", err)
	}

	// The wrapper's child run should still exist, in status='error'.
	var (
		status string
		errMsg *string
	)
	err = f.pool.QueryRow(ctx, `
		SELECT status, error FROM script_runs
		WHERE tenant_id = $1 AND parent_run_id = $2
	`, f.tenant.ID, parentRunID).Scan(&status, &errMsg)
	if err != nil {
		t.Fatalf("select wrapper child run: %v", err)
	}
	if status != RunStatusError {
		t.Errorf("wrapper child status = %q, want error", status)
	}
	if errMsg == nil || !strings.Contains(*errMsg, "nested") {
		t.Errorf("wrapper child error should mention nested: %v", errMsg)
	}
}
