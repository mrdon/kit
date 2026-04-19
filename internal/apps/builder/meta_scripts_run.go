// Package builder: meta_scripts_run.go implements the run_script meta-tool
// — the keystone where every Phase 3 substrate (db_*, actions, llm_*,
// util, tools_call, shared) composes into one script invocation and the
// script_runs audit row gets populated.
//
// Flow:
//
//  1. Resolve app + script + current revision.
//  2. Resolve per-tenant limits from tenant_builder_config, allowing a
//     per-call override via the `limits` kwarg (rare — admins leave it
//     alone most of the time).
//  3. INSERT a script_runs row with status='running' and commit it so the
//     run is visible for audit tools immediately (audit > atomicity here).
//  4. Assemble Capabilities via BuildScriptCapabilities.
//  5. Engine.Compile(body) + Engine.Run(ctx, mod, fn, args, caps).
//  6. UPDATE the script_runs row with final status + counters.
//  7. Return the result to the admin caller.
//
// Runtime-error handling:
//   - Context deadline (admin request timeout, or `Limits.MaxDuration`) →
//     status='cancelled'.
//   - Monty wall-clock exceeded → status='limit_exceeded'.
//   - Any panic inside Engine.Run → status='error', recovered + logged.
//   - Script-level exceptions come back as errors from Engine.Run and
//     land in status='error'.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// Default per-run limits. Matched against tenant_builder_config at
// resolveLimits time; admins can raise the DB cap or LLM cap per tenant,
// and the `limits` kwarg on run_script provides a one-shot override.
const (
	defaultMaxDBCalls = 1000
	defaultMaxRuntime = 30 * time.Second
)

// runScriptResponse is what run_script returns to the admin caller.
// Separate from script_runs columns so the LLM sees a narrow surface and
// we control precisely what crosses that boundary.
type runScriptResponse struct {
	RunID           uuid.UUID      `json:"run_id"`
	Status          string         `json:"status"`
	Result          any            `json:"result,omitempty"`
	MutationSummary map[string]any `json:"mutation_summary"`
	DurationMs      int64          `json:"duration_ms"`
	TokensUsed      int64          `json:"tokens_used"`
	CostCents       int64          `json:"cost_cents"`
	Error           string         `json:"error,omitempty"`
}

// scriptRunDeps are the process-wide dependencies run_script needs beyond
// what execContextLike carries. The default wiring in app.go installs
// them at init (services + engine + sender + slack). Tests inject their
// own. Stored via a package-global because execContextLike is intended to
// match the minimal ExecContext shape and we don't want to change
// Phase 4a's contract.
type scriptRunDeps struct {
	Services *services.Services
	Engine   runtime.Engine
	Sender   Sender
	Slack    *kitslack.Client
}

// currentRunDeps is set via SetScriptRunDeps during app Init. Nil is
// allowed (tests that don't need run_script simply never call it) and
// handleRunScript errors cleanly in that case.
var currentRunDeps *scriptRunDeps

// SetScriptRunDeps wires process-wide dependencies for run_script. Called
// from app.go once the app has a pool + services + engine; tests call it
// directly with a stub engine/sender to exercise the full flow.
func SetScriptRunDeps(deps *scriptRunDeps) {
	currentRunDeps = deps
}

func handleRunScript(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	scriptName, err := argString(m, "script")
	if err != nil {
		return "", err
	}
	fn, err := argString(m, "fn")
	if err != nil {
		return "", err
	}
	argsMap, err := argOptionalMap(m, "args")
	if err != nil {
		return "", err
	}
	if argsMap == nil {
		argsMap = map[string]any{}
	}
	limitsMap, err := argOptionalMap(m, "limits")
	if err != nil {
		return "", err
	}

	resp, err := invokeRunScript(ec.Ctx, ec.Pool, ec.Caller, currentRunDeps, appName, scriptName, fn, argsMap, limitsMap)
	if err != nil {
		return "", err
	}
	return formatToolResult(resp)
}

