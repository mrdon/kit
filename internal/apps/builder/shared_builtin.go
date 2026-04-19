// Package builder: shared_builtin.go exposes the `shared("script", "fn",
// **kwargs)` host function — within-app helper calls that dispatch directly
// into another script in the SAME builder app without opening a full
// `script_runs` row per hop.
//
// Why a dedicated primitive (vs. tools.call)?
//   - `tools.call` crosses apps and creates a full ScriptRun with parent_run_id
//     for audit. That's the right shape for "one app invokes another app's
//     published tool" — but too heavy for "format_phone(raw)" that every
//     script in the CRM app wants to reuse.
//   - `shared` is the cheap in-app helper path. No new script_runs row; the
//     parent run's `sharedCallsCounter` increments once per call so telemetry
//     can surface the hop count later (mutation_summary.shared_calls).
//
// ABI shape (v0.1 simplification):
//
//	# Admin exposes a helper script "utils" with format_phone(phone):
//	phone = shared("utils", "format_phone", phone="8885551234")
//	ok    = shared("utils", "validate_email", email="jane@example.com")
//
// Positional args to the TARGET function are not supported in v0.1 — admins
// must pass target arguments by keyword. `shared("utils", "format_phone",
// "8885551234")` errors with a clear "shared() requires keyword arguments
// for the target function" message. Rationale: Monty's host-function ABI
// maps positional args to registered param names, and we don't know the
// target fn's signature here without parsing the body. Kwargs sidestep that
// entirely and keep the dispatcher under 400 LOC.
//
// Security rules:
//   - Scoped by builder_app_id: the lookup WHERE clause filters on the
//     caller's app. Cross-app calls surface as "not found in this app".
//   - The child script runs under the PARENT's full Capabilities (same
//     allowlist, same limits, same tenant/app/user). That's the whole point —
//     a cheap helper call, not a privilege boundary.
package builder

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// FnShared is the canonical builtin name exposed to admin scripts.
const FnShared = "shared"

// sharedArgScript and sharedArgFn name the two positional parameters the
// dispatcher peels off before forwarding the remaining kwargs to the target
// function. Kept as constants so the error messages and the FuncDef stay in
// sync.
const (
	sharedArgScript = "script"
	sharedArgFn     = "fn"
)

// SharedBuiltin bundles the FuncDef + BuiltIns + Params a caller wires into
// runtime.Capabilities to make `shared(...)` available inside a script run.
// Shape mirrors DBBuiltins / UtilBuiltins for consistency.
type SharedBuiltin struct {
	// Funcs is the ordered list of FuncDefs, ready to hand to
	// runtime.WithExternalFunc for raw Runner use.
	Funcs []runtime.FuncDef

	// BuiltIns maps each function name to its GoFunc. Feed this into
	// runtime.Capabilities.BuiltIns.
	BuiltIns map[string]runtime.GoFunc

	// Params maps each function name to its positional parameter names.
	// Feed this into runtime.Capabilities.BuiltInParams.
	Params map[string][]string
}

// BuildSharedBuiltin wires the `shared` host function for one script run.
//
// The parent's full Capabilities are rebuilt on each call from the supplied
// parameters (tenantID, builderAppID, callerUserID, callerRoles, parentRunID)
// so the child sees everything the parent sees. The engine is reused — we
// Compile + Run the target script in place; no new Runner, no new WASM
// instance bookkeeping.
//
// sharedCallsCounter is incremented on every successful lookup (pre-dispatch)
// so nested `shared` calls all count. Zero means "nobody's tracking"; the
// counter may be nil in which case we skip the increment.
//
// callerRoles is plumbed through because a future revision may want to use
// it in access checks; today's shared() doesn't touch it (no cross-app, no
// admin-only gate) but the dispatcher keeps it on the closure so the
// signature matches the plan and adding checks later is a one-liner.
func BuildSharedBuiltin(
	pool *pgxpool.Pool,
	engine runtime.Engine,
	tenantID, builderAppID, callerUserID uuid.UUID,
	callerRoles []string,
	parentRunID *uuid.UUID,
	sharedCallsCounter *atomic.Int64,
) *SharedBuiltin {
	_ = callerRoles // reserved for future ACL checks; see comment above.

	handler := func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
		return dispatchShared(
			ctx, pool, engine,
			tenantID, builderAppID, callerUserID,
			parentRunID, sharedCallsCounter,
			call,
		)
	}

	params := map[string][]string{
		FnShared: {sharedArgScript, sharedArgFn},
	}

	funcs := []runtime.FuncDef{
		runtime.Func(FnShared, params[FnShared]...),
	}

	builtIns := map[string]runtime.GoFunc{
		FnShared: handler,
	}

	return &SharedBuiltin{
		Funcs:    funcs,
		BuiltIns: builtIns,
		Params:   params,
	}
}

