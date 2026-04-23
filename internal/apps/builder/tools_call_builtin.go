// Package builder: tools_call_builtin.go bridges cross-script composition
// into the Monty runtime. Admin scripts call `tools_call("name", {...})`
// to invoke another tenant-exposed script function; the bridge looks up
// the exposed_tools row, enforces role visibility, opens a child
// script_runs row, and re-enters the engine with the target script's body.
//
// Flat-name convention: Monty's host ABI can't express `tools.call(...)`
// attribute dispatch, so we register a single `tools_call(name, args_dict)`
// host function. Scripts look like:
//
//	result = tools_call("lookup_contact", {"name": "Jane"})
//	if result:
//	    tools_call("add_contact_note", {"contact_id": result["_id"], ...})
//
// Role check: `visible_to_roles` is authoritative — admins are NOT
// automatically granted access. If admins need access, the exposing admin
// must include "admin" in visible_to_roles. This matches the Phase-3 plan
// and keeps the chain from silently escalating privilege.
//
// v0.1 limitation: ONE LEVEL OF NESTING. A script invoked via tools_call
// cannot itself call tools_call — the bridge refuses at the second level.
// This keeps re-entrancy simple and bounds worst-case recursion; v0.2 can
// lift this once the capability-plumbing story is firmed up.
// TODO(v0.2): support deeper nesting with proper privilege / quota roll-up.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// FnToolsCall is the canonical host-function name script authors see.
const FnToolsCall = "tools_call"

// RunDelta reports counters for the parent run to roll up.
type RunDelta struct {
	// ChildRuns is the count of tools_call invocations that fired during
	// the current parent run. The caller accumulates these into the
	// parent script_runs row's mutation_summary (or equivalent audit
	// surface) at end-of-run.
	ChildRuns int
}

// ToolsCallBuiltin bundles the FuncDefs, dispatcher, and Capabilities-ready
// maps for the tools_call host function. Shape matches the other *Builtins
// structs so wiring sites look uniform.
type ToolsCallBuiltin struct {
	Funcs    []runtime.FuncDef
	BuiltIns map[string]runtime.GoFunc
	Params   map[string][]string
}

// Engine is the narrow slice of runtime.Engine this bridge needs. Keeps
// the compile-time dependency explicit at the call site (and lets tests
// swap in a fake engine if the real Monty runner is unavailable).
type Engine interface {
	Compile(src string) (runtime.Module, error)
	Run(
		ctx context.Context,
		mod runtime.Module,
		fn string,
		kwargs map[string]any,
		caps *runtime.Capabilities,
	) (any, runtime.Metadata, error)
}

