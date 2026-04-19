// Forked from github.com/fugue-labs/monty-go v0.2.0 (MIT, see
// third_party/monty-wasm/LICENSE). Upstream looks unmaintained, so Kit owns
// the pipeline: a vendored Rust shim under third_party/monty-wasm/ produces
// monty.wasm, which this package embeds and drives via wazero.
//
// Package runtime wraps the Monty Python interpreter compiled to WebAssembly.
// wazero (pure Go, no CGO) runs the guest in a sandbox with pause/resume
// hooks for host-side function calls.
package runtime

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed monty.wasm
var montyWasm []byte

// Runner is a compiled Monty WASM runtime ready to execute Python code.
// Create one Runner and reuse it across multiple Execute calls.
// Each Execute call gets its own isolated WASM instance.
type Runner struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
}

// Limits configures resource limits for Python execution.
type Limits struct {
	MaxMemoryBytes    uint64        `json:"max_memory,omitempty"`
	MaxDuration       time.Duration `json:"-"`
	MaxAllocations    uint64        `json:"max_allocations,omitempty"`
	MaxRecursionDepth uint32        `json:"max_recursion_depth,omitempty"`
}

// MarshalJSON implements custom JSON marshaling for Limits.
func (l Limits) MarshalJSON() ([]byte, error) {
	type alias struct {
		MaxAllocations    *uint64 `json:"max_allocations,omitempty"`
		MaxDurationMs     *uint64 `json:"max_duration_ms,omitempty"`
		MaxMemory         *uint64 `json:"max_memory,omitempty"`
		MaxRecursionDepth *uint32 `json:"max_recursion_depth,omitempty"`
	}
	a := alias{}
	if l.MaxAllocations > 0 {
		v := l.MaxAllocations
		a.MaxAllocations = &v
	}
	if l.MaxDuration > 0 {
		v := uint64(l.MaxDuration.Milliseconds())
		a.MaxDurationMs = &v
	}
	if l.MaxMemoryBytes > 0 {
		v := l.MaxMemoryBytes
		a.MaxMemory = &v
	}
	if l.MaxRecursionDepth > 0 {
		v := l.MaxRecursionDepth
		a.MaxRecursionDepth = &v
	}
	return json.Marshal(a)
}

// FunctionCall contains information about an external function call from Python.
// Args contains all arguments merged into a single map — positional args are
// mapped to parameter names (registered via FuncDef) and kwargs are merged in.
type FunctionCall struct {
	Name   string
	Args   map[string]any
	CallID uint32
}

// ArgsJSON returns Args serialized as a JSON string, suitable for passing
// directly to tool handlers that accept JSON argument strings.
func (fc *FunctionCall) ArgsJSON() string {
	if len(fc.Args) == 0 {
		return "{}"
	}
	b, err := json.Marshal(fc.Args)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// FuncDef defines an external Python function with its parameter names.
// Parameter names enable positional-to-keyword argument mapping in the WASM layer.
type FuncDef struct {
	Name   string   `json:"name"`
	Params []string `json:"params,omitempty"`
}

// Func creates a FuncDef with the given name and parameter names.
func Func(name string, params ...string) FuncDef {
	return FuncDef{Name: name, Params: params}
}

// OsCall contains information about an OS-level operation from Python.
type OsCall struct {
	Function string
	Args     []any
	Kwargs   map[string]any
	CallID   uint32
}

// ExternalFunc is called when Python code calls an external function.
type ExternalFunc func(ctx context.Context, call *FunctionCall) (any, error)

// OsCallFunc is called when Python code performs an OS operation.
type OsCallFunc func(ctx context.Context, call *OsCall) (any, error)

// ExecuteOption configures a single Execute call.
type ExecuteOption func(*executeConfig)

type executeConfig struct {
	externalFunc ExternalFunc
	osCallFunc   OsCallFunc
	limits       Limits
	printFunc    func(string)
	extFuncs     []FuncDef
}

// WithExternalFunc sets the callback for external function calls.
// Each FuncDef declares a function name and its parameter names (for
// positional-to-keyword argument mapping).
func WithExternalFunc(fn ExternalFunc, funcs ...FuncDef) ExecuteOption {
	return func(c *executeConfig) {
		c.externalFunc = fn
		c.extFuncs = funcs
	}
}

// WithOsCallFunc sets the callback for OS-level operations.
func WithOsCallFunc(fn OsCallFunc) ExecuteOption {
	return func(c *executeConfig) { c.osCallFunc = fn }
}

// WithLimits sets resource limits for the execution.
func WithLimits(l Limits) ExecuteOption {
	return func(c *executeConfig) { c.limits = l }
}

// WithPrintFunc sets a callback for Python print() output.
func WithPrintFunc(fn func(string)) ExecuteOption {
	return func(c *executeConfig) { c.printFunc = fn }
}

// wasmCacheDir is where wazero's file-backed compilation cache lives. Using
// os.TempDir keeps it out of the repo, per-host, and transparently cleaned
// up by OS tmp-reaper policies. The contents are a pure speedup — safe to
// delete at any time; wazero will rebuild them on the next New() call.
func wasmCacheDir() string {
	return filepath.Join(os.TempDir(), "kit-monty-wasm-cache")
}

// New creates a new Monty WASM runner.
// The WASM module is compiled once and reused across Execute calls.
//
// wazero's compilation cache is attached in file-backed mode; on second and
// subsequent process starts, the Monty WASM binary is loaded from the cache
// directory (os.TempDir()/kit-monty-wasm-cache) instead of recompiled. The
// cache is a pure speedup — deleting the directory at any time is safe and
// simply causes the next boot to recompile and repopulate.
func New() (*Runner, error) {
	ctx := context.Background()

	config := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true)

	// Best-effort: enable the file-backed compile cache. If the cache dir
	// can't be created or opened (disk full, permission denied, read-only
	// tmp), fall back to an uncached runtime rather than failing outright —
	// the cache is a speedup, not a correctness requirement.
	cacheDir := wasmCacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
		if cache, cerr := wazero.NewCompilationCacheWithDir(cacheDir); cerr == nil {
			config = config.WithCompilationCache(cache)
		} else {
			slog.Warn("monty: compile cache unavailable, continuing without it", "dir", cacheDir, "err", cerr)
		}
	} else {
		slog.Warn("monty: compile cache dir unusable, continuing without it", "dir", cacheDir, "err", err)
	}

	r := wazero.NewRuntimeWithConfig(ctx, config)

	// Instantiate WASI (provides clock, random, fd_write for the WASM module).
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("monty: failed to instantiate WASI: %w", err)
	}

	compiled, err := r.CompileModule(ctx, montyWasm)
	if err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("monty: failed to compile WASM module: %w", err)
	}

	return &Runner{
		runtime:  r,
		compiled: compiled,
	}, nil
}

// Execute runs Python code with the given inputs and returns the result.
// Each call creates an isolated WASM instance that is cleaned up when done.
func (r *Runner) Execute(ctx context.Context, code string, inputs map[string]any, opts ...ExecuteOption) (any, error) {
	cfg := &executeConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Instantiate a fresh module for this execution.
	mod, err := r.runtime.InstantiateModule(ctx, r.compiled,
		wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("monty: failed to instantiate module: %w", err)
	}
	defer mod.Close(ctx)

	inst := &instance{mod: mod}
	if err := inst.resolveExports(); err != nil {
		return nil, err
	}

	return inst.execute(ctx, code, inputs, cfg)
}

// Close releases all WASM resources.
func (r *Runner) Close() error {
	return r.runtime.Close(context.Background())
}
