// Integration tests for the Phase 4a app-CRUD meta-tools. Each test opens a
// tenant via testdb, exercises the handler end-to-end against Postgres, and
// cleans up via t.Cleanup + ON DELETE CASCADE on tenants. The tests use the
// handler entry points (handleCreateApp, handleDeleteApp, ...) rather than
// calling the low-level createApp / deleteApp helpers directly so we
// exercise the parseInput → argString → guardAdmin call chain for real.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// metaFixture is an admin-caller tenant ready for meta-tool testing. newly-
// minted tenants have no apps so create_app runs against an empty set.
type metaFixture struct {
	pool   *pgxpool.Pool
	tenant *models.Tenant
	admin  *services.Caller
	user   *models.User
}

// ec returns an execContextLike bound to the fixture's admin caller — the
// default for most tests. Tests that need a non-admin build their own.
func (f *metaFixture) ec(ctx context.Context) *execContextLike {
	return &execContextLike{Ctx: ctx, Pool: f.pool, Caller: f.admin}
}

// newMetaFixture bootstraps: tenant + admin user + services.Caller. Caller
// has IsAdmin=true by default; non-admin tests swap Caller on the fly.
func newMetaFixture(t *testing.T) *metaFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_meta_" + uuid.NewString()
	slug := models.SanitizeSlug("meta-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "meta-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_meta_"+uuid.NewString()[:8], "Meta Admin")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	return &metaFixture{
		pool:   pool,
		tenant: tenant,
		user:   user,
		admin: &services.Caller{
			TenantID: tenant.ID,
			UserID:   user.ID,
			IsAdmin:  true,
		},
	}
}

// parseSummary pulls a single appSummary out of a handler string result. The
// helper keeps per-test asserts short.
func parseSummary(t *testing.T, s string) appSummary {
	t.Helper()
	var out appSummary
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("parsing summary: %v\nraw=%s", err, s)
	}
	return out
}