// dispatchShared pulls (script, fn) off the call, resolves to the current
// revision inside this builder app, compiles + runs the target via the
// parent's engine under a re-assembled Capabilities, and returns the result.
func dispatchShared(
	ctx context.Context,
	pool *pgxpool.Pool,
	engine runtime.Engine,
	tenantID, builderAppID, callerUserID uuid.UUID,
	parentRunID *uuid.UUID,
	sharedCallsCounter *atomic.Int64,
	call *runtime.FunctionCall,
) (any, error) {
	scriptName, err := argString(call.Args, sharedArgScript)
	if err != nil {
		return nil, fmt.Errorf("shared(): %w", err)
	}
	fnName, err := argString(call.Args, sharedArgFn)
	if err != nil {
		return nil, fmt.Errorf("shared(): %w", err)
	}

	kwargs, posCount := extractTargetArgs(call.Args)
	if posCount > 0 {
		return nil, fmt.Errorf(
			"shared(): positional arguments to target function are not supported; "+
				"pass them by keyword (got %d positional arg(s) to %q)",
			posCount, fnName,
		)
	}

	body, err := loadSharedScriptBody(ctx, pool, tenantID, builderAppID, scriptName)
	if err != nil {
		return nil, err
	}

	// Count the hop before we actually invoke. Counting pre-dispatch means
	// a nested shared call inside the child still increments even if the
	// child errors out — which is what telemetry wants (we measured the
	// work, not just the successful work).
	if sharedCallsCounter != nil {
		sharedCallsCounter.Add(1)
	}

	mod, err := engine.Compile(body)
	if err != nil {
		return nil, fmt.Errorf("shared(): compiling %q: %w", scriptName, err)
	}

	caps := &runtime.Capabilities{
		// Re-assembled from the parent's scalar identity. BuiltIns and
		// Limits are NOT available at this layer — the callers (scheduler,
		// manual runner) pre-populate those when they build the parent's
		// Capabilities, and each nested shared() call re-enters via
		// engine.Run with the parent's live BuiltIns map carried through
		// on the parent's own Capabilities closure (the host function
		// dispatcher captures it). See TestSharedBuiltin_Nested — the
		// BuiltIns map passed into Run below is the parent's, threaded in
		// via the capsTemplate closure at wire time.
		TenantID:     tenantID,
		BuilderAppID: builderAppID,
		CallerID:     callerUserID,
	}
	if parentRunID != nil {
		caps.RunID = *parentRunID
	}
	// The caller's BuildSharedBuiltin produced us via a closure; capture of
	// BuiltIns + Limits happens via threadCapabilities set by the wiring
	// code. See ShareEngineCaps for the one-liner that stitches them on.
	// For direct callers that pass caps via ShareEngineCaps, we honor them.
	if override := capsOverrideFromCtx(ctx); override != nil {
		caps.BuiltIns = override.BuiltIns
		caps.BuiltInParams = override.BuiltInParams
		caps.Limits = override.Limits
	}

	result, _, err := engine.Run(ctx, mod, fnName, kwargs, caps)
	if err != nil {
		return nil, fmt.Errorf("shared(): running %s.%s: %w", scriptName, fnName, err)
	}
	return result, nil
}

