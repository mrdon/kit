// Package builder: meta_exposed_runner.go is the tools.ExposedToolRunner
// implementation used by Kit's agent registry (and, indirectly, the MCP
// filter). Split out from meta_exposed.go so each file stays under the
// 500-LOC soft cap; the CRUD surface (expose/revoke/list) lives next door
// in meta_exposed.go and shares the exposedToolDTO type.
//
// Flow for a non-admin caller:
//
//  1. tools.NewRegistry calls runner.List(ctx, caller).
//  2. runner.List pulls non-stale exposed_tools rows scoped to the caller's
//     tenant, verifies each backing fn still exists in the current
//     revision (lazy stale check), and returns the callable set.
//  3. When the agent picks one, the closure's Invoke re-enters
//     invokeRunScript — the same keystone admins use via run_script.
//
// v0.1 simplification: we shadow the caller's IsAdmin to true so
// guardAdmin inside invokeRunScript lets us through. Visibility is
// already enforced upstream by the registry via VisibleToRoles; skipping
// the admin guard here is intentional and documented at invokeExposedTool.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// exposedToolRunner is the App-scoped implementation of
// tools.ExposedToolRunner. cmd/kit/main.go registers it with the tools
// package once the pool is live; tests that don't need exposed tools
// simply never call tools.SetExposedToolRunner.
type exposedToolRunner struct {
	pool *pgxpool.Pool
}

// ExposedToolRunner returns an implementation of tools.ExposedToolRunner
// bound to the app's pool. Wire into tools via tools.SetExposedToolRunner
// once apps.Init has run and the pool is ready.
func (a *App) ExposedToolRunner() tools.ExposedToolRunner {
	if a.pool == nil {
		return nil
	}
	return &exposedToolRunner{pool: a.pool}
}

// exposedRow is the intermediate shape carried between List's DB fetch
// and the per-row staleness check. Kept package-private so tests in this
// file can reason about the transform stage without re-deriving it.
type exposedRow struct {
	id         uuid.UUID
	toolName   string
	appName    string
	scriptName string
	fnName     string
	desc       string
	argsSchema []byte
	roles      []string
	revID      *uuid.UUID
}

// List enumerates non-stale exposed tools for the caller's tenant, lazily
// marking freshly-broken rows stale. Each returned entry's Invoke closure
// calls invokeRunScript via the existing run_script path, so audit +
// limits + mutation tracking all land the same way they do for admin
// runs.
func (r *exposedToolRunner) List(ctx context.Context, caller *services.Caller) ([]tools.ExposedToolDef, error) {
	if caller == nil {
		return nil, nil
	}
	pending, err := r.fetchExposedRows(ctx, caller.TenantID)
	if err != nil {
		return nil, err
	}
	out := make([]tools.ExposedToolDef, 0, len(pending))
	for _, x := range pending {
		def, ok := r.buildDef(ctx, caller, x)
		if !ok {
			continue
		}
		out = append(out, def)
	}
	return out, nil
}