func TestCreateApp_HappyPath(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	out, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"crm","description":"customers"}`))
	if err != nil {
		t.Fatalf("create_app: %v", err)
	}
	app := parseSummary(t, out)
	if app.Name != "crm" {
		t.Errorf("name = %q, want crm", app.Name)
	}
	if app.Description != "customers" {
		t.Errorf("description = %q, want customers", app.Description)
	}
	if app.ID == uuid.Nil {
		t.Error("id is nil UUID")
	}
	if app.CreatedAt == "" {
		t.Error("created_at is empty")
	}
}

func TestCreateApp_DuplicateName(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	if _, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"dup"}`)); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"dup"}`))
	if err == nil {
		t.Fatal("expected duplicate-name error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want 'already exists'", err)
	}
}

func TestListApps_SortedAndTenantIsolated(t *testing.T) {
	fA := newMetaFixture(t)
	fB := newMetaFixture(t) // separate tenant; must not see each other's apps
	ctx := context.Background()

	// Tenant A: create apps out of alphabetical order to confirm the SQL
	// ORDER BY is doing the sort (not accidental insertion order).
	for _, name := range []string{"zeta", "alpha", "beta"} {
		if _, err := handleCreateApp(fA.ec(ctx), mustJSON(map[string]any{"name": name})); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	// Tenant B: one app with a distinctive name so we can detect leaks.
	if _, err := handleCreateApp(fB.ec(ctx), mustJSON(map[string]any{"name": "b_only"})); err != nil {
		t.Fatalf("create b_only: %v", err)
	}

	out, err := handleListApps(fA.ec(ctx), nil)
	if err != nil {
		t.Fatalf("list_apps: %v", err)
	}
	var apps []appSummary
	if err := json.Unmarshal([]byte(out), &apps); err != nil {
		t.Fatalf("parse: %v", err)
	}
	gotNames := make([]string, len(apps))
	for i, a := range apps {
		gotNames[i] = a.Name
		if a.Name == "b_only" {
			t.Fatal("tenant A saw tenant B's app — isolation broken")
		}
	}
	wantOrder := []string{"alpha", "beta", "zeta"}
	if len(gotNames) != len(wantOrder) {
		t.Fatalf("got %d apps, want %d: %v", len(gotNames), len(wantOrder), gotNames)
	}
	for i, n := range wantOrder {
		if gotNames[i] != n {
			t.Errorf("apps[%d] = %q, want %q", i, gotNames[i], n)
		}
	}
}

func TestGetApp_NotFound(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()
	_, err := handleGetApp(f.ec(ctx), json.RawMessage(`{"name":"nope"}`))
	if !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("err = %v, want ErrAppNotFound", err)
	}
}

func TestGetApp_WithInventory(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	// Create the app via the tool so we drive the same handler path the LLM
	// would — catches stupid bugs in the wrapper that a direct-DB insert
	// wouldn't.
	if _, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"inv","description":"has stuff"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Reach under the hood to seed a script, a schedule, and an exposed
	// tool. Phase 4a doesn't have the meta-tools for these yet — later
	// subtasks wire add_script / schedule_script / expose_tool.
	app, err := loadBuilderAppByName(ctx, f.pool, f.tenant.ID, "inv")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var revID, scriptID uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO scripts (tenant_id, builder_app_id, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, f.tenant.ID, app.ID, "format", "a helper", f.user.ID).Scan(&scriptID); err != nil {
		t.Fatalf("insert script: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO script_revisions (script_id, body, created_by) VALUES ($1, $2, $3) RETURNING id
	`, scriptID, "def main(): return 1\n", f.user.ID).Scan(&revID); err != nil {
		t.Fatalf("insert rev: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE scripts SET current_rev_id = $1 WHERE id = $2`, revID, scriptID); err != nil {
		t.Fatalf("update current_rev: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO tasks (
			id, tenant_id, created_by, description, cron_expr, timezone, channel_id,
			task_type, status, next_run_at, config
		) VALUES (gen_random_uuid(), $1, $2, $3, $4, 'UTC', '',
		          'builder_script', 'active', now() + interval '1 hour',
		          jsonb_build_object('script_id', $5::text, 'fn_name', $6::text))
	`, f.tenant.ID, f.user.ID, "builder: inv/format.main", "*/5 * * * *", scriptID.String(), "main"); err != nil {
		t.Fatalf("insert sched: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO exposed_tools (tenant_id, tool_name, script_id, fn_name, created_by)
		VALUES ($1, $2, $3, $4, $5)
	`, f.tenant.ID, "do_stuff", scriptID, "main", f.user.ID); err != nil {
		t.Fatalf("insert exposed: %v", err)
	}

	out, err := handleGetApp(f.ec(ctx), json.RawMessage(`{"name":"inv"}`))
	if err != nil {
		t.Fatalf("get_app: %v", err)
	}
	var detail appDetail
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("parse detail: %v", err)
	}
	if detail.Name != "inv" {
		t.Errorf("name = %q, want inv", detail.Name)
	}
	if len(detail.Scripts) != 1 || detail.Scripts[0].Name != "format" {
		t.Errorf("scripts = %+v, want [format]", detail.Scripts)
	}
	if len(detail.Schedules) != 1 || detail.Schedules[0].ScriptName != "format" || detail.Schedules[0].FnName != "main" {
		t.Errorf("schedules = %+v, want [format/main]", detail.Schedules)
	}
	if len(detail.ExposedTools) != 1 || detail.ExposedTools[0].ToolName != "do_stuff" {
		t.Errorf("exposed_tools = %+v, want [do_stuff]", detail.ExposedTools)
	}
}

func TestDeleteApp_WithoutConfirm(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	if _, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"del_a"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := handleDeleteApp(f.ec(ctx), json.RawMessage(`{"name":"del_a"}`))
	if !errors.Is(err, ErrMissingConfirm) {
		t.Fatalf("err = %v, want ErrMissingConfirm", err)
	}
}

