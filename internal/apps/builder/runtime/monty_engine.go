package runtime

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// kwargsInputName is the single Monty-side input variable used to smuggle
// the caller's kwargs map into the script. We then append
// `fn(**__kit_kwargs__)` as the trailing expression so its return value
// becomes Monty's Execute result.
//
// The name is double-underscore-prefixed to minimise the risk of colliding
// with an admin-authored global. Scripts can still shadow it, but that's
// opt-in self-harm.
const kwargsInputName = "__kit_kwargs__"

// MontyEngine implements Engine on top of the existing Monty Runner. The
// Runner owns the wazero runtime and compiled WASM; the engine is a thin
// translator between the Engine contract and the Runner's Execute options.
//
// The engine may or may not own its Runner. When constructed via
// NewMontyEngine the caller owns the Runner and is responsible for its
// lifecycle; Engine.Close() is then a no-op. When constructed via
// NewMontyEngineOwned the engine takes ownership and Close() tears the
// Runner down.
type MontyEngine struct {
	runner     *Runner
	ownsRunner bool
}

// NewMontyEngine wraps an existing Runner. The engine does not take
// ownership — the caller is responsible for calling Runner.Close() when the
// Runner's lifetime ends. This is the canonical constructor for cases where
// the Runner has a longer lifetime than any single engine (e.g. test suites
// that share one Runner across every test via TestMain).
func NewMontyEngine(r *Runner) *MontyEngine {
	return &MontyEngine{runner: r, ownsRunner: false}
}

// NewMontyEngineOwned constructs a MontyEngine backed by a freshly-built
// Runner that the engine owns. Close() on the returned engine tears down
// the Runner. Prefer NewMontyEngine when the caller already has a Runner
// it wants to share.
func NewMontyEngineOwned() (*MontyEngine, error) {
	r, err := New()
	if err != nil {
		return nil, fmt.Errorf("new monty engine: %w", err)
	}
	return &MontyEngine{runner: r, ownsRunner: true}, nil
}

// Compile returns a Module carrying the source. No syntax pre-check is
// performed here — Monty re-parses during Run, so compile errors surface on
// the first Run call. TODO: wire up a cheap parse-only path if monty-wasm
// ever exposes one, so authoring errors are caught at build time.
func (e *MontyEngine) Compile(src string) (Module, error) {
	return Module{Source: src}, nil
}

// Run executes `fn(**kwargs)` inside the supplied module under the given
// capabilities. It synthesises a trailing expression that calls fn with
// kwargs splatted from a single Monty input, so admin scripts can keep
// their function definitions clean and we don't have to interpolate
// potentially-unsafe keys into the source.
func (e *MontyEngine) Run(
	ctx context.Context,
	mod Module,
	fn string,
	kwargs map[string]any,
	caps *Capabilities,
) (any, Metadata, error) {
	if caps == nil {
		caps = &Capabilities{}
	}

	code, inputs := buildInvocation(mod.Source, fn, kwargs)
	funcDefs := sortedFuncDefs(caps.BuiltIns, caps.BuiltInParams)

	meta := Metadata{}
	dispatch, counter := dispatchBuiltIns(caps.BuiltIns)

	printed := []string{}
	printFunc := func(s string) { printed = append(printed, s) }

	start := time.Now()
	opts := []ExecuteOption{
		WithLimits(caps.Limits),
		WithPrintFunc(printFunc),
	}
	if len(caps.BuiltIns) > 0 {
		opts = append(opts, WithExternalFunc(dispatch, funcDefs...))
	}

	result, err := e.runner.Execute(ctx, code, inputs, opts...)
	meta.DurationMs = time.Since(start).Milliseconds()
	meta.Printed = printed
	meta.ExternalCalls = *counter
	if err != nil {
		return nil, meta, err
	}
	return result, meta, nil
}

// Close releases the underlying Runner only if this engine owns it
// (constructed via NewMontyEngineOwned). For engines built via
// NewMontyEngine the Runner is caller-owned and Close is a no-op.
func (e *MontyEngine) Close() error {
	if !e.ownsRunner {
		return nil
	}
	return e.runner.Close()
}

// buildInvocation assembles the Python source Monty will actually execute:
// the module body, a newline, then `fn(**__kit_kwargs__)` as a trailing
// expression whose value becomes Execute's result. The kwargs map is
// threaded through as a single Monty input, which avoids quoting concerns
// for arbitrary keys and values.
func buildInvocation(src, fn string, kwargs map[string]any) (string, map[string]any) {
	if kwargs == nil {
		kwargs = map[string]any{}
	}
	code := src + "\n\n" + fn + "(**" + kwargsInputName + ")\n"
	inputs := map[string]any{kwargsInputName: kwargs}
	return code, inputs
}

// sortedFuncDefs turns the BuiltIns map into a FuncDef slice with stable
// ordering. Monty doesn't care about order, but tests do — and stable
// output makes logs less noisy when we start dumping the allowlist.
//
// paramsByName supplies positional parameter names for individual builtins.
// Entries with no params declared come back as zero-param FuncDefs (kwargs-
// only on the script side); explicit entries let scripts call the builtin
// positionally and still get a named Args map on the Go side.
func sortedFuncDefs(builtIns map[string]GoFunc, paramsByName map[string][]string) []FuncDef {
	if len(builtIns) == 0 {
		return nil
	}
	names := make([]string, 0, len(builtIns))
	for name := range builtIns {
		names = append(names, name)
	}
	sort.Strings(names)
	defs := make([]FuncDef, 0, len(names))
	for _, name := range names {
		defs = append(defs, Func(name, paramsByName[name]...))
	}
	return defs
}

// dispatchBuiltIns returns an ExternalFunc that routes to the right GoFunc
// in the BuiltIns map, plus a pointer to the call counter used for
// Metadata.ExternalCalls.
func dispatchBuiltIns(builtIns map[string]GoFunc) (ExternalFunc, *int) {
	count := 0
	dispatcher := func(ctx context.Context, call *FunctionCall) (any, error) {
		count++
		fn, ok := builtIns[call.Name]
		if !ok {
			return nil, fmt.Errorf("built-in %q not registered", call.Name)
		}
		return fn(ctx, call)
	}
	return dispatcher, &count
}