// BuildToolsCallBuiltin wires cross-script composition for one parent
// script run. Each call resolves the exposed_tools row for the current
// tenant, enforces visible_to_roles against the originating caller's
// roles, opens a child script_runs row, and re-enters the engine with
// the target script's current revision body.
//
// parentRunID threads the parent script_runs.id into the child row so
// the full audit chain is reconstructable; pass nil when this is a
// top-level host (no parent).
//
// runMetadata is a callback the bridge invokes once per successful
// dispatch so the parent run can accumulate a ChildRuns count. Pass nil
// to disable the callback — the bridge still works, the parent just
// won't see the rollup.
//
// childBuiltinsFactory builds the host-function surface for the CHILD
// script's run. The caller supplies it because the builder (which owns
// db/llm/action bundles) has the pool + services handles we need to
// avoid importing here. See NewChildBuiltinsFactory for a reference
// implementation the production wiring can use directly.
func BuildToolsCallBuiltin(
	pool *pgxpool.Pool,
	engine Engine,
	tenantID, callerUserID uuid.UUID,
	callerRoles []string,
	parentRunID *uuid.UUID,
	runMetadata func(delta RunDelta),
	childBuiltinsFactory ChildBuiltinsFactory,
) *ToolsCallBuiltin {
	// Track whether the current parent run is itself a child invocation.
	// v0.1 forbids a second nesting level; we detect it by checking the
	// parent's triggered_by at dispatch time (one DB hit per call — kept
	// inline because tools_call is expected to be rare).
	handler := func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
		if call.Name != FnToolsCall {
			return nil, fmt.Errorf("tools_call: unexpected name %q", call.Name)
		}
		name, err := argString(call.Args, "name")
		if err != nil {
			return nil, fmt.Errorf("tools_call: %w", err)
		}
		argsDict, err := argOptionalMap(call.Args, "args")
		if err != nil {
			return nil, fmt.Errorf("tools_call: %w", err)
		}
		if argsDict == nil {
			argsDict = map[string]any{}
		}

		// v0.1 one-level-nesting check: refuse if the parent run itself
		// came from a tools_call. This keeps the child's capability
		// surface exactly matching the author-supplied one, and bounds
		// worst-case re-entry depth at 2 (caller + one child).
		if parentRunID != nil {
			nested, err := isNestedCall(ctx, pool, *parentRunID)
			if err != nil {
				return nil, fmt.Errorf("tools_call: checking nesting: %w", err)
			}
			if nested {
				return nil, errors.New("tools_call: nested tools_call not supported in v0.1 (one level only)")
			}
		}

		tool, body, err := lookupExposedTool(ctx, pool, tenantID, name)
		if err != nil {
			return nil, err
		}
		// `visible_to_roles` is authoritative for tools_call — admins are
		// NOT automatically granted access. If an admin needs to call a
		// scoped tool, the exposing admin must include "admin" in
		// visible_to_roles. This matches the file header's Role-check
		// rule and keeps the chain from silently escalating privilege.
		if !rolesIntersect(callerRoles, tool.VisibleToRoles) {
			return nil, fmt.Errorf("tools_call: tool not accessible with current role: %s", name)
		}

		childRunID := uuid.New()
		if err := insertChildRun(ctx, pool, childRunID, tool, tenantID, callerUserID, parentRunID, argsDict); err != nil {
			return nil, fmt.Errorf("tools_call: starting child run: %w", err)
		}

		result, meta, runErr := runChild(ctx, engine, childBuiltinsFactory, tool, body, argsDict, tenantID, callerUserID, callerRoles, childRunID)

		if finishErr := finishChildRun(ctx, pool, tenantID, childRunID, result, meta, runErr); finishErr != nil {
			// Swallow finish errors when the script itself failed — the
			// script error is the signal the caller cares about. Bubble
			// finish errors only when the run succeeded, to avoid
			// silently losing the audit trail.
			if runErr == nil {
				return nil, fmt.Errorf("tools_call: recording child run: %w", finishErr)
			}
		}
		if runErr != nil {
			return nil, fmt.Errorf("tools_call %s: %w", name, runErr)
		}

		if runMetadata != nil {
			runMetadata(RunDelta{ChildRuns: 1})
		}
		return result, nil
	}

	params := map[string][]string{
		FnToolsCall: {"name", "args"},
	}
	funcs := []runtime.FuncDef{
		runtime.Func(FnToolsCall, params[FnToolsCall]...),
	}
	builtIns := map[string]runtime.GoFunc{
		FnToolsCall: handler,
	}
	return &ToolsCallBuiltin{
		Funcs:    funcs,
		BuiltIns: builtIns,
		Params:   params,
	}
}

// exposedToolLookup is the subset of exposed_tools + script fields the
// bridge needs to open a child run and reject stale/unpublished tools.
type exposedToolLookup struct {
	ToolID         uuid.UUID
	ScriptID       uuid.UUID
	BuilderAppID   uuid.UUID
	RevisionID     uuid.UUID
	FnName         string
	VisibleToRoles []string
	IsStale        bool
}

// lookupExposedTool fetches the exposed tool + its backing script's
// current revision body in one round-trip. Returns a friendly error for
// each of: tool missing, tool stale, script missing a current revision.
func lookupExposedTool(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	name string,
) (exposedToolLookup, string, error) {
	const q = `
		SELECT et.id, et.script_id, et.fn_name, et.visible_to_roles, et.is_stale,
		       s.builder_app_id, s.current_rev_id, sr.id, sr.body
		FROM exposed_tools et
		JOIN scripts s ON s.id = et.script_id
		LEFT JOIN script_revisions sr ON sr.id = s.current_rev_id
		WHERE et.tenant_id = $1 AND et.tool_name = $2
	`
	var (
		out          exposedToolLookup
		currentRevID *uuid.UUID
		revID        *uuid.UUID
		body         *string
	)
	err := pool.QueryRow(ctx, q, tenantID, name).Scan(
		&out.ToolID, &out.ScriptID, &out.FnName, &out.VisibleToRoles, &out.IsStale,
		&out.BuilderAppID, &currentRevID, &revID, &body,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return exposedToolLookup{}, "", fmt.Errorf("tools_call: tool not found: %s", name)
		}
		return exposedToolLookup{}, "", fmt.Errorf("tools_call: lookup %s: %w", name, err)
	}
	if out.IsStale {
		return exposedToolLookup{}, "", fmt.Errorf("tools_call: tool %s is stale (backing script/function unavailable)", name)
	}
	if currentRevID == nil || revID == nil || body == nil {
		return exposedToolLookup{}, "", fmt.Errorf("tools_call: tool %s: script has no current revision", name)
	}
	out.RevisionID = *revID
	return out, *body, nil
}