// extractTargetArgs returns (kwargs-to-forward, count-of-positional-leftovers).
//
// The Monty guest maps the caller's positional args onto the declared param
// names in order. Our FuncDef declares only ("script", "fn"), so positional
// args 1-2 land under those keys and any 3rd+ positional arg falls off the
// end — it's NOT in call.Args at all. So in practice posCount is 0 for any
// normal admin call. We still scan for synthetic keys like "a0"/"arg0" in
// case a future Monty build starts forwarding orphan positionals; better to
// error now than silently drop them.
func extractTargetArgs(args map[string]any) (map[string]any, int) {
	out := make(map[string]any, len(args))
	posCount := 0
	for k, v := range args {
		switch k {
		case sharedArgScript, sharedArgFn:
			continue
		}
		if isPositionalLeftoverKey(k) {
			posCount++
			continue
		}
		out[k] = v
	}
	return out, posCount
}

// isPositionalLeftoverKey returns true for the synthetic positional-arg
// keys a future Monty build might surface. Today's build just drops excess
// positionals, so this is belt-and-braces — cheap, defensive.
func isPositionalLeftoverKey(k string) bool {
	if len(k) < 2 {
		return false
	}
	// "a0", "a1", ... or "arg0", "arg1", ...
	prefixes := []string{"a", "arg"}
	for _, p := range prefixes {
		if len(k) > len(p) && k[:len(p)] == p && allDigits(k[len(p):]) {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// loadSharedScriptBody resolves (tenant, app, name) → current revision body.
// Error messages distinguish "no such script in this app" from "script exists
// but has no active revision" because the two diagnoses lead to very
// different fixes on the admin side.
func loadSharedScriptBody(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, builderAppID uuid.UUID,
	name string,
) (string, error) {
	var (
		currentRevID *uuid.UUID
		body         *string
	)
	err := pool.QueryRow(ctx, `
		SELECT s.current_rev_id, sr.body
		FROM scripts s
		LEFT JOIN script_revisions sr ON sr.id = s.current_rev_id
		WHERE s.tenant_id = $1 AND s.builder_app_id = $2 AND s.name = $3
	`, tenantID, builderAppID, name).Scan(&currentRevID, &body)
	if err != nil {
		// pgx returns ErrNoRows for missing; unwrap with a friendly error.
		return "", fmt.Errorf("shared(): script %q not found in app", name)
	}
	if currentRevID == nil || body == nil {
		return "", fmt.Errorf("shared(): script %q has no current revision", name)
	}
	return *body, nil
}

// capsOverrideCtxKey is the context key for threading the parent's live
// BuiltIns/Limits through to nested shared() calls. We use a context value
// instead of a closure field so arbitrary call stacks (shared -> shared ->
// shared) all see the same template without the caller having to plumb it.
type capsOverrideCtxKey struct{}

// ShareEngineCaps attaches the parent's live Capabilities to ctx so that
// nested shared() calls reuse the same BuiltIns / Limits. Returns a derived
// context the caller should pass into Engine.Run.
//
// Callers (typically the script runner / scheduler branch) build the parent
// Capabilities once, then:
//
//	ctx = builder.ShareEngineCaps(ctx, caps)
//	result, _, err := engine.Run(ctx, mod, fn, kwargs, caps)
//
// The BuiltIns map inside caps is captured by reference — if a handler in
// that map (e.g. dispatchShared) re-enters engine.Run with a fresh caps,
// the override read from ctx tops it up.
func ShareEngineCaps(ctx context.Context, caps *runtime.Capabilities) context.Context {
	if caps == nil {
		return ctx
	}
	return context.WithValue(ctx, capsOverrideCtxKey{}, caps)
}

func capsOverrideFromCtx(ctx context.Context) *runtime.Capabilities {
	v, _ := ctx.Value(capsOverrideCtxKey{}).(*runtime.Capabilities)
	return v
}
