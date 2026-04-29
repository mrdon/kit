// Package builder: acceptance_test.go contains Phase 5 task 5c end-to-
// end acceptance tests. Each test drives the admin workflow through the
// meta-tool handlers (the same entry points the LLM agent or MCP server
// invoke) — no direct service-layer shortcuts. Together they prove the
// builder substrate composes as documented:
//
//   - TestAcceptance_MugClub_DayOneSmoke (this file): the "one-shot easy"
//     pitch check. Admin installs mug_club via builder_examples ->
//     create_app -> app_create_script -> app_expose_tool, a
//     non-admin bartender discovers the tool through tools.NewRegistry,
//     invokes it, rows land (with concurrent-invocation atomicity).
//
//   - TestAcceptance_ReviewTriage_Showcase (acceptance_triage_test.go):
//     cron + llm_* + decisions + rollback, keyed on the review_triage
//     example with a stubSender that classifies deterministically.
//
//   - TestAcceptance_AppIsolation (acceptance_isolation_test.go): two
//     apps in the same tenant sharing a collection name don't collide;
//     delete blocks while rows exist; purge clears only the target
//     app's rows.
//
// Shared fixtures + helpers live here; per-test assertions live in the
// sibling _test.go files so each stays well under the 500-LOC cap.
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
	"github.com/mrdon/kit/internal/tools"
)

// acceptanceFixture bundles tenant + admin + an additional named user
// (manager or bartender) so each test can seed the role split it needs.
type acceptanceFixture struct {
	pool      *pgxpool.Pool
	tenant    *models.Tenant
	admin     *services.Caller
	adminUser *models.User
	roleUser  *models.User
	// roleCaller is the non-admin caller holding one tenant-local role.
	roleCaller *services.Caller
}

