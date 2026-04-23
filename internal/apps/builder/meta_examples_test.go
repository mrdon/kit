// Package builder: meta_examples_test.go covers the builder_examples
// meta-tool. Unit tests check the catalog + lookup semantics against the
// static map; the end-to-end test takes the mug_club payload and drives
// it through create_app + app_create_script + app_expose_tool
// + app_run_script, verifying the example is more than a README — it really
// spins up into a working bundle.
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

// TestBuilderExamples_Catalog verifies the no-arg call returns all five
// IDs in sorted order so the LLM output is stable.
func TestBuilderExamples_Catalog(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	out, err := handleBuilderExamples(f.ec(ctx), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("builder_examples: %v", err)
	}
	var list []exampleCatalogEntry
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("parse catalog: %v\nraw=%s", err, out)
	}
	want := []string{
		"crm_with_service_layer",
		"mug_club",
		"review_triage",
		"timecards",
		"vendor_book_multi_script",
		"weekly_digest",
	}
	if len(list) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(list), len(want), list)
	}
	for i, id := range want {
		if list[i].ID != id {
			t.Errorf("entry[%d].ID = %q, want %q", i, list[i].ID, id)
		}
		if list[i].Title == "" {
			t.Errorf("entry[%d] %q has empty title", i, id)
		}
		if list[i].Description == "" {
			t.Errorf("entry[%d] %q has empty description", i, id)
		}
	}
}

// TestBuilderExamples_MugClubDefinition checks the full payload shape for
// mug_club: the example has one app, one script, three exposed tools.
func TestBuilderExamples_MugClubDefinition(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	out, err := handleBuilderExamples(f.ec(ctx), json.RawMessage(`{"name":"mug_club"}`))
	if err != nil {
		t.Fatalf("builder_examples(mug_club): %v", err)
	}
	var def exampleDefinition
	if err := json.Unmarshal([]byte(out), &def); err != nil {
		t.Fatalf("parse definition: %v\nraw=%s", err, out)
	}
	if def.ID != "mug_club" {
		t.Errorf("id = %q, want mug_club", def.ID)
	}
	if len(def.Apps) != 1 {
		t.Fatalf("apps = %d, want 1", len(def.Apps))
	}
	app := def.Apps[0]
	if app.AppName != "mug_club" {
		t.Errorf("app_name = %q, want mug_club", app.AppName)
	}
	if len(app.Scripts) != 1 || app.Scripts[0].Name != "core" {
		t.Errorf("scripts = %+v, want [core]", app.Scripts)
	}
	if !strings.Contains(app.Scripts[0].Body, "def add_member") {
		t.Errorf("core body missing add_member:\n%s", app.Scripts[0].Body)
	}
	if len(app.Expose) != 3 {
		t.Errorf("expose count = %d, want 3", len(app.Expose))
	}
	// Verify each exposed tool has the expected role set.
	for _, e := range app.Expose {
		if len(e.VisibleToRoles) == 0 {
			t.Errorf("expose %q has empty visible_to_roles", e.ToolName)
		}
	}
}