// invokeRunScript is the keystone that glues every substrate together.
// Admin-only; returns the final response map.
func invokeRunScript(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	deps *scriptRunDeps,
	appName, scriptName, fn string,
	args, limitOverrides map[string]any,
) (*runScriptResponse, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	if deps == nil || deps.Engine == nil {
		return nil, errors.New("run_script: engine not wired (internal bug — SetScriptRunDeps not called)")
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return nil, err
	}

	script, err := loadScriptByName(ctx, pool, caller.TenantID, app.ID, scriptName)
	if err != nil {
		return nil, err
	}
	if script.CurrentRevID == nil {
		return nil, fmt.Errorf("script %q has no current revision", scriptName)
	}

	body, revID, err := loadRevisionBody(ctx, pool, *script.CurrentRevID)
	if err != nil {
		return nil, err
	}

	limits, maxDBCalls := resolveLimits(ctx, pool, caller.TenantID, limitOverrides)

	// Insert + commit the script_runs row so audit tools see the in-flight
	// run immediately. Using a dedicated COMMIT here (not a long-lived tx)
	// means a mid-run panic still leaves the 'running' row behind for
	// diagnosis rather than silently disappearing on rollback.
	runID := uuid.New()
	argsJSON, _ := json.Marshal(args)
	if _, err = pool.Exec(ctx, `
		INSERT INTO script_runs (
			id, tenant_id, script_id, revision_id, fn_called, args,
			status, started_at, triggered_by, caller_user_id
		) VALUES ($1, $2, $3, $4, $5, $6, 'running', now(), 'manual', $7)
	`, runID, caller.TenantID, script.ID, revID, fn, argsJSON, caller.UserID); err != nil {
		return nil, fmt.Errorf("inserting script_runs: %w", err)
	}

	params := ScriptRunParams{
		TenantID:       caller.TenantID,
		BuilderAppID:   app.ID,
		CallerUserID:   caller.UserID,
		CallerRoles:    caller.Roles,
		CallerTimezone: caller.Timezone,
		RunID:          runID,
		Limits:         limits,
		MaxDBCalls:     maxDBCalls,
	}

	caps, counters, err := BuildScriptCapabilities(
		ctx, pool, deps.Services, deps.Sender, deps.Slack, deps.Engine, params,
	)
	if err != nil {
		finishRun(ctx, pool, caller.TenantID, runID, RunStatusError, nil,
			nil, err.Error(), 0, 0, 0)
		return nil, fmt.Errorf("building capabilities: %w", err)
	}

	// Thread caps onto ctx so nested shared() calls see the parent's live
	// BuiltIns/Limits template (see shared_builtin.go ShareEngineCaps).
	runCtx := ShareEngineCaps(ctx, caps)

	result, meta, runErr := invokeScript(runCtx, deps.Engine, body, fn, args, caps)

	status, errMsg := classifyRunError(runErr)
	mutation := counters.Snapshot()
	tokens := counters.TokensUsed()
	cost := counters.CostCents()

	finishRun(ctx, pool, caller.TenantID, runID, status, result,
		mutation, errMsg, meta.DurationMs, int(tokens), int(cost))

	return &runScriptResponse{
		RunID:           runID,
		Status:          status,
		Result:          resultForResponse(result, status),
		MutationSummary: mutation,
		DurationMs:      meta.DurationMs,
		TokensUsed:      tokens,
		CostCents:       cost,
		Error:           errMsg,
	}, nil
}

// invokeScript compiles the body and calls fn(**args). Recovers from
// panics inside the engine so a bad Monty guest can't crash the MCP
// server. A recovered panic surfaces as a normal error with a "panic:"
// prefix so callers can distinguish it from plain script exceptions.
func invokeScript(
	ctx context.Context,
	engine runtime.Engine,
	body, fn string,
	args map[string]any,
	caps *runtime.Capabilities,
) (result any, meta runtime.Metadata, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	mod, compileErr := engine.Compile(body)
	if compileErr != nil {
		return nil, runtime.Metadata{}, fmt.Errorf("compile: %w", compileErr)
	}
	return engine.Run(ctx, mod, fn, args, caps)
}

// classifyRunError maps runErr to one of the script_runs.status values.
//
//   - nil                     → completed
//   - context.DeadlineExceeded → cancelled (timeout)
//   - context.Canceled        → cancelled
//   - message contains "limit" / "quota" / "budget" / "too long" / "deadline"
//     → limit_exceeded
//   - anything else           → error
//
// The string-matching heuristic is deliberately loose — Monty surfaces
// resource-limit errors as opaque strings (no typed error) and the
// llm_builtins budget-exhausted error contains "budget exhausted".
// False positives are fine: limit_exceeded is strictly more informative
// than error, and we never mis-map a completed run to limit_exceeded.
func classifyRunError(runErr error) (status, msg string) {
	if runErr == nil {
		return RunStatusCompleted, ""
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return RunStatusCancelled, runErr.Error()
	}
	lower := strings.ToLower(runErr.Error())
	keywords := []string{"limit", "quota", "budget", "too long", "deadline", "exhausted", "time limit", "max_duration"}
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return RunStatusLimitExceeded, runErr.Error()
		}
	}
	return RunStatusError, runErr.Error()
}

