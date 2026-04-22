// Package builder: db_builtins.go bridges the ItemService into the Monty
// runtime as a flat allowlist of `db_*` host functions.
//
// Why a flat function set?  Monty's host-function ABI is a named allowlist —
// no classes, no attribute dispatch. So instead of `db.contacts.find(...)`
// we expose six top-level calls where `collection` is the first positional
// argument. Scripts look like:
//
//	user = db_insert_one("contacts", {"name": "Jane"})
//	doc  = db_find_one("contacts", {"_id": user["_id"]})
//	rows = db_find("contacts", {}, limit=10, sort=[("_created_at", -1)])
//	db_update_one("contacts", {"_id": user["_id"]}, {"$set": {"tier": "gold"}})
//	n    = db_delete_one("contacts", {"_id": user["_id"]})
//	c    = db_count_documents("contacts", {"tier": "gold"})
//
// Each call lands in the single dispatcher returned from BuildDBBuiltins,
// which pulls `collection` out of the args, stitches it onto a Scope
// template built from the caller's tenant/app/user/run triple, and hands the
// rest to the matching ItemService method.
//
// The bridge lives in the builder package (not runtime/) so it can import
// ItemService without creating a runtime→builder cycle. Callers wire it in
// via runtime.Capabilities — the handler and FuncDefs are returned
// separately so callers can also stack them on runtime.WithExternalFunc
// directly for raw Runner-level uses.
package builder

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// Canonical builtin names. Exported so call sites (tests, registrations) can
// reference them without hard-coding strings.
const (
	FnInsertOne      = "db_insert_one"
	FnFindOne        = "db_find_one"
	FnFind           = "db_find"
	FnUpdateOne      = "db_update_one"
	FnDeleteOne      = "db_delete_one"
	FnCountDocuments = "db_count_documents"
)

// DBBuiltins is the return bundle from BuildDBBuiltins: the FuncDefs to
// register with Monty (so positional args get named), a dispatcher
// ExternalFunc, per-name param metadata (for runtime.Capabilities.
// BuiltInParams), a map keyed by function name (for
// runtime.Capabilities.BuiltIns), and a call counter.
type DBBuiltins struct {
	// Funcs is the ordered list of FuncDefs, ready to hand to
	// runtime.WithExternalFunc for raw Runner use.
	Funcs []runtime.FuncDef

	// Handler dispatches every db_* call into the ItemService.
	Handler runtime.ExternalFunc

	// Params maps each function name to its positional parameter names.
	// Feed this into runtime.Capabilities.BuiltInParams.
	Params map[string][]string

	// BuiltIns maps each function name to a thin wrapper around Handler
	// that filters on the name. Feed this into
	// runtime.Capabilities.BuiltIns.
	BuiltIns map[string]runtime.GoFunc

	// CallsRemaining returns the remaining quota after every db_* call the
	// script has made so far. Useful for post-run telemetry and tests.
	CallsRemaining func() int

	// Mutation counters, bumped on successful db_insert_one / db_update_one
	// (count > 0) / db_delete_one (count > 0). The Snapshot in
	// ScriptRunCounters folds these together with the ActionBuiltins
	// counters so mutation_summary reports all row-level changes the
	// script made (app_items + Kit-native entities), not just one surface.
	insertCount int
	updateCount int
	deleteCount int
}

// MutationSummary reports per-run app_items mutation counts, mirroring
// ActionBuiltins.MutationSummary so both can be aggregated by
// ScriptRunCounters.Snapshot.
func (d *DBBuiltins) MutationSummary() map[string]int {
	return map[string]int{
		"inserts": d.insertCount,
		"updates": d.updateCount,
		"deletes": d.deleteCount,
	}
}