func TestDeleteApp_BlockedByItems(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	if _, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"del_b"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Insert a live item via ItemService so the pre-flight count sees > 0.
	app, err := loadBuilderAppByName(ctx, f.pool, f.tenant.ID, "del_b")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	svc := NewItemService(f.pool)
	if _, err := svc.InsertOne(ctx, Scope{
		TenantID:     f.tenant.ID,
		BuilderAppID: app.ID,
		Collection:   "c",
		CallerUserID: f.user.ID,
	}, map[string]any{"x": 1}); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	_, err = handleDeleteApp(f.ec(ctx), json.RawMessage(`{"name":"del_b","confirm":true}`))
	if err == nil {
		t.Fatal("expected 'has N items' error, got nil")
	}
	if !strings.Contains(err.Error(), "items") || !strings.Contains(err.Error(), "purge_app_data") {
		t.Errorf("error = %v, want 'has N items; call purge_app_data'", err)
	}
}

func TestDeleteApp_Succeeds(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	if _, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"del_c"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := handleDeleteApp(f.ec(ctx), json.RawMessage(`{"name":"del_c","confirm":true}`))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, `"deleted":"del_c"`) {
		t.Errorf("result = %q, want deleted:del_c", out)
	}
	// Confirm gone.
	if _, err := loadBuilderAppByName(ctx, f.pool, f.tenant.ID, "del_c"); !errors.Is(err, ErrAppNotFound) {
		t.Errorf("app still present after delete: err=%v", err)
	}
}

func TestPurgeAppData_WithoutConfirm(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()
	if _, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"p_a"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := handlePurgeAppData(f.ec(ctx), json.RawMessage(`{"name":"p_a"}`))
	if !errors.Is(err, ErrMissingConfirm) {
		t.Fatalf("err = %v, want ErrMissingConfirm", err)
	}
}

func TestPurgeAppData_DeletesAndEnablesDelete(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	if _, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"p_b"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}
	app, err := loadBuilderAppByName(ctx, f.pool, f.tenant.ID, "p_b")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	svc := NewItemService(f.pool)
	for i := range 3 {
		if _, err := svc.InsertOne(ctx, Scope{
			TenantID:     f.tenant.ID,
			BuilderAppID: app.ID,
			Collection:   "c",
			CallerUserID: f.user.ID,
		}, map[string]any{"i": i}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	out, err := handlePurgeAppData(f.ec(ctx), json.RawMessage(`{"name":"p_b","confirm":true}`))
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !strings.Contains(out, `"purged":3`) {
		t.Errorf("result = %q, want purged:3", out)
	}
	// History rows must be written — the purge is meant to be auditable.
	var hist int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items_history
		WHERE tenant_id = $1 AND builder_app_id = $2 AND operation = 'DELETE'
	`, f.tenant.ID, app.ID).Scan(&hist); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if hist != 3 {
		t.Errorf("history rows = %d, want 3", hist)
	}
	// And delete_app now succeeds because no live items remain.
	if _, err := handleDeleteApp(f.ec(ctx), json.RawMessage(`{"name":"p_b","confirm":true}`)); err != nil {
		t.Errorf("post-purge delete: %v", err)
	}
}

func TestMetaTools_NonAdminForbidden(t *testing.T) {
	f := newMetaFixture(t)
	ctx := context.Background()

	// Seed an app as admin so the non-admin has something to poke at.
	if _, err := handleCreateApp(f.ec(ctx), json.RawMessage(`{"name":"x"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Swap to a non-admin caller sharing the same tenant.
	nonAdmin := &services.Caller{
		TenantID: f.tenant.ID,
		UserID:   f.user.ID,
		IsAdmin:  false,
	}
	ec := &execContextLike{Ctx: ctx, Pool: f.pool, Caller: nonAdmin}

	cases := []struct {
		name  string
		fn    func(*execContextLike, json.RawMessage) (string, error)
		input string
	}{
		{"create_app", handleCreateApp, `{"name":"y"}`},
		{"list_apps", handleListApps, `{}`},
		{"get_app", handleGetApp, `{"name":"x"}`},
		{"delete_app", handleDeleteApp, `{"name":"x","confirm":true}`},
		{"purge_app_data", handlePurgeAppData, `{"name":"x","confirm":true}`},
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

// mustJSON is a test-only convenience for building inputs from a map. Panic
// on marshal failure is fine inside a test helper — test authors can read
// the stack directly.
func mustJSON(m map[string]any) json.RawMessage {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}
