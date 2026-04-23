// Package builder: capabilities.go assembles the Phase 3 builtin bundles
// (db_*, actions, llm_*, utilities, tools_call, shared) into one
// runtime.Capabilities struct that app_run_script hands to the engine. It also
// returns a ScriptRunCounters handle so the post-run UPDATE can populate
// the script_runs row with mutation_summary, tokens_used, cost_cents, etc.
//
// The orchestrator is deliberately the ONLY place in the codebase that
// knows about every bundle at once; individual bundles continue to live
// in their own files under db_builtins.go / action_builtins.go / etc.
// If any bundle grows new builtins we just need to rebuild the combined
// map here — the rest of the app_run_script flow stays unchanged.
//
// Key disjointness: every bundle namespaces its names (db_*, llm_*, plus
// action verbs like create_todo, tools_call, shared). Combining them
// should never clash; we verify with an explicit panic at assembly time
// so a future rename can't accidentally shadow an existing builtin.
package builder

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// ScriptRunParams carries the identity + scope a single script invocation
// needs. The app_run_script handler builds one of these from the caller's
// MCP/agent context and the loaded app; downstream (tools_call,
// shared) receives only what its narrower surface needs, but the parent
// orchestrator starts from this full shape.
type ScriptRunParams struct {
	TenantID       uuid.UUID
	BuilderAppID   uuid.UUID
	CallerUserID   uuid.UUID
	CallerRoles    []string
	CallerTimezone string
	RunID          uuid.UUID
	ParentRunID    *uuid.UUID
	Limits         runtime.Limits
	MaxDBCalls     int
}

// ScriptRunCounters exposes the per-run counters the orchestrator captures
// at assembly time. After the script runs, app_run_script calls Snapshot to
// pull a map suitable for json-marshal into script_runs.mutation_summary,
// and reads TokensUsed / CostCents separately to populate the dedicated
// script_runs columns.
type ScriptRunCounters struct {
	// actions is the ActionBuiltins bundle so we can query its mutation
	// counters at end-of-run. The fields are plain ints (not atomic)
	// because action dispatch is serial per-run.
	actions *ActionBuiltins

	// db is the DBBuiltins bundle, whose MutationSummary tracks the
	// app_items inserts/updates/deletes a script made. Snapshot folds
	// both counters together so mutation_summary reports every row-level
	// change the run performed, not just the Kit-native actions.
	db *DBBuiltins

	// sharedCalls is the shared() hop counter. Incremented on every
	// successful lookup inside the SharedBuiltin dispatcher.
	sharedCalls *atomic.Int64

	// childRuns is the tools_call child-run counter. Incremented via the
	// runMetadata callback once per successful dispatch.
	childRuns *atomic.Int64

	// dbCallsLeft returns remaining db_* quota; -1 means unlimited.
	dbCallsLeft func() int

	// llm is the LLMBuiltins bundle; TokensUsed / CostCents are atomic
	// int64 accessors.
	llm *LLMBuiltins
}

// Snapshot returns a map shaped for json.Marshal into
// script_runs.mutation_summary. The keys match the MutationSummary struct
// fields plus the two extra counters (shared_calls, child_runs) that
// aren't part of the canonical {inserts,updates,deletes} shape but are
// still useful audit signal.
func (c *ScriptRunCounters) Snapshot() map[string]any {
	out := map[string]any{
		"inserts":      0,
		"updates":      0,
		"deletes":      0,
		"shared_calls": int64(0),
		"child_runs":   int64(0),
	}
	if c.actions != nil {
		m := c.actions.MutationSummary()
		out["inserts"] = m["inserts"]
		out["updates"] = m["updates"]
		out["deletes"] = m["deletes"]
	}
	if c.db != nil {
		m := c.db.MutationSummary()
		out["inserts"] = out["inserts"].(int) + m["inserts"]
		out["updates"] = out["updates"].(int) + m["updates"]
		out["deletes"] = out["deletes"].(int) + m["deletes"]
	}
	if c.sharedCalls != nil {
		out["shared_calls"] = c.sharedCalls.Load()
	}
	if c.childRuns != nil {
		out["child_runs"] = c.childRuns.Load()
	}
	return out
}

// TokensUsed returns the total input+output tokens consumed by llm_* calls
// during this run. Safe to call any time; reads an atomic counter.
func (c *ScriptRunCounters) TokensUsed() int64 {
	if c.llm == nil {
		return 0
	}
	return c.llm.TokensUsed()
}