// BuildDBBuiltins wires an ItemService into a batch of Monty host functions.
//
// The returned Handler routes each db_* call into ItemService using a Scope
// built from (tenantID, appID, callerID, runID, collection); `collection`
// is pulled out of the script's first positional arg on every call.
//
// maxCalls caps how many db_* calls a single run may make. Every dispatch
// decrements the counter; when it reaches zero the next call returns an
// error that aborts the script (host errors cannot be caught from Python
// per the earlier monty-wasm finding). Pass 0 or negative to disable the
// quota entirely.
func BuildDBBuiltins(
	svc *ItemService,
	tenantID, appID, callerID uuid.UUID,
	runID *uuid.UUID,
	maxCalls int,
) *DBBuiltins {
	remaining := maxCalls
	// When maxCalls <= 0 we treat the quota as infinite; using a sentinel
	// avoids a conditional on every call.
	unlimited := maxCalls <= 0

	scopeTemplate := Scope{
		TenantID:     tenantID,
		BuilderAppID: appID,
		CallerUserID: callerID,
		ScriptRunID:  runID,
	}

	// Counters are captured by pointer so both the handler closure and the
	// returned *DBBuiltins share a single source of truth — incrementing
	// one is visible to MutationSummary without a sync primitive (per-run
	// dispatch is serial).
	bundle := &DBBuiltins{}

	handler := func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
		if !unlimited {
			if remaining <= 0 {
				return nil, fmt.Errorf("db quota exhausted: %d calls allowed per run", maxCalls)
			}
			remaining--
		}

		collection, err := argString(call.Args, "collection")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", call.Name, err)
		}
		scope := scopeTemplate
		scope.Collection = collection

		switch call.Name {
		case FnInsertOne:
			res, err := dispatchInsertOne(ctx, svc, scope, call)
			if err == nil {
				bundle.insertCount++
			}
			return res, err
		case FnFindOne:
			return dispatchFindOne(ctx, svc, scope, call)
		case FnFind:
			return dispatchFind(ctx, svc, scope, call)
		case FnUpdateOne:
			res, err := dispatchUpdateOne(ctx, svc, scope, call)
			if err == nil {
				if n, ok := res.(int); ok && n > 0 {
					bundle.updateCount += n
				}
			}
			return res, err
		case FnDeleteOne:
			res, err := dispatchDeleteOne(ctx, svc, scope, call)
			if err == nil {
				if n, ok := res.(int); ok && n > 0 {
					bundle.deleteCount += n
				}
			}
			return res, err
		case FnCountDocuments:
			return dispatchCount(ctx, svc, scope, call)
		default:
			return nil, fmt.Errorf("db_builtins: unknown function %q", call.Name)
		}
	}

	params := map[string][]string{
		FnInsertOne:      {"collection", "doc"},
		FnFindOne:        {"collection", "filter"},
		FnFind:           {"collection", "filter", "limit", "skip", "sort"},
		FnUpdateOne:      {"collection", "filter", "update"},
		FnDeleteOne:      {"collection", "filter"},
		FnCountDocuments: {"collection", "filter"},
	}

	// FuncDefs in stable order for deterministic registration logs.
	funcs := []runtime.FuncDef{
		runtime.Func(FnCountDocuments, params[FnCountDocuments]...),
		runtime.Func(FnDeleteOne, params[FnDeleteOne]...),
		runtime.Func(FnFind, params[FnFind]...),
		runtime.Func(FnFindOne, params[FnFindOne]...),
		runtime.Func(FnInsertOne, params[FnInsertOne]...),
		runtime.Func(FnUpdateOne, params[FnUpdateOne]...),
	}

	builtIns := map[string]runtime.GoFunc{}
	for name := range params {
		builtIns[name] = func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
			// The handler dispatches on call.Name; it doesn't trust this
			// closure's captured name because Capabilities.BuiltIns is
			// looked up by map key.
			if call.Name != name {
				return nil, fmt.Errorf("db_builtins: name mismatch %q != %q", call.Name, name)
			}
			return handler(ctx, call)
		}
	}

	callsRemaining := func() int {
		if unlimited {
			return -1
		}
		return remaining
	}

	bundle.Funcs = funcs
	bundle.Handler = handler
	bundle.Params = params
	bundle.BuiltIns = builtIns
	bundle.CallsRemaining = callsRemaining
	return bundle
}

// dispatchInsertOne routes db_insert_one(collection, doc) into
// ItemService.InsertOne.
func dispatchInsertOne(ctx context.Context, svc *ItemService, scope Scope, call *runtime.FunctionCall) (any, error) {
	doc, err := argMap(call.Args, "doc")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	return svc.InsertOne(ctx, scope, doc)
}

