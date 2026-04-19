package runtime

import (
	"context"

	"github.com/google/uuid"
)

// Engine is the pluggable script runtime interface used by the Kit builder.
// Monty (Python-on-WASM) is the default implementation; a Starlark fallback
// can slot in behind the same interface if Monty ever fails Kit's needs.
//
// The two-step shape (Compile then Run) matches how skill modules are expected
// to work: an admin-authored script defines one or more named functions, the
// module is compiled once per deployment, and individual executions call a
// specific function by name with kwargs.
type Engine interface {
	// Compile prepares a script source for execution. Returns an opaque
	// Module that can be passed to Run repeatedly. Implementations MAY defer
	// syntax validation until Run; callers should not assume Compile catches
	// every authoring error.
	Compile(src string) (Module, error)

	// Run invokes fn(**kwargs) inside the compiled module using the supplied
	// capabilities. Returns the function's return value, execution metadata,
	// and any error (host, runtime, or script-side).
	Run(ctx context.Context, mod Module, fn string, kwargs map[string]any, caps *Capabilities) (any, Metadata, error)
}

// Module is an opaque handle to a compiled script. Implementations embed
// whatever representation they need; callers should treat it as a token.
//
// For the Monty engine, Module currently carries the source string because
// Monty re-parses on every Execute call. A future pass could cache an AST or
// a guest-side compiled handle if monty-wasm exposes one — that's a
// performance knob, not a public-API change.
type Module struct {
	// Source is the raw script text. Opaque to callers — only the Engine
	// implementation should read it.
	Source string
}

// GoFunc is a host function exposed to scripts via Capabilities.BuiltIns.
// It matches the existing monty ExternalFunc shape so migrations between
// the raw Runner API and the Engine API are mechanical.
type GoFunc func(ctx context.Context, call *FunctionCall) (any, error)

// ResourceLimits is an alias of the existing Limits struct. The plan uses
// the longer name; the alias keeps the existing Monty-level knobs in one
// place without forcing a rename cascade through monty.go.
type ResourceLimits = Limits

// Capabilities bundles everything a script run is allowed to touch: the
// allowlisted host functions, resource limits, and identity metadata for
// audit/telemetry. No field on this struct should be assumed safe to log
// as-is — TenantID/CallerID are fine, BuiltIns obviously are not.
type Capabilities struct {
	// BuiltIns maps function name -> host implementation. Every name a
	// script can call must be in this map; there is no ambient surface.
	BuiltIns map[string]GoFunc

	// BuiltInParams declares positional parameter names for entries in
	// BuiltIns. Monty's WASM guest uses these to map positional call args to
	// named fields in FunctionCall.Args; without them, positional arguments
	// are dropped (only kwargs survive). Functions not listed here are
	// registered with no positional params, matching the legacy behavior
	// where BuiltIns entries were kwargs-only.
	BuiltInParams map[string][]string

	// Limits is the wall-clock / memory / allocation / recursion budget.
	Limits ResourceLimits

	// RunID uniquely identifies this execution (for logs + storage).
	RunID uuid.UUID

	// TenantID is the tenant whose data the script is operating against.
	TenantID uuid.UUID

	// BuilderAppID scopes this run to a single builder_app row. Host
	// bridges (e.g. the db_* builtins) stitch this onto the ItemService
	// Scope so scripts never see or supply it.
	BuilderAppID uuid.UUID

	// CallerID is the user (or system principal) that triggered the run.
	CallerID uuid.UUID
}

// Metadata is the per-run telemetry the Engine reports back. Intentionally
// minimal for v0.1 — the storage / skill-surface work will extend this as
// needed (e.g. memory high-water, allocation count).
type Metadata struct {
	// DurationMs is wall-clock time spent inside Engine.Run, in milliseconds.
	DurationMs int64

	// Printed is the ordered list of strings the script sent to print().
	Printed []string

	// ExternalCalls is how many times the script dispatched into a BuiltIn.
	ExternalCalls int
}
