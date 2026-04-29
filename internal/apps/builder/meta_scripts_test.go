// Package builder: meta_scripts_test.go drives the Phase 4b script-CRUD
// meta-tool handlers (create/update/list/get) end-to-end against a local
// test Postgres. Each test seeds its own tenant + admin user + app so
// tests parallelise without stepping on each other.
//
// The app_run_script and app_rollback_script_run tests live in
// meta_scripts_run_test.go and meta_scripts_rollback_test.go respectively
// to keep each file under the 500-LOC cap.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// scriptFixture bundles tenant + admin + app so tests can jump straight
// to exercising the meta-tool handlers. Mirrors metaFixture but also
// creates a builder_apps row.
type scriptFixture struct {
	pool   *pgxpool.Pool
	tenant *models.Tenant
	user   *models.User
	admin  *services.Caller
	app    *BuilderApp
}

func (f *scriptFixture) ec(ctx context.Context) *execContextLike {
	return &execContextLike{Ctx: ctx, Pool: f.pool, Caller: f.admin}
}

func newScriptFixture(t *testing.T) *scriptFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_script_" + uuid.NewString()
	slug := models.SanitizeSlug("script-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "script-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_script_"+uuid.NewString()[:8], "Script Admin", "")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleAdmin, "admin"); err != nil {
		t.Fatalf("creating admin role: %v", err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, user.ID, models.RoleAdmin); err != nil {
		t.Fatalf("assigning admin role: %v", err)
	}

	caller := &services.Caller{
		TenantID: tenant.ID,
		UserID:   user.ID,
		IsAdmin:  true,
	}

	app, err := createApp(ctx, pool, caller, "app-"+uuid.NewString()[:8], "script test app")
	if err != nil {
		t.Fatalf("creating app: %v", err)
	}

	return &scriptFixture{
		pool:   pool,
		tenant: tenant,
		user:   user,
		admin:  caller,
		app:    app,
	}
}

// TestCreateScript_HappyPath verifies a successful create round-trip:
// scripts row + script_revisions row + current_rev_id populated.
func TestCreateScript_HappyPath(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()

	body := "def main():\n    return 42\n"
	out, err := handleCreateScript(f.ec(ctx), mustJSON(map[string]any{
		"app":         f.app.Name,
		"name":        "answer",
		"body":        body,
		"description": "returns 42",
	}))
	if err != nil {
		t.Fatalf("app_create_script: %v", err)
	}
	var dto scriptDTO
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if dto.Name != "answer" || dto.Description != "returns 42" {
		t.Errorf("dto = %+v", dto)
	}
	if dto.ID == uuid.Nil {
		t.Error("id is nil UUID")
	}
	if dto.CurrentRevID == nil {
		t.Error("current_rev_id not set")
	}

	// Verify the revision body round-trips.
	detail, err := getScript(ctx, f.pool, f.admin, f.app.Name, "answer")
	if err != nil {
		t.Fatalf("app_get_script: %v", err)
	}
	if detail.Body != body {
		t.Errorf("body mismatch: got %q want %q", detail.Body, body)
	}
}

func TestCreateScript_DuplicateName(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	body := "def main(): pass\n"
	input := mustJSON(map[string]any{"app": f.app.Name, "name": "dup", "body": body})
	if _, err := handleCreateScript(f.ec(ctx), input); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := handleCreateScript(f.ec(ctx), input)
	if err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want 'already exists'", err)
	}
}

// TestUpdateScript_AdvancesRevision verifies app_update_script creates a
// new revision and repoints current_rev_id.
func TestUpdateScript_AdvancesRevision(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()

	first, err := createScript(ctx, f.pool, f.admin, f.app.Name, "evolve", "def main(): return 1\n", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	firstRev := *first.CurrentRevID

	out, err := handleUpdateScript(f.ec(ctx), mustJSON(map[string]any{
		"app":  f.app.Name,
		"name": "evolve",
		"body": "def main(): return 2\n",
	}))
	if err != nil {
		t.Fatalf("app_update_script: %v", err)
	}
	var updated scriptDTO
	if err := json.Unmarshal([]byte(out), &updated); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if updated.CurrentRevID == nil || *updated.CurrentRevID == firstRev {
		t.Errorf("current_rev_id did not advance: %v", updated.CurrentRevID)
	}
	if updated.ID != first.ID {
		t.Errorf("script id changed across update: %v -> %v", first.ID, updated.ID)
	}

	var n int
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM script_revisions WHERE script_id = $1`, first.ID).Scan(&n); err != nil {
		t.Fatalf("count revs: %v", err)
	}
	if n != 2 {
		t.Errorf("revisions = %d, want 2", n)
	}
}

func TestListScripts_FilterByApp(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()

	// Two scripts under f.app, one under a second app in the same tenant.
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "a", "def main(): return 1\n", ""); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "b", "def main(): return 2\n", ""); err != nil {
		t.Fatalf("create b: %v", err)
	}
	otherApp, err := createApp(ctx, f.pool, f.admin, "other-"+uuid.NewString()[:4], "")
	if err != nil {
		t.Fatalf("create other app: %v", err)
	}
	if _, err := createScript(ctx, f.pool, f.admin, otherApp.Name, "other", "def main(): return 3\n", ""); err != nil {
		t.Fatalf("create other: %v", err)
	}

	out, err := handleListScripts(f.ec(ctx), mustJSON(map[string]any{"app": f.app.Name}))
	if err != nil {
		t.Fatalf("app_list_scripts: %v", err)
	}
	var list []scriptSummary
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("parse: %v", err)
	}
	names := make(map[string]bool)
	for _, s := range list {
		names[s.Name] = true
	}
	if !names["a"] || !names["b"] {
		t.Errorf("missing expected scripts, got %v", names)
	}
	if names["other"] {
		t.Error("list leaked other app's script")
	}
}

func TestGetScript_NotFound(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	_, err := handleGetScript(f.ec(ctx), mustJSON(map[string]any{"app": f.app.Name, "name": "ghost"}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestScriptTools_NonAdminForbidden(t *testing.T) {
	f := newScriptFixture(t)
	ctx := context.Background()
	if _, err := createScript(ctx, f.pool, f.admin, f.app.Name, "p", "def main(): return 1\n", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	nonAdmin := &services.Caller{TenantID: f.tenant.ID, UserID: f.user.ID, IsAdmin: false}
	ec := &execContextLike{Ctx: ctx, Pool: f.pool, Caller: nonAdmin}

	cases := []struct {
		name  string
		fn    func(*execContextLike, json.RawMessage) (string, error)
		input string
	}{
		{"app_create_script", handleCreateScript, fmt.Sprintf(`{"app":%q,"name":"x","body":"def main(): return 1"}`, f.app.Name)},
		{"app_update_script", handleUpdateScript, fmt.Sprintf(`{"app":%q,"name":"p","body":"def main(): return 2"}`, f.app.Name)},
		{"app_list_scripts", handleListScripts, fmt.Sprintf(`{"app":%q}`, f.app.Name)},
		{"app_get_script", handleGetScript, fmt.Sprintf(`{"app":%q,"name":"p"}`, f.app.Name)},
		{"app_run_script", handleRunScript, fmt.Sprintf(`{"app":%q,"script":"p","fn":"main"}`, f.app.Name)},
		{"app_rollback_script_run", handleRollbackScriptRun, `{"run_id":"00000000-0000-0000-0000-000000000000","confirm":true}`},
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