// dispatchFindOne routes db_find_one(collection, filter) into
// ItemService.FindOne. Missing filter defaults to empty.
func dispatchFindOne(ctx context.Context, svc *ItemService, scope Scope, call *runtime.FunctionCall) (any, error) {
	filter, err := argOptionalMap(call.Args, "filter")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	doc, err := svc.FindOne(ctx, scope, filter)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		// nil crosses back to Python as None, which is what FindOne
		// semantics should look like script-side.
		return nil, nil //nolint:nilnil
	}
	return doc, nil
}

// dispatchFind routes db_find(collection, filter, limit, skip, sort) into
// ItemService.Find. Numeric kwargs arrive as float64; sort is a list.
func dispatchFind(ctx context.Context, svc *ItemService, scope Scope, call *runtime.FunctionCall) (any, error) {
	filter, err := argOptionalMap(call.Args, "filter")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	limit, err := argOptionalInt(call.Args, "limit")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	skip, err := argOptionalInt(call.Args, "skip")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	sortArg, err := argOptionalList(call.Args, "sort")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	rows, err := svc.Find(ctx, scope, filter, sortArg, limit, skip)
	if err != nil {
		return nil, err
	}
	// Monty's JSON bridge wants []any, not []map[string]any.
	out := make([]any, len(rows))
	for i := range rows {
		out[i] = rows[i]
	}
	return out, nil
}

// dispatchUpdateOne routes db_update_one(collection, filter, update).
func dispatchUpdateOne(ctx context.Context, svc *ItemService, scope Scope, call *runtime.FunctionCall) (any, error) {
	filter, err := argOptionalMap(call.Args, "filter")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	update, err := argMap(call.Args, "update")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	n, err := svc.UpdateOne(ctx, scope, filter, update)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// dispatchDeleteOne routes db_delete_one(collection, filter).
func dispatchDeleteOne(ctx context.Context, svc *ItemService, scope Scope, call *runtime.FunctionCall) (any, error) {
	filter, err := argOptionalMap(call.Args, "filter")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	n, err := svc.DeleteOne(ctx, scope, filter)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// dispatchCount routes db_count_documents(collection, filter).
func dispatchCount(ctx context.Context, svc *ItemService, scope Scope, call *runtime.FunctionCall) (any, error) {
	filter, err := argOptionalMap(call.Args, "filter")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	return svc.CountDocuments(ctx, scope, filter)
}

// argString pulls a required string argument from the Monty-supplied args
// map. Returns a clear error if missing or wrong type.
func argString(args map[string]any, name string) (string, error) {
	raw, ok := args[name]
	if !ok {
		return "", fmt.Errorf("missing required argument %q", name)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("argument %q must be a string, got %T", name, raw)
	}
	if s == "" {
		return "", fmt.Errorf("argument %q must be a non-empty string", name)
	}
	return s, nil
}

// argMap pulls a required object argument.
func argMap(args map[string]any, name string) (map[string]any, error) {
	raw, ok := args[name]
	if !ok {
		return nil, fmt.Errorf("missing required argument %q", name)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("argument %q must be an object, got %T", name, raw)
	}
	return m, nil
}

// argOptionalMap allows missing/None; returns nil if absent.
func argOptionalMap(args map[string]any, name string) (map[string]any, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return nil, nil //nolint:nilnil // intended: absent means empty
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("argument %q must be an object or None, got %T", name, raw)
	}
	return m, nil
}

// argOptionalInt coerces a numeric arg via float64 (JSON) → int. Absent or
// None is zero. Negative values are rejected — limit/skip are counts.
func argOptionalInt(args map[string]any, name string) (int, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return 0, nil
	}
	// JSON numbers always arrive as float64 from the Monty bridge.
	f, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("argument %q must be a number or None, got %T", name, raw)
	}
	n := int(f)
	if n < 0 {
		return 0, fmt.Errorf("argument %q must be >= 0, got %d", name, n)
	}
	return n, nil
}

// argOptionalList coerces a list-of-tuples arg (as produced by Python
// literals like [("field", -1)]) into []any for the translator. Absent or
// None returns nil.
func argOptionalList(args map[string]any, name string) ([]any, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("argument %q must be a list or None, got %T", name, raw)
	}
	return list, nil
}
