// Integration tests for the Phase 4d exposed-tool meta-tools. Each test opens
// a tenant via testdb, creates an app + script, and drives the handlers
// end-to-end. The scriptFixture + ec() helpers from meta_scripts_test.go are
// reused so this file stays focused on the expose/revoke/list surface.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// seedExposableScript creates a script with a simple `def lookup(...)` so the
// expose tests have a real function to point at. Returns the script name.
func seedExposableScript(t *testing.T, f *scriptFixture) string {
	t.Helper()
	ctx := context.Background()
	body := "def lookup(name):\n    return {'name': name, 'hit': True}\n"
	_, err := createScript(ctx, f.pool, f.admin, f.app.Name, "lookups", body, "")
	if err != nil {
		t.Fatalf("seed script: %v", err)
	}
	return "lookups"
}

func TestExposeScriptFunctionAsTool_HappyPath(t *testing.T) {
	f := newScriptFixture(t)
	scriptName := seedExposableScript(t, f)
	ctx := context.Background()

	out, err := handleExposeScriptFunctionAsTool(f.ec(ctx), mustJSON(map[string]any{
		"app":              f.app.Name,
		"script":           scriptName,
		"fn_name":          "lookup",
		"tool_name":        "lookup",
		"description":      "Look up a name",
		"args_schema":      map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}},
		"visible_to_roles": []string{"bartender"},
	}))
	if err != nil {
		t.Fatalf("expose: %v", err)
	}
	var dto exposedToolDTO
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse dto: %v\nraw=%s", err, out)
	}
	if dto.ToolName != "lookup" {
		t.Errorf("tool_name = %q, want lookup", dto.ToolName)
	}
	if dto.ScriptName != scriptName {
		t.Errorf("script = %q, want %q", dto.ScriptName, scriptName)
	}
	if dto.IsStale {
		t.Error("newly-exposed tool should not be stale")
	}
	if len(dto.VisibleToRoles) != 1 || dto.VisibleToRoles[0] != "bartender" {
		t.Errorf("visible_to_roles = %v, want [bartender]", dto.VisibleToRoles)
	}
}

func TestExposeScriptFunctionAsTool_DuplicateToolName(t *testing.T) {
	f := newScriptFixture(t)
	scriptName := seedExposableScript(t, f)
	ctx := context.Background()

	input := mustJSON(map[string]any{
		"app":       f.app.Name,
		"script":    scriptName,
		"fn_name":   "lookup",
		"tool_name": "dup",
	})
	if _, err := handleExposeScriptFunctionAsTool(f.ec(ctx), input); err != nil {
		t.Fatalf("first expose: %v", err)
	}
	_, err := handleExposeScriptFunctionAsTool(f.ec(ctx), input)
	if err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want 'already exists'", err)
	}
}

