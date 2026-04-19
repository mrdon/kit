// Test helpers shared across tools_call_builtin_test.go. Split out so the
// main test file stays under the 500-line project rule.
package builder

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// exposedToolFixture bundles the rows a tools_call test needs: an app,
// a script with a revision, and an exposed_tools row pointing at them.
type exposedToolFixture struct {
	appID      uuid.UUID
	scriptID   uuid.UUID
	revisionID uuid.UUID
	toolName   string
	fnName     string
}

// seedExposedTool creates a builder_app, a script + revision, and an
// exposed_tools row published at toolName → fnName. The script body is
// whatever the caller supplies.
func seedExposedTool(
	t *testing.T,
	pool *pgxpool.Pool,
	tenantID, userID uuid.UUID,
	toolName, fnName, body string,
	visibleToRoles []string,
) exposedToolFixture {
	t.Helper()
	ctx := context.Background()

	var appID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO builder_apps (tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, tenantID, "app-"+uuid.NewString()[:8], "tools_call fixture", userID).Scan(&appID)
	if err != nil {
		t.Fatalf("insert builder_app: %v", err)
	}

	var scriptID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO scripts (tenant_id, builder_app_id, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, tenantID, appID, "script-"+uuid.NewString()[:8], "tools_call test script", userID).Scan(&scriptID)
	if err != nil {
		t.Fatalf("insert script: %v", err)
	}

	var revID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO script_revisions (script_id, body, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, scriptID, body, userID).Scan(&revID)
	if err != nil {
		t.Fatalf("insert script_revision: %v", err)
	}

	_, err = pool.Exec(ctx, `
		UPDATE scripts SET current_rev_id = $1 WHERE id = $2
	`, revID, scriptID)
	if err != nil {
		t.Fatalf("update scripts.current_rev_id: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO exposed_tools (tenant_id, tool_name, script_id, fn_name, visible_to_roles, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, tenantID, toolName, scriptID, fnName, visibleToRoles, userID)
	if err != nil {
		t.Fatalf("insert exposed_tool: %v", err)
	}

	return exposedToolFixture{
		appID:      appID,
		scriptID:   scriptID,
		revisionID: revID,
		toolName:   toolName,
		fnName:     fnName,
	}
}

// seedParentRun inserts a parent script_runs row so the child row's
// parent_run_id FK is satisfied. Takes a triggeredBy so nesting tests
// can simulate the child's own invocation frame.
func seedParentRun(
	t *testing.T,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	scriptID, revisionID uuid.UUID,
	userID uuid.UUID,
	triggeredBy string,
) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var runID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO script_runs (tenant_id, script_id, revision_id, status, triggered_by, caller_user_id)
		VALUES ($1, $2, $3, 'running', $4, $5)
		RETURNING id
	`, tenantID, scriptID, revisionID, triggeredBy, userID).Scan(&runID)
	if err != nil {
		t.Fatalf("insert parent script_run: %v", err)
	}
	return runID
}

// runToolsCall compiles + runs a Monty script with just the tools_call
// builtin wired in. The parent caller script is authored inline by the
// test; the exposed-tool body comes from the seeded fixture row.
func runToolsCall(
	t *testing.T,
	ctx context.Context,
	src string,
	bundle *ToolsCallBuiltin,
) (any, runtime.Metadata, error) {
	t.Helper()
	mod, err := testEngine.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	caps := &runtime.Capabilities{
		BuiltIns:      bundle.BuiltIns,
		BuiltInParams: bundle.Params,
		RunID:         uuid.New(),
	}
	return testEngine.Run(ctx, mod, "main", nil, caps)
}

// nilFactory returns (nil, nil) — child runs with no host surface. Used
// by the happy-path / no-op child tests where the exposed function is
// pure Python.
func nilFactory(
	tenantID, builderAppID, callerUserID uuid.UUID,
	callerRoles []string,
	childRunID uuid.UUID,
) (map[string]runtime.GoFunc, map[string][]string) {
	return nil, nil
}

// nilFactoryMatching returns a factory matching the ChildBuiltinsFactory
// signature that always yields a nil surface. Small wrapper for clarity
// in the nesting test.
func nilFactoryMatching() ChildBuiltinsFactory {
	return func(
		tenantID, builderAppID, callerUserID uuid.UUID,
		callerRoles []string,
		childRunID uuid.UUID,
	) (map[string]runtime.GoFunc, map[string][]string) {
		return nil, nil
	}
}