// insertChildRun opens the child script_runs row. Kept as its own helper
// so the caller's main flow reads cleanly; no transaction is needed — if
// finishChildRun never runs (e.g. panic) we leave an orphan 'running' row
// which is fine for audit purposes (caller can still see something was
// started and never finished).
func insertChildRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	childRunID uuid.UUID,
	tool exposedToolLookup,
	tenantID, callerUserID uuid.UUID,
	parentRunID *uuid.UUID,
	argsDict map[string]any,
) error {
	argsJSON, err := json.Marshal(argsDict)
	if err != nil {
		argsJSON = []byte("{}")
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO script_runs (
			id, tenant_id, script_id, revision_id, fn_called, args,
			status, started_at, triggered_by, parent_run_id, caller_user_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'running', now(), 'tools_call', $7, $8)
	`,
		childRunID, tenantID, tool.ScriptID, tool.RevisionID, tool.FnName, argsJSON,
		parentRunID, callerUserID,
	)
	return err
}

// runChild compiles the child module and invokes fn_name(**args) under a
// fresh Capabilities bundle built via the caller-supplied factory. Any
// result/error produced here is the caller's final answer — stale, not-
// found, and role-visibility are already filtered out before we land.
func runChild(
	ctx context.Context,
	engine Engine,
	factory ChildBuiltinsFactory,
	tool exposedToolLookup,
	body string,
	argsDict map[string]any,
	tenantID, callerUserID uuid.UUID,
	callerRoles []string,
	childRunID uuid.UUID,
) (any, runtime.Metadata, error) {
	mod, err := engine.Compile(body)
	if err != nil {
		return nil, runtime.Metadata{}, fmt.Errorf("compile child: %w", err)
	}
	// Child capabilities: same tenant + caller identity; child's own
	// RunID; the target script's BuilderAppID (cross-app is the point).
	// Host-function surface comes from the factory.
	caps := &runtime.Capabilities{
		RunID:        childRunID,
		TenantID:     tenantID,
		BuilderAppID: tool.BuilderAppID,
		CallerID:     callerUserID,
	}
	if factory != nil {
		builtIns, params := factory(tenantID, tool.BuilderAppID, callerUserID, callerRoles, childRunID)
		caps.BuiltIns = builtIns
		caps.BuiltInParams = params
	}
	return engine.Run(ctx, mod, tool.FnName, argsDict, caps)
}

// finishChildRun closes the child script_runs row with completed/error
// status, duration, result, and any error message. Best-effort — logged
// by the caller if we fail here (only surfaced as the return error when
// the script itself succeeded).
func finishChildRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	childRunID uuid.UUID,
	result any,
	meta runtime.Metadata,
	runErr error,
) error {
	status := RunStatusCompleted
	var errMsg string
	if runErr != nil {
		status = RunStatusError
		errMsg = runErr.Error()
	}
	var resultJSON []byte
	if runErr == nil {
		resultJSON, _ = json.Marshal(result)
	}
	duration := meta.DurationMs
	_, err := pool.Exec(ctx, `
		UPDATE script_runs
		SET status = $1,
		    finished_at = now(),
		    duration_ms = $2,
		    result = $3,
		    error = $4
		WHERE tenant_id = $5 AND id = $6
	`, status, duration, resultJSON, nullIfEmpty(errMsg), tenantID, childRunID)
	return err
}

// isNestedCall returns true when the parent script_runs row was itself
// triggered by a tools_call. This is how we enforce the v0.1
// one-level-nesting rule without adding state to the builtin itself.
func isNestedCall(ctx context.Context, pool *pgxpool.Pool, parentRunID uuid.UUID) (bool, error) {
	var triggeredBy *string
	err := pool.QueryRow(ctx, `
		SELECT triggered_by FROM script_runs WHERE id = $1
	`, parentRunID).Scan(&triggeredBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Orphan parent id — treat as not-nested and let the call
			// proceed; audit will still record the child.
			return false, nil
		}
		return false, err
	}
	if triggeredBy == nil {
		return false, nil
	}
	return *triggeredBy == TriggerToolsCall, nil
}

// rolesIntersect reports whether any role in want matches any role in
// have, using case-sensitive string comparison. An empty want slice
// means "no roles granted" — default-deny to match the rest of Kit's
// scope semantics.
func rolesIntersect(have, want []string) bool {
	if len(want) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(have))
	for _, r := range have {
		set[r] = struct{}{}
	}
	for _, r := range want {
		if _, ok := set[r]; ok {
			return true
		}
	}
	return false
}

// nullIfEmpty returns nil for empty string so UPDATEs can distinguish
// "no error" (NULL) from "error was the empty string" (which shouldn't
// happen, but belt-and-braces).
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ChildBuiltinsFactory builds the host-function surface a child script
// gets when invoked via tools_call. The factory is supplied by the
// caller (typically the builder app wiring) because only it has the
// services / pool handles needed to build the db/llm/action bundles.
//
// Params map must be safe to use as Capabilities.BuiltInParams; the
// BuiltIns map likewise as Capabilities.BuiltIns. A nil return value
// is allowed when the caller wants the child to run with no host
// surface (pure-Python only).
type ChildBuiltinsFactory func(
	tenantID, builderAppID, callerUserID uuid.UUID,
	callerRoles []string,
	childRunID uuid.UUID,
) (map[string]runtime.GoFunc, map[string][]string)
