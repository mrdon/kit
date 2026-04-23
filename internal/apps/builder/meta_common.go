// Package builder: meta_common.go collects the shared helpers every Phase 4
// meta-tool (create_app, add_script, app_schedule_script, expose_tool, ...) reaches
// for. Parsing the tool input JSON, enforcing admin-only guardrails, looking up
// a builder_app by (tenant_id, name), and formatting the JSON result all live
// here so the per-category files (meta_apps.go, meta_scripts.go, ...) can stay
// focused on the SQL + shape that belongs to them.
//
// Why a separate file:
//   - Meta-tools land across multiple subtasks (4a/4b/4c/4d/4e). Without a
//     shared helpers file each subtask would re-invent its own arg parsing and
//     admin guard — divergent error messages, inconsistent null-handling,
//     harder to audit.
//   - The underlying runtime (db_builtins.go's `argString`) is geared toward
//     Monty host calls, where empty string is always invalid. Meta-tool inputs
//     sometimes allow empty (clearing a field), sometimes require non-empty
//     (create_app name). The split keeps both callsite semantics unambiguous.
//
// Naming:
//   - `arg*` helpers match the existing Monty-side helpers — they all take
//     (map[string]any, key) — so authors can move between builtin dispatchers
//     and meta-tool handlers without mental context switching.
//   - `load*` helpers are the DB lookups meta-tools share. Returning ErrNotFound
//     keeps the handler callsite short: one friendly message, one branch.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// Errors returned by meta-tool helpers. Exported so individual handlers can
// translate them to tool-level friendly messages via errors.Is.
var (
	// ErrForbidden fires when guardAdmin rejects a non-admin caller.
	ErrForbidden = errors.New("forbidden: admin role required")

	// ErrMissingConfirm fires when a destructive meta-tool was called without
	// the `confirm: true` flag. Per the plan, every destructive meta-tool
	// (delete_app, purge_app_data, ...) requires explicit confirmation so the
	// LLM can't wipe tenant data with a single hallucinated call.
	ErrMissingConfirm = errors.New("destructive operation requires confirm: true")

	// ErrAppNotFound is returned by loadBuilderAppByName when no app with the
	// given (tenant_id, name) exists. Handlers surface this as a clean
	// "app not found" message.
	ErrAppNotFound = errors.New("app not found")
)

// guardAdmin rejects non-admin callers with ErrForbidden. Every meta-tool
// handler calls this first — the admin-only registration in RegisterAgentTools
// already skips tool registration for non-admins, but the guard is repeated
// here because MCP tool registration is caller-agnostic (tools are registered
// once at server startup and the caller is resolved per request). Without this
// check, a non-admin MCP user could still invoke any meta-tool they knew the
// name of.
func guardAdmin(c *services.Caller) error {
	if c == nil {
		return ErrForbidden
	}
	if !c.IsAdmin {
		return ErrForbidden
	}
	return nil
}

// argBool returns a bool from input; defaults to false if missing or null.
// Unlike the Monty-side argOptionalBool (which errors on wrong types), this
// one is the tolerant meta-tool variant: LLM models occasionally serialise
// booleans as "true"/"false" strings, but we still flag that as an error so
// a mis-typed confirm flag doesn't silently default to false.
func argBool(input map[string]any, key string) (bool, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("argument %q must be a bool, got %T", key, raw)
	}
	return b, nil
}

// argOptionalJSON returns the raw JSON bytes for key or nil if missing. The
// input map comes from json.Unmarshal into map[string]any, so we re-marshal
// the sub-tree. Handlers use this for free-form fields (args_schema, data,
// etc.) where the shape isn't fixed.
func argOptionalJSON(input map[string]any, key string) (json.RawMessage, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("argument %q: marshalling: %w", key, err)
	}
	return b, nil
}

// argStringList returns a []string from input[key]. Missing / null returns an
// empty slice (not nil) so handlers can range without a nil check. Non-list
// or non-string items are a hard error — we don't silently coerce.
func argStringList(input map[string]any, key string) ([]string, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return []string{}, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("argument %q must be a list of strings, got %T", key, raw)
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("argument %q[%d] must be a string, got %T", key, i, item)
		}
		out = append(out, s)
	}
	return out, nil
}

// parseInput unmarshals tool input JSON into a map. Meta-tool handlers call
// this first to get a uniform map they can pass to arg* helpers. Rejects
// non-object inputs (numbers, arrays, bare strings) with a clear error so an
// LLM that emits the wrong shape gets an immediate correction rather than a
// confusing "missing required arg" further down.
func parseInput(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		// An absent input block is equivalent to an empty object. Tools like
		// list_apps take no args; the LLM might emit nothing at all.
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("input must be a JSON object: %w", err)
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

// formatToolResult marshals v to a JSON string. Meta-tools return structured
// results (the created app, the list of apps, deletion summaries) — the LLM
// on the other side parses them back to JSON, so stringifying here is the
// one-line glue.
func formatToolResult(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("formatting result: %w", err)
	}
	return string(b), nil
}

// requireConfirm enforces that a destructive meta-tool was called with
// `confirm: true`. Returns ErrMissingConfirm otherwise. The caller bubbles
// that up as a friendly "this deletes data; pass confirm=true" message.
func requireConfirm(input map[string]any) error {
	ok, err := argBool(input, "confirm")
	if err != nil {
		return err
	}
	if !ok {
		return ErrMissingConfirm
	}
	return nil
}

// loadBuilderAppByName resolves (tenant_id, name) → BuilderApp. Returns
// ErrAppNotFound when no row matches so callers can branch on errors.Is and
// return a clean message. Any other error wraps as "loading app: %w".
func loadBuilderAppByName(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	name string,
) (*BuilderApp, error) {
	const q = `
		SELECT id, tenant_id, name, COALESCE(description, ''), created_by, created_at
		FROM builder_apps
		WHERE tenant_id = $1 AND name = $2
	`
	var app BuilderApp
	err := pool.QueryRow(ctx, q, tenantID, name).Scan(
		&app.ID, &app.TenantID, &app.Name, &app.Description, &app.CreatedBy, &app.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAppNotFound
		}
		return nil, fmt.Errorf("loading app %q: %w", name, err)
	}
	return &app, nil
}