func TestExposeScriptFunctionAsTool_NonAdminForbidden(t *testing.T) {
	f := newScriptFixture(t)
	scriptName := seedExposableScript(t, f)
	ctx := context.Background()

	nonAdmin := &services.Caller{TenantID: f.tenant.ID, UserID: f.user.ID, IsAdmin: false}
	ec := &execContextLike{Ctx: ctx, Pool: f.pool, Caller: nonAdmin}

	_, err := handleExposeScriptFunctionAsTool(ec, mustJSON(map[string]any{
		"app": f.app.Name, "script": scriptName, "fn_name": "lookup", "tool_name": "x",
	}))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestRevokeExposedTool_HappyAndMissing(t *testing.T) {
	f := newScriptFixture(t)
	scriptName := seedExposableScript(t, f)
	ctx := context.Background()

	if _, err := handleExposeScriptFunctionAsTool(f.ec(ctx), mustJSON(map[string]any{
		"app": f.app.Name, "script": scriptName, "fn_name": "lookup", "tool_name": "r",
	})); err != nil {
		t.Fatalf("expose: %v", err)
	}

	out, err := handleRevokeExposedTool(f.ec(ctx), mustJSON(map[string]any{"tool_name": "r"}))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !strings.Contains(out, `"revoked":"r"`) {
		t.Errorf("result = %q, want revoked:r", out)
	}

	// Second revoke fails — row already gone.
	_, err = handleRevokeExposedTool(f.ec(ctx), mustJSON(map[string]any{"tool_name": "r"}))
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}

func TestListExposedTools_FilterByApp(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	scriptName := seedExposableScript(t, f)

	// Second app + script so we can confirm the filter.
	app2, err := createApp(ctx, f.pool, f.admin, "app2-"+uuid.NewString()[:6], "")
	if err != nil {
		t.Fatalf("create app2: %v", err)
	}
	if _, err := createScript(ctx, f.pool, f.admin, app2.Name, "other", "def lookup(n):\n    return n\n", ""); err != nil {
		t.Fatalf("create script2: %v", err)
	}

	if _, err := handleExposeScriptFunctionAsTool(f.ec(ctx), mustJSON(map[string]any{
		"app": f.app.Name, "script": scriptName, "fn_name": "lookup", "tool_name": "t1",
	})); err != nil {
		t.Fatalf("expose t1: %v", err)
	}
	if _, err := handleExposeScriptFunctionAsTool(f.ec(ctx), mustJSON(map[string]any{
		"app": app2.Name, "script": "other", "fn_name": "lookup", "tool_name": "t2",
	})); err != nil {
		t.Fatalf("expose t2: %v", err)
	}

	// No filter: both tools returned.
	out, err := handleListExposedTools(f.ec(ctx), mustJSON(map[string]any{}))
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	var all []exposedToolDTO
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		t.Fatalf("parse all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d tools, want 2", len(all))
	}

	// Filter to one app.
	out, err = handleListExposedTools(f.ec(ctx), mustJSON(map[string]any{"app": app2.Name}))
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	var filtered []exposedToolDTO
	if err := json.Unmarshal([]byte(out), &filtered); err != nil {
		t.Fatalf("parse filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ToolName != "t2" {
		t.Errorf("filtered = %+v, want [t2]", filtered)
	}
}

// TestExposedTool_EndToEnd exercises the full agent-time flow: admin
// creates + exposes a script for "bartender"; a bartender caller pulls
// the runner's List output, confirms the tool appears with the expected
// schema, then invokes it via Invoke. The Monty engine runs the backing
// script and the result flows back through invokeRunScript.
func TestExposedTool_EndToEnd(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	body := "def lookup(name):\n    return {'name': name, 'hit': True}\n"
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "lookups", body, ""); err != nil {
		t.Fatalf("create script: %v", err)
	}
	if _, err := handleExposeScriptFunctionAsTool(f.ec(ctx), mustJSON(map[string]any{
		"app":              f.app.Name,
		"script":           "lookups",
		"fn_name":          "lookup",
		"tool_name":        "lookup",
		"description":      "Look up a name",
		"visible_to_roles": []string{"bartender"},
	})); err != nil {
		t.Fatalf("expose: %v", err)
	}

	// Bartender pulls the available tool set via the runner.
	runner := &exposedToolRunner{pool: f.pool}
	bartender := &services.Caller{
		TenantID: f.tenant.ID,
		UserID:   f.user.ID,
		Roles:    []string{"bartender"},
	}
	defs, err := runner.List(ctx, bartender)
	if err != nil {
		t.Fatalf("runner.List: %v", err)
	}
	if len(defs) != 1 || defs[0].ToolName != "lookup" {
		t.Fatalf("defs = %+v, want [lookup]", defs)
	}

	// Invoke the tool with a name arg; script returns {name: "jane", hit: true}.
	result, err := defs[0].Invoke(ctx, nil, map[string]any{"name": "jane"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("parse result %q: %v", result, err)
	}
	if got["name"] != "jane" {
		t.Errorf("result.name = %v, want jane", got["name"])
	}
	if got["hit"] != true {
		t.Errorf("result.hit = %v, want true", got["hit"])
	}

	// Audit: a script_runs row was created with triggered_by=manual (since
	// invokeRunScript uses manual; this is a v0.1 limitation — exposed
	// invocations go through the same keystone as admin run_script).
	var runCount int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM script_runs
		WHERE tenant_id = $1 AND status = 'completed'
	`, f.tenant.ID).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runCount < 1 {
		t.Errorf("expected at least 1 completed script_run, got %d", runCount)
	}
}

func TestExposedToolRunner_List_VisibilityAndStale(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	scriptName := seedExposableScript(t, f)

	if _, err := handleExposeScriptFunctionAsTool(f.ec(ctx), mustJSON(map[string]any{
		"app":              f.app.Name,
		"script":           scriptName,
		"fn_name":          "lookup",
		"tool_name":        "lookup",
		"visible_to_roles": []string{"bartender"},
	})); err != nil {
		t.Fatalf("expose: %v", err)
	}

	runner := &exposedToolRunner{pool: f.pool}

	// Caller with the right role sees the tool.
	bartender := &services.Caller{TenantID: f.tenant.ID, UserID: f.user.ID, Roles: []string{"bartender"}}
	defs, err := runner.List(ctx, bartender)
	if err != nil {
		t.Fatalf("list(bartender): %v", err)
	}
	// Runner returns all non-stale tools; the registry applies
	// VisibleToRoles downstream. Here we just check enumeration.
	if len(defs) != 1 || defs[0].ToolName != "lookup" {
		t.Fatalf("defs = %+v, want one entry for lookup", defs)
	}
	if len(defs[0].VisibleToRoles) != 1 || defs[0].VisibleToRoles[0] != "bartender" {
		t.Errorf("visible_to_roles = %v", defs[0].VisibleToRoles)
	}

	// Update the script to drop the lookup fn — runner should flag stale + skip.
	if _, err := updateScript(ctx, f.pool, f.admin, f.app.Name, scriptName, "def other():\n    return 1\n"); err != nil {
		t.Fatalf("update script: %v", err)
	}
	defs2, err := runner.List(ctx, bartender)
	if err != nil {
		t.Fatalf("list(after update): %v", err)
	}
	if len(defs2) != 0 {
		t.Errorf("expected lookup to be stale + skipped, got %+v", defs2)
	}
	// And the DB row should now be flagged.
	var isStale bool
	if err := f.pool.QueryRow(ctx, `
		SELECT is_stale FROM exposed_tools WHERE tenant_id = $1 AND tool_name = $2
	`, f.tenant.ID, "lookup").Scan(&isStale); err != nil {
		t.Fatalf("query stale: %v", err)
	}
	if !isStale {
		t.Error("exposed_tools.is_stale was not flipped to true after fn removal")
	}
}