// TestBuilderExamples_UnknownName returns a clean error, not a nil
// definition that could be mis-read as "empty example".
func TestBuilderExamples_UnknownName(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()
	_, err := handleBuilderExamples(f.ec(ctx), json.RawMessage(`{"name":"does_not_exist"}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown example") {
		t.Errorf("err = %v, want 'unknown example'", err)
	}
}

// TestBuilderExamples_NonAdminForbidden: like every other meta-tool,
// non-admin callers get ErrForbidden even for the read-only catalog
// call. Examples hint at app structure and expose tool-name conventions
// we don't want to leak to regular users.
func TestBuilderExamples_NonAdminForbidden(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()
	nonAdmin := &services.Caller{TenantID: f.tenant.ID, UserID: f.user.ID, IsAdmin: false}
	ec := &execContextLike{Ctx: ctx, Pool: f.pool, Caller: nonAdmin}
	_, err := handleBuilderExamples(ec, json.RawMessage(`{}`))
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

// TestBuilderExamples_MugClubEndToEnd is the proof that the example
// payload is actually executable: pull the mug_club bundle via the tool,
// replay it against create_app + app_create_script + expose, then call
// app_run_script on one of its functions and verify an app_items row lands.
func TestBuilderExamples_MugClubEndToEnd(t *testing.T) {
	f := newScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deps := scriptTestDeps(t, f)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	// 1. Fetch the example via the tool (not the map directly) so we cover
	//    the whole builder_examples path the admin would use.
	out, err := handleBuilderExamples(f.ec(ctx), json.RawMessage(`{"name":"mug_club"}`))
	if err != nil {
		t.Fatalf("fetch example: %v", err)
	}
	var def exampleDefinition
	if err := json.Unmarshal([]byte(out), &def); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(def.Apps) == 0 {
		t.Fatal("example has no apps")
	}
	spec := def.Apps[0]

	// 2. Replay: create_app with a tenant-unique name so we don't collide
	//    with scriptFixture's default app. The example's AppName is a
	//    template — admins are free to rename. We suffix a UUID to stay
	//    inside one tenant's namespace.
	appName := spec.AppName + "-" + uuid.NewString()[:6]
	if _, err := handleCreateApp(f.ec(ctx), mustJSON(map[string]any{
		"name":        appName,
		"description": def.Description,
	})); err != nil {
		t.Fatalf("create_app: %v", err)
	}

	// 3. Replay: app_create_script for each spec.Scripts entry.
	for _, s := range spec.Scripts {
		if _, err := handleCreateScript(f.ec(ctx), mustJSON(map[string]any{
			"app":  appName,
			"name": s.Name,
			"body": s.Body,
		})); err != nil {
			t.Fatalf("app_create_script %q: %v", s.Name, err)
		}
	}

	// 4. Replay: expose each function. Tool names in the example are
	//    globally-unique-ish but the tenant uniqueness constraint bites
	//    if another test ran the same example against the same tenant,
	//    which scriptFixture's per-test tenant guarantees doesn't happen.
	for _, e := range spec.Expose {
		if _, err := handleExposeScriptFunctionAsTool(f.ec(ctx), mustJSON(map[string]any{
			"app":              appName,
			"script":           e.Script,
			"fn_name":          e.Fn,
			"tool_name":        e.ToolName,
			"visible_to_roles": e.VisibleToRoles,
		})); err != nil {
			t.Fatalf("expose %q: %v", e.ToolName, err)
		}
	}

	// 5. Run the add_member function — the simplest mutation path that
	//    proves the whole chain produced a working bundle. If the db_
	//    builtin set lands a row into app_items, the scripts are live.
	resp, err := invokeRunScript(ctx, f.pool, f.admin, deps, appName, "core", "add_member",
		map[string]any{"name": "Ada Lovelace", "email": "ADA@example.com"}, nil)
	if err != nil {
		t.Fatalf("app_run_script add_member: %v", err)
	}
	if resp.Status != RunStatusCompleted {
		t.Fatalf("status = %q, want completed (err=%q)", resp.Status, resp.Error)
	}

	// Result should echo the inserted doc. Monty returns a map[string]any
	// with the stored fields + _id.
	doc, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %T, want map", resp.Result)
	}
	if got, _ := doc["email"].(string); got != "ada@example.com" {
		t.Errorf("email = %q, want lowered 'ada@example.com'", got)
	}
	if got, _ := doc["tier"].(string); got != "silver" {
		t.Errorf("tier default = %q, want silver", got)
	}

	// Pull the app_id out of the freshly-created builder_apps row so we
	// can count rows in app_items by (tenant, app).
	app, err := loadBuilderAppByName(ctx, f.pool, f.tenant.ID, appName)
	if err != nil {
		t.Fatalf("load app: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items
		WHERE tenant_id = $1 AND builder_app_id = $2 AND collection = 'members'
	`, f.tenant.ID, app.ID).Scan(&count); err != nil {
		t.Fatalf("count members: %v", err)
	}
	if count != 1 {
		t.Errorf("member rows = %d, want 1", count)
	}

	// 6. Sanity-check the exposed tools all materialised so the admin can
	//    immediately invoke them via tools_call / the MCP catalog.
	exposed, err := listExposedTools(ctx, f.pool, f.admin, appName)
	if err != nil {
		t.Fatalf("app_list_tools: %v", err)
	}
	if len(exposed) != len(spec.Expose) {
		t.Errorf("exposed count = %d, want %d", len(exposed), len(spec.Expose))
	}
}

// TestBuilderExamples_AllBodiesParse is a cheap compile-time-in-tests
// sanity check: every example body must make it through Monty's
// Compile() so a dumb syntax typo in meta_examples.go fails early
// instead of at admin-install time.
func TestBuilderExamples_AllBodiesParse(t *testing.T) {
	for id, def := range examplesByID {
		for _, app := range def.Apps {
			for _, s := range app.Scripts {
				if _, err := testEngine.Compile(s.Body); err != nil {
					t.Errorf("example %s/%s/%s failed to compile: %v", id, app.AppName, s.Name, err)
				}
			}
		}
	}
}