// fetchExposedRows pulls the non-stale exposed tools for a tenant,
// joined to app + script names so the Invoke closure has everything it
// needs without a second round-trip.
func (r *exposedToolRunner) fetchExposedRows(ctx context.Context, tenantID uuid.UUID) ([]exposedRow, error) {
	const q = `
		SELECT et.id, et.tool_name, ba.name, s.name, et.fn_name,
		       COALESCE(et.description, ''), et.args_schema,
		       COALESCE(et.visible_to_roles, '{}'::text[]), s.current_rev_id
		FROM exposed_tools et
		JOIN scripts s       ON s.id  = et.script_id
		JOIN builder_apps ba ON ba.id = s.builder_app_id
		WHERE et.tenant_id = $1 AND et.is_stale = false
	`
	rows, err := r.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying exposed_tools: %w", err)
	}
	defer rows.Close()

	var out []exposedRow
	for rows.Next() {
		var x exposedRow
		if err := rows.Scan(
			&x.id, &x.toolName, &x.appName, &x.scriptName, &x.fnName,
			&x.desc, &x.argsSchema, &x.roles, &x.revID,
		); err != nil {
			return nil, fmt.Errorf("scan exposed_tools row: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// buildDef converts one DB row into an ExposedToolDef ready for the
// registry, returning ok=false when the row should be skipped (stale or
// unverifiable). Staleness is recorded in the DB as a side effect so
// subsequent Lists skip the query entirely.
func (r *exposedToolRunner) buildDef(
	ctx context.Context,
	caller *services.Caller,
	x exposedRow,
) (tools.ExposedToolDef, bool) {
	if x.revID == nil {
		r.markStale(ctx, caller.TenantID, x.id, "no current revision")
		return tools.ExposedToolDef{}, false
	}
	body, _, err := loadRevisionBody(ctx, r.pool, *x.revID)
	if err != nil {
		slog.Warn("loading revision for staleness check", "tool", x.toolName, "error", err)
		return tools.ExposedToolDef{}, false
	}
	if !scriptBodyHasFn(body, x.fnName) {
		r.markStale(ctx, caller.TenantID, x.id, "fn not in current revision")
		return tools.ExposedToolDef{}, false
	}

	schema := map[string]any{}
	if len(x.argsSchema) > 0 {
		_ = json.Unmarshal(x.argsSchema, &schema)
	}
	if _, ok := schema["type"]; !ok {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}

	appName, scriptName, fnName, toolName := x.appName, x.scriptName, x.fnName, x.toolName
	invoke := func(ctx context.Context, _ *tools.ExecContext, args map[string]any) (string, error) {
		return r.invokeExposedTool(ctx, caller, appName, scriptName, fnName, toolName, args)
	}
	return tools.ExposedToolDef{
		ToolName:       x.toolName,
		Description:    x.desc,
		ArgsSchema:     schema,
		VisibleToRoles: append([]string(nil), x.roles...),
		Invoke:         invoke,
	}, true
}

// markStale flips an exposed_tools row's is_stale bit. Best-effort — a
// race with the admin revoking/re-exposing is fine; we log and keep
// going so one broken row doesn't block the whole list.
func (r *exposedToolRunner) markStale(ctx context.Context, tenantID, toolID uuid.UUID, reason string) {
	slog.Info("marking exposed_tool stale", "tool_id", toolID, "reason", reason)
	if _, err := r.pool.Exec(ctx, `
		UPDATE exposed_tools SET is_stale = true
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, toolID); err != nil {
		slog.Warn("updating is_stale", "tool_id", toolID, "error", err)
	}
}

// invokeExposedTool routes an agent's exposed-tool invocation through
// invokeRunScript (the same keystone admins use via run_script). The
// caller here is the session's caller (possibly non-admin), so we bypass
// guardAdmin by constructing a shadow admin-flagged caller — visibility
// is already enforced upstream by the registry via VisibleToRoles.
//
// The response is trimmed to the raw result (or error message) so the
// LLM sees a clean tool output rather than the full admin-facing
// audit-heavy shape. Errors surface as text for the model to react to.
func (r *exposedToolRunner) invokeExposedTool(
	ctx context.Context,
	caller *services.Caller,
	appName, scriptName, fnName, toolName string,
	args map[string]any,
) (string, error) {
	deps := currentRunDeps
	if deps == nil || deps.Engine == nil {
		return "", errors.New("exposed tool runner: script engine not wired")
	}
	// Shadow IsAdmin=true just to pass guardAdmin. TenantID / UserID /
	// Roles are preserved so DB-level attribution (script_runs.caller_user_id)
	// stays accurate.
	shadow := *caller
	shadow.IsAdmin = true

	resp, err := invokeRunScript(ctx, r.pool, &shadow, deps, appName, scriptName, fnName, args, nil)
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

// scriptBodyHasFn does a cheap substring check for `def <name>(`. This
// matches the v0.1 plan: admins catch parse-level mistakes via
// update_script's own validation; staleness here is a safety net for
// "I updated the script and forgot to remove the exposure" type errors.
func scriptBodyHasFn(body, fnName string) bool {
	if fnName == "" {
		return false
	}
	return strings.Contains(body, "def "+fnName+"(")
}