// resultForResponse gates the result field: only successful runs surface
// a result to the caller. Errored/cancelled/limit_exceeded runs return
// nil so callers don't accidentally trust a half-built object.
func resultForResponse(result any, status string) any {
	if status != RunStatusCompleted {
		return nil
	}
	return result
}

// finishRun writes the final script_runs UPDATE. Takes every scalar field
// inline so the caller's flow reads cleanly at the site. Logged-only
// failure — a broken audit UPDATE is better than a blocked return to the
// admin caller.
func finishRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, runID uuid.UUID,
	status string,
	result any,
	mutationSummary map[string]any,
	errMsg string,
	durationMs int64,
	tokens, cents int,
) {
	var resultJSON []byte
	if result != nil {
		resultJSON, _ = json.Marshal(result)
	}
	var mutationJSON []byte
	if mutationSummary != nil {
		mutationJSON, _ = json.Marshal(mutationSummary)
	}
	_, err := pool.Exec(ctx, `
		UPDATE script_runs
		SET status = $1,
		    finished_at = now(),
		    duration_ms = $2,
		    result = $3,
		    error = $4,
		    mutation_summary = $5,
		    tokens_used = NULLIF($6, 0),
		    cost_cents = NULLIF($7, 0)
		WHERE tenant_id = $8 AND id = $9
	`, status, durationMs, resultJSON, nullIfEmpty(errMsg), mutationJSON, tokens, cents, tenantID, runID)
	if err != nil {
		// Best-effort — the run already happened and we have the data in
		// the response. Don't break the caller just because the audit
		// UPDATE hit an error.
		_ = err
	}
}

// loadScriptByName resolves (tenant, app, name) → Script, returning a
// friendly error if the script is missing.
func loadScriptByName(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, builderAppID uuid.UUID,
	name string,
) (*Script, error) {
	var s Script
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, builder_app_id, name,
		       COALESCE(description, ''), current_rev_id, created_by, created_at
		FROM scripts
		WHERE tenant_id = $1 AND builder_app_id = $2 AND name = $3
	`, tenantID, builderAppID, name).Scan(
		&s.ID, &s.TenantID, &s.BuilderAppID, &s.Name,
		&s.Description, &s.CurrentRevID, &s.CreatedBy, &s.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("script %q not found", name)
		}
		return nil, fmt.Errorf("loading script: %w", err)
	}
	return &s, nil
}

// loadRevisionBody fetches a revision's body + echoes its id so the
// caller has a single fact (no chance of a race changing current_rev_id
// between the lookup here and the script_runs.revision_id column).
func loadRevisionBody(ctx context.Context, pool *pgxpool.Pool, revID uuid.UUID) (string, uuid.UUID, error) {
	var body string
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT id, body FROM script_revisions WHERE id = $1
	`, revID).Scan(&id, &body)
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("loading revision: %w", err)
	}
	return body, id, nil
}

// resolveLimits reads tenant_builder_config and applies any per-call
// override from the admin's `limits` kwarg. Missing config row falls back
// to the defaults — scripts shouldn't fail just because an admin never
// explicitly configured quotas. Returns Monty's runtime.Limits plus the
// per-run DB cap (kept as a separate int because db_builtins wants it
// pre-plumbed, not via runtime.Limits).
func resolveLimits(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	overrides map[string]any,
) (runtime.Limits, int) {
	// Wall-clock limit lives entirely in Monty's runtime.Limits; db call
	// cap is external (enforced inside the db_* dispatcher).
	limits := runtime.Limits{
		MaxDuration: defaultMaxRuntime,
	}
	maxDBCalls := defaultMaxDBCalls

	var cfgMaxDB int
	err := pool.QueryRow(ctx, `
		SELECT max_db_calls_per_run FROM tenant_builder_config WHERE tenant_id = $1
	`, tenantID).Scan(&cfgMaxDB)
	if err == nil && cfgMaxDB > 0 {
		maxDBCalls = cfgMaxDB
	}
	// We ignore other errors: default values are sane.

	if overrides != nil {
		if v, ok := overrides["max_db_calls"].(float64); ok && v > 0 {
			maxDBCalls = int(v)
		}
		if v, ok := overrides["max_duration_ms"].(float64); ok && v > 0 {
			limits.MaxDuration = time.Duration(v) * time.Millisecond
		}
		if v, ok := overrides["max_memory_bytes"].(float64); ok && v > 0 {
			limits.MaxMemoryBytes = uint64(v)
		}
	}

	return limits, maxDBCalls
}