// CostCents returns the cumulative rounded cost in cents across all llm_*
// calls this run.
func (c *ScriptRunCounters) CostCents() int64 {
	if c.llm == nil {
		return 0
	}
	return c.llm.CostCents()
}

// DBCallsLeft returns the remaining db_* quota, or -1 if unlimited.
func (c *ScriptRunCounters) DBCallsLeft() int {
	if c.dbCallsLeft == nil {
		return -1
	}
	return c.dbCallsLeft()
}

// BuildScriptCapabilities assembles every Phase 3 bundle for one script
// run and returns the Capabilities + a ScriptRunCounters handle.
//
// Bundle stitching:
//   - db_builtins: routes into ItemService. maxDBCalls caps per-run
//     db_* calls; 0 or negative means unlimited.
//   - action_builtins: routes into services.Services + slack client.
//   - llm_builtins: routes into Anthropic sender with per-tenant cap.
//   - util_builtins: clock / log helpers.
//   - tools_call_builtin: cross-app composition via exposed_tools.
//   - shared_builtin: within-app helper calls.
//
// slack may be nil (tests, dry runs); send_slack_message will error at
// dispatch time in that case rather than silently no-op.
//
// childBuiltinsFactory wires the host-function surface for any child runs
// opened via tools_call. The orchestrator uses a closure over its own
// bundle-building logic so children see the same db/action/llm surface as
// the parent (re-scoped to the child's BuilderAppID).
func BuildScriptCapabilities(
	_ context.Context,
	pool *pgxpool.Pool,
	svc *services.Services,
	sender Sender,
	slack *kitslack.Client,
	engine runtime.Engine,
	params ScriptRunParams,
) (*runtime.Capabilities, *ScriptRunCounters, error) {
	runIDCopy := params.RunID
	runIDPtr := &runIDCopy

	itemSvc := NewItemService(pool)

	db := BuildDBBuiltins(
		itemSvc,
		params.TenantID, params.BuilderAppID, params.CallerUserID,
		runIDPtr,
		params.MaxDBCalls,
	)
	actions := BuildActionBuiltins(
		pool, svc, slack,
		params.TenantID, params.BuilderAppID, params.CallerUserID,
		runIDPtr,
	)
	llm := BuildLLMBuiltins(pool, sender, params.TenantID, runIDPtr)
	util := BuildUtilBuiltins(pool, params.TenantID, runIDPtr, params.CallerTimezone)
	identity := BuildIdentityBuiltins(
		pool,
		params.TenantID, params.CallerUserID,
		params.CallerRoles, params.CallerTimezone,
	)

	sharedCalls := &atomic.Int64{}
	shared := BuildSharedBuiltin(
		pool, engine,
		params.TenantID, params.BuilderAppID, params.CallerUserID,
		params.CallerRoles,
		runIDPtr,
		sharedCalls,
	)

	childRuns := &atomic.Int64{}
	// childBuiltinsFactory reuses the same bundle configuration for every
	// child run, rebinding the app-id to the target tool's builder_app.
	// It intentionally excludes tools_call itself — the v0.1 policy is one
	// level of nesting, so the child gets db/action/llm/util/shared but
	// cannot call tools_call again.
	childFactory := func(
		tenantID, builderAppID, callerUserID uuid.UUID,
		callerRoles []string,
		childRunID uuid.UUID,
	) (map[string]runtime.GoFunc, map[string][]string) {
		childRunIDCopy := childRunID
		childRunIDPtr := &childRunIDCopy

		childDB := BuildDBBuiltins(
			itemSvc,
			tenantID, builderAppID, callerUserID,
			childRunIDPtr,
			params.MaxDBCalls,
		)
		childActions := BuildActionBuiltins(
			pool, svc, slack,
			tenantID, builderAppID, callerUserID,
			childRunIDPtr,
		)
		childLLM := BuildLLMBuiltins(pool, sender, tenantID, childRunIDPtr)
		childUtil := BuildUtilBuiltins(pool, tenantID, childRunIDPtr, params.CallerTimezone)
		childIdentity := BuildIdentityBuiltins(
			pool,
			tenantID, callerUserID,
			callerRoles, params.CallerTimezone,
		)
		childShared := BuildSharedBuiltin(
			pool, engine,
			tenantID, builderAppID, callerUserID,
			callerRoles, childRunIDPtr, sharedCalls,
		)

		combined := map[string]runtime.GoFunc{}
		combinedParams := map[string][]string{}
		mergeBuiltIns(combined, combinedParams, childDB.BuiltIns, childDB.Params, "db")
		mergeBuiltIns(combined, combinedParams, childActions.BuiltIns, childActions.Params, "action")
		mergeBuiltIns(combined, combinedParams, childLLM.BuiltIns, childLLM.Params, "llm")
		mergeBuiltIns(combined, combinedParams, childUtil.BuiltIns, childUtil.Params, "util")
		mergeBuiltIns(combined, combinedParams, childIdentity.BuiltIns, childIdentity.Params, "identity")
		mergeBuiltIns(combined, combinedParams, childShared.BuiltIns, childShared.Params, "shared")
		return combined, combinedParams
	}

	toolsCall := BuildToolsCallBuiltin(
		pool, engine,
		params.TenantID, params.CallerUserID,
		params.CallerRoles,
		runIDPtr,
		func(delta RunDelta) { childRuns.Add(int64(delta.ChildRuns)) },
		childFactory,
	)

	// Collect everything into one BuiltIns/Params map. mergeBuiltIns panics
	// if two bundles register the same name — that would be a programming
	// bug, not a script-level error, so failing fast is correct.
	combined := map[string]runtime.GoFunc{}
	combinedParams := map[string][]string{}
	if err := mergeBuiltInsSafe(combined, combinedParams, db.BuiltIns, db.Params, "db"); err != nil {
		return nil, nil, err
	}
	if err := mergeBuiltInsSafe(combined, combinedParams, actions.BuiltIns, actions.Params, "action"); err != nil {
		return nil, nil, err
	}
	if err := mergeBuiltInsSafe(combined, combinedParams, llm.BuiltIns, llm.Params, "llm"); err != nil {
		return nil, nil, err
	}
	if err := mergeBuiltInsSafe(combined, combinedParams, util.BuiltIns, util.Params, "util"); err != nil {
		return nil, nil, err
	}
	if err := mergeBuiltInsSafe(combined, combinedParams, identity.BuiltIns, identity.Params, "identity"); err != nil {
		return nil, nil, err
	}
	if err := mergeBuiltInsSafe(combined, combinedParams, toolsCall.BuiltIns, toolsCall.Params, "tools_call"); err != nil {
		return nil, nil, err
	}
	if err := mergeBuiltInsSafe(combined, combinedParams, shared.BuiltIns, shared.Params, "shared"); err != nil {
		return nil, nil, err
	}

	caps := &runtime.Capabilities{
		BuiltIns:      combined,
		BuiltInParams: combinedParams,
		Limits:        params.Limits,
		RunID:         params.RunID,
		TenantID:      params.TenantID,
		BuilderAppID:  params.BuilderAppID,
		CallerID:      params.CallerUserID,
	}

	counters := &ScriptRunCounters{
		actions:     actions,
		db:          db,
		sharedCalls: sharedCalls,
		childRuns:   childRuns,
		dbCallsLeft: db.CallsRemaining,
		llm:         llm,
	}
	return caps, counters, nil
}

