// Package builder: meta_exposed_invoke.go is a thin public wrapper over
// the package-private exposed-tool dispatch path. External surfaces (MCP
// session-tool handlers) look up an exposed_tools row by tool_name, and
// dispatch through the same invokeRunScript keystone that admin-side
// app_run_script uses. Matches the semantics of meta_exposed_runner.go's
// invokeExposedTool, but starts from just (caller, toolName) instead of
// pre-resolved app/script/fn — the MCP side doesn't close over those at
// handler-build time because we want one handler that routes by name.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// InvokeExposedTool resolves an exposed_tools row by (caller.TenantID,
// toolName), then dispatches the underlying script function via the same
// invokeRunScript keystone used by admin app_run_script. Invoked from MCP
// session-tool handlers; the agent-side path stays on
// exposedToolRunner.invokeExposedTool (which has pre-resolved names).
//
// Caller admin-check is intentionally bypassed (shadow-admin inside the
// invoke helper) — visibility is already gated upstream by
// VisibleToRoles filtering at session-register time.
func InvokeExposedTool(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	toolName string,
	args map[string]any,
) (string, error) {
	if caller == nil {
		return "", errors.New("exposed tool: caller required")
	}
	if pool == nil {
		return "", errors.New("exposed tool: pool required")
	}
	var (
		appName, scriptName, fnName string
	)
	err := pool.QueryRow(ctx, `
		SELECT ba.name, s.name, et.fn_name
		FROM exposed_tools et
		JOIN scripts s       ON s.id  = et.script_id
		JOIN builder_apps ba ON ba.id = s.builder_app_id
		WHERE et.tenant_id = $1 AND et.tool_name = $2
	`, caller.TenantID, toolName).Scan(&appName, &scriptName, &fnName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("exposed tool %q not found", toolName)
	}
	if err != nil {
		return "", fmt.Errorf("loading exposed tool: %w", err)
	}

	deps := currentRunDeps
	if deps == nil || deps.Engine == nil {
		return "", errors.New("exposed tool: script engine not wired")
	}
	shadow := *caller
	shadow.IsAdmin = true

	resp, err := invokeRunScript(ctx, pool, &shadow, deps, appName, scriptName, fnName, args, nil)
	if err != nil {
		return "", err
	}
	if resp.Status != RunStatusCompleted {
		msg := resp.Error
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("exposed tool %s: %s", toolName, msg)
	}
	out, err := json.Marshal(resp.Result)
	if err != nil {
		return "", fmt.Errorf("marshalling result: %w", err)
	}
	return string(out), nil
}