// newAcceptanceFixture provisions a tenant + admin user + one role-
// bearing user holding the given role name. The role is created inline
// so tests don't depend on a preseeded role catalog. Also seeds a
// tenant_builder_config row so the llm_* budget pre-check has data to
// read.
func newAcceptanceFixture(t *testing.T, roleName string) *acceptanceFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_accept_" + uuid.NewString()
	slug := models.SanitizeSlug("accept-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "acceptance", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	adminUser, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_admin_"+uuid.NewString()[:8], "Admin User", "")
	if err != nil {
		t.Fatalf("creating admin user: %v", err)
	}
	roleUser, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_"+roleName+"_"+uuid.NewString()[:8], "Role User", "")
	if err != nil {
		t.Fatalf("creating role user: %v", err)
	}

	if _, err := models.CreateRole(ctx, pool, tenant.ID, roleName, ""); err != nil {
		t.Fatalf("creating role %q: %v", roleName, err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, roleUser.ID, roleName); err != nil {
		t.Fatalf("assigning role: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO tenant_builder_config (tenant_id, llm_daily_cent_cap, max_db_calls_per_run)
		VALUES ($1, 10000, 1000)
		ON CONFLICT (tenant_id) DO NOTHING
	`, tenant.ID); err != nil {
		t.Fatalf("seeding tenant_builder_config: %v", err)
	}

	return &acceptanceFixture{
		pool:      pool,
		tenant:    tenant,
		adminUser: adminUser,
		roleUser:  roleUser,
		admin: &services.Caller{
			TenantID: tenant.ID,
			UserID:   adminUser.ID,
			IsAdmin:  true,
		},
		roleCaller: &services.Caller{
			TenantID: tenant.ID,
			UserID:   roleUser.ID,
			Roles:    []string{roleName},
		},
	}
}

// adminEC builds an execContextLike bound to the admin caller — the
// shape every meta-tool handler expects.
func (f *acceptanceFixture) adminEC(ctx context.Context) *execContextLike {
	return &execContextLike{Ctx: ctx, Pool: f.pool, Caller: f.admin}
}

// replayExample drives create_app + app_create_script + expose +
// app_schedule_script for every entry in an example definition's first app
// (every existing example has exactly one app). Returns the app name
// that ended up in the DB.
func replayExample(t *testing.T, f *acceptanceFixture, def exampleDefinition) string {
	t.Helper()
	ctx := context.Background()
	if len(def.Apps) == 0 {
		t.Fatalf("example %s has no apps", def.ID)
	}
	spec := def.Apps[0]

	if _, err := handleCreateApp(f.adminEC(ctx), mustJSON(map[string]any{
		"name":        spec.AppName,
		"description": def.Description,
	})); err != nil {
		t.Fatalf("create_app %q: %v", spec.AppName, err)
	}
	for _, s := range spec.Scripts {
		if _, err := handleCreateScript(f.adminEC(ctx), mustJSON(map[string]any{
			"app":  spec.AppName,
			"name": s.Name,
			"body": s.Body,
		})); err != nil {
			t.Fatalf("app_create_script %q: %v", s.Name, err)
		}
	}
	for _, e := range spec.Expose {
		if _, err := handleExposeScriptFunctionAsTool(f.adminEC(ctx), mustJSON(map[string]any{
			"app":              spec.AppName,
			"script":           e.Script,
			"fn_name":          e.Fn,
			"tool_name":        e.ToolName,
			"visible_to_roles": e.VisibleToRoles,
			"args_schema":      e.ArgsSchema,
		})); err != nil {
			t.Fatalf("expose %q: %v", e.ToolName, err)
		}
	}
	for _, sc := range spec.Schedule {
		if _, err := handleScheduleScript(f.adminEC(ctx), mustJSON(map[string]any{
			"app":    spec.AppName,
			"script": sc.Script,
			"fn":     sc.Fn,
			"cron":   sc.Cron,
		})); err != nil {
			t.Fatalf("app_schedule_script %q/%q: %v", sc.Script, sc.Fn, err)
		}
	}
	return spec.AppName
}

// acceptanceDeps builds scriptRunDeps from a fixture and an explicit
// sender so each test can plug in its preferred stub.
func acceptanceDeps(t *testing.T, f *acceptanceFixture, sender Sender) *scriptRunDeps {
	t.Helper()
	return &scriptRunDeps{
		Services:   services.New(f.pool, nil),
		Engine:     testEngine,
		Sender:     sender,
		BuildSlack: nil,
	}
}

// registryHasToolFor returns true if the registry surfaces a tool with
// the given name to the supplied caller. Uses DefinitionsFor so the
// VisibleToRoles/AdminOnly filters are applied — if the caller should
// not see the tool this returns false.
func registryHasToolFor(r *tools.Registry, caller *services.Caller, toolName string) bool {
	for _, d := range r.DefinitionsFor(caller) {
		if d.Name == toolName {
			return true
		}
	}
	return false
}

// invokeRegistryTool routes a call through Registry.Execute. This is
// the same dispatch path agent.go uses; sidesteps the need to read the
// private Def slice.
func invokeRegistryTool(r *tools.Registry, ec *tools.ExecContext, name string, input json.RawMessage) (string, error) {
	return r.Execute(ec, name, input)
}

// containsAppNamed reports whether the list contains an app with the
// exact name.
func containsAppNamed(list []appSummary, name string) bool {
	for _, a := range list {
		if a.Name == name {
			return true
		}
	}
	return false
}

// hasAllToolNames returns true if every name in want is present in the
// exposed list. Order-agnostic.
func hasAllToolNames(list []exposedToolDTO, want []string) bool {
	have := make(map[string]bool, len(list))
	for _, e := range list {
		have[e.ToolName] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

// scriptRunsCompleted returns the number of completed script_runs for
// the tenant.
func scriptRunsCompleted(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM script_runs
		WHERE tenant_id = $1 AND status = 'completed'
	`, tenantID).Scan(&n); err != nil {
		t.Fatalf("counting completed runs: %v", err)
	}
	return n
}

// countCollectionRows returns the number of app_items rows in the
// collection across all apps for a tenant.
func countCollectionRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, collection string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items WHERE tenant_id = $1 AND collection = $2
	`, tenantID, collection).Scan(&n); err != nil {
		t.Fatalf("counting %s rows: %v", collection, err)
	}
	return n
}

// TestAcceptance_MugClub_DayOneSmoke replays the mug_club starter end-
// to-end: admin installs via meta-tools, a bartender user discovers
// the exposed tool via the generic tools.Registry, invokes it, and the
// rows land. Also verifies concurrent-invocation atomicity across 10
// goroutines hitting the same exposed tool.
func TestAcceptance_MugClub_DayOneSmoke(t *testing.T) {
	f := newAcceptanceFixture(t, "bartender")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Wire the script runtime so handleRunScript + the exposed-tool
	// runner can actually execute scripts.
	deps := acceptanceDeps(t, f, &stubSender{respText: "stub", model: "haiku", inTokens: 1, outTokens: 1})
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	// Wire the exposed-tool runner into tools.NewRegistry so the
	// bartender's registry picks up tenant-published tools.
	tools.SetExposedToolRunner(&exposedToolRunner{pool: f.pool})
	t.Cleanup(func() { tools.SetExposedToolRunner(nil) })

	// 1. Fetch the mug_club example via the meta-tool.
	out, err := handleBuilderExamples(f.adminEC(ctx), json.RawMessage(`{"name":"mug_club"}`))
	if err != nil {
		t.Fatalf("builder_examples: %v", err)
	}
	var def exampleDefinition
	if err := json.Unmarshal([]byte(out), &def); err != nil {
		t.Fatalf("parse example: %v", err)
	}

	// 2. Replay the example through create_app / app_create_script / expose.
	appName := replayExample(t, f, def)

	// 3. Verify list_apps / app_list_scripts / app_list_tools reflect
	//    the install.
	listOut, err := handleListApps(f.adminEC(ctx), nil)
	if err != nil {
		t.Fatalf("list_apps: %v", err)
	}
	var apps []appSummary
	_ = json.Unmarshal([]byte(listOut), &apps)
	if !containsAppNamed(apps, appName) {
		t.Fatalf("list_apps missing %q: %v", appName, apps)
	}

	scriptsOut, err := handleListScripts(f.adminEC(ctx), mustJSON(map[string]any{"app": appName}))
	if err != nil {
		t.Fatalf("app_list_scripts: %v", err)
	}
	var scripts []scriptSummary
	_ = json.Unmarshal([]byte(scriptsOut), &scripts)
	if len(scripts) != 1 || scripts[0].Name != "core" {
		t.Fatalf("scripts = %+v, want [core]", scripts)
	}

	exposedOut, err := handleListExposedTools(f.adminEC(ctx), mustJSON(map[string]any{"app": appName}))
	if err != nil {
		t.Fatalf("app_list_tools: %v", err)
	}
	var exposed []exposedToolDTO
	_ = json.Unmarshal([]byte(exposedOut), &exposed)
	wantExposed := []string{"add_mug_member", "list_mug_members", "update_mug_tier"}
	if !hasAllToolNames(exposed, wantExposed) {
		t.Fatalf("exposed = %+v, want all of %v", exposed, wantExposed)
	}

	// 4. Build the bartender's tool registry. It should pick up the
	//    exposed tools via the runner, visible to the bartender role.
	bartenderReg := tools.NewRegistry(ctx, f.roleCaller, false)
	if !registryHasToolFor(bartenderReg, f.roleCaller, "add_mug_member") {
		t.Fatalf("bartender registry missing add_mug_member visible to bartender")
	}
	if !registryHasToolFor(bartenderReg, f.roleCaller, "list_mug_members") {
		t.Fatalf("bartender registry missing list_mug_members visible to bartender")
	}

	// 5. Invoke add_mug_member as the bartender via the registry's
	//    Execute path — same dispatch agent.go uses. The exposed-tool
	//    Invoke closure only reads ctx from the ExecContext.
	ec := &tools.ExecContext{Ctx: ctx, Pool: f.pool}
	res, err := invokeRegistryTool(bartenderReg, ec, "add_mug_member",
		mustJSON(map[string]any{"name": "Jane Doe", "email": "JANE@Example.COM"}))
	if err != nil {
		t.Fatalf("add_mug_member: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(res), &doc); err != nil {
		t.Fatalf("parse add result: %v\nraw=%s", err, res)
	}
	if email, _ := doc["email"].(string); email != "jane@example.com" {
		t.Errorf("email = %q, want lowered 'jane@example.com'", email)
	}
	if tier, _ := doc["tier"].(string); tier != "silver" {
		t.Errorf("tier default = %q, want silver", tier)
	}
	// Auto system fields: _id, _created_at, _updated_at all populated.
	if _, ok := doc["_id"].(string); !ok {
		t.Errorf("_id missing/wrong type: %T %v", doc["_id"], doc["_id"])
	}
	if _, ok := doc["_created_at"].(string); !ok {
		t.Errorf("_created_at missing: %v", doc)
	}
	if _, ok := doc["_updated_at"].(string); !ok {
		t.Errorf("_updated_at missing: %v", doc)
	}

	// 6. list_mug_members returns exactly the row we just inserted.
	listRes, err := invokeRegistryTool(bartenderReg, ec, "list_mug_members", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("list_mug_members: %v", err)
	}
	var members []map[string]any
	if err := json.Unmarshal([]byte(listRes), &members); err != nil {
		t.Fatalf("parse list result: %v\nraw=%s", err, listRes)
	}
	if len(members) != 1 {
		t.Fatalf("members count = %d, want 1", len(members))
	}

	// 7. Audit: the bartender's invocation lands a completed
	//    script_runs row. (db_* mutations don't count in
	//    mutation_summary — that surface is for ActionBuiltins like
	//    create_todo — so the row itself is the audit signal.)
	if n := scriptRunsCompleted(t, f.pool, f.tenant.ID); n < 1 {
		t.Errorf("expected >= 1 completed script_run, got %d", n)
	}

	// 8. Atomic concurrency: 10 goroutines, 10 distinct names; all 10
	//    rows must land. Each invocation goes through the exposed-tool
	//    runner which reenters invokeRunScript. If Postgres + the
	//    runtime are serialising correctly there should be no write
	//    loss.
	const concur = 10
	var wg sync.WaitGroup
	errs := make([]error, concur)
	for i := range concur {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			arg := mustJSON(map[string]any{
				"name":  fmt.Sprintf("Concurrent %02d", i),
				"email": fmt.Sprintf("user%02d@example.com", i),
			})
			_, err := invokeRegistryTool(bartenderReg, ec, "add_mug_member", arg)
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent add %d: %v", i, err)
		}
	}
	// Total member rows should be 1 (from step 5) + concur.
	if count := countCollectionRows(t, ctx, f.pool, f.tenant.ID, "members"); count != 1+concur {
		t.Errorf("member rows = %d, want %d", count, 1+concur)
	}
}