// mergeBuiltInsSafe copies each (name, fn) pair into dst, returning an
// error if a name already exists. Params get the same treatment, though
// they never collide independently of their BuiltIns counterpart.
func mergeBuiltInsSafe(
	dst map[string]runtime.GoFunc,
	dstParams map[string][]string,
	src map[string]runtime.GoFunc,
	srcParams map[string][]string,
	bundle string,
) error {
	for name, fn := range src {
		if _, exists := dst[name]; exists {
			return fmt.Errorf("builder capabilities: builtin %q registered twice (bundle %q collides)", name, bundle)
		}
		dst[name] = fn
	}
	for name, ps := range srcParams {
		if _, exists := dstParams[name]; exists && !stringSlicesEqual(dstParams[name], ps) {
			return fmt.Errorf("builder capabilities: params %q registered twice with different shapes (bundle %q)", name, bundle)
		}
		dstParams[name] = ps
	}
	return nil
}

// mergeBuiltIns is the panic-on-collision variant used from the child
// factory, which is invoked at script-run time and should never hit a
// collision (the parent's assembly already caught any shape bugs). If
// somehow it does, panicking is the right move — the process is in a
// bad state.
func mergeBuiltIns(
	dst map[string]runtime.GoFunc,
	dstParams map[string][]string,
	src map[string]runtime.GoFunc,
	srcParams map[string][]string,
	bundle string,
) {
	if err := mergeBuiltInsSafe(dst, dstParams, src, srcParams, bundle); err != nil {
		panic(err)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
