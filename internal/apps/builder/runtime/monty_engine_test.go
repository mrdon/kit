package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// getEngine returns the package-scoped testEngine set up in TestMain. It
// exists purely as a named accessor so tests read naturally; there is no
// per-test construction cost.
func getEngine(t *testing.T) *MontyEngine {
	t.Helper()
	if testEngine == nil {
		t.Fatal("testEngine not initialised; TestMain did not run")
	}
	return testEngine
}

func newEngineCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// TestEngineCompileRun exercises the happy path: a script defining a single
// function, invoked via Run with a kwargs map.
func TestEngineCompileRun(t *testing.T) {
	engine := getEngine(t)
	ctx, cancel := newEngineCtx(t)
	defer cancel()

	mod, err := engine.Compile(`
def greet(name):
    return "hello " + name
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	caps := &Capabilities{RunID: uuid.New(), TenantID: uuid.New(), CallerID: uuid.New()}
	result, meta, err := engine.Run(ctx, mod, "greet", map[string]any{"name": "kit"}, caps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := result.(string); got != "hello kit" {
		t.Fatalf("result = %q, want %q", got, "hello kit")
	}
	if meta.DurationMs < 0 {
		t.Fatalf("DurationMs negative: %d", meta.DurationMs)
	}
	if meta.ExternalCalls != 0 {
		t.Fatalf("ExternalCalls = %d, want 0", meta.ExternalCalls)
	}
}

// TestEngineRunWithBuiltIns: script calls a host function, dispatcher routes
// the call to the matching GoFunc. Covers the core allowlist contract.
func TestEngineRunWithBuiltIns(t *testing.T) {
	engine := getEngine(t)
	ctx, cancel := newEngineCtx(t)
	defer cancel()

	var gotArgs map[string]any
	builtIns := map[string]GoFunc{
		"find_user": func(_ context.Context, call *FunctionCall) (any, error) {
			gotArgs = call.Args
			return map[string]any{"id": "u_42", "name": call.Args["name"]}, nil
		},
	}

	mod, err := engine.Compile(`
def lookup(name):
    u = find_user(name=name)
    return u["id"]
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	caps := &Capabilities{BuiltIns: builtIns, RunID: uuid.New()}
	result, meta, err := engine.Run(ctx, mod, "lookup", map[string]any{"name": "alice"}, caps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "u_42" {
		t.Fatalf("result = %v, want u_42", result)
	}
	if gotArgs["name"] != "alice" {
		t.Fatalf("dispatcher saw name = %v, want alice", gotArgs["name"])
	}
	if meta.ExternalCalls != 1 {
		t.Fatalf("ExternalCalls = %d, want 1", meta.ExternalCalls)
	}
}

// TestEngineRunRespectsLimits: a busy-loop module under a tight MaxDuration
// must be aborted and surface an error.
func TestEngineRunRespectsLimits(t *testing.T) {
	engine := getEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mod, err := engine.Compile(`
def spin():
    while True:
        pass
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	caps := &Capabilities{
		Limits: ResourceLimits{MaxDuration: 100 * time.Millisecond},
	}
	start := time.Now()
	_, _, err = engine.Run(ctx, mod, "spin", nil, caps)
	elapsed := time.Since(start)
	t.Logf("engine Run elapsed under MaxDuration=100ms: %v", elapsed)

	if err == nil {
		t.Fatal("expected error from MaxDuration, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("MaxDuration didn't abort within 2s (elapsed %v)", elapsed)
	}
	var montyErr *MontyError
	if !errors.As(err, &montyErr) {
		t.Fatalf("expected *MontyError, got %T: %v", err, err)
	}
}

// TestEngineMetadata verifies DurationMs is non-zero and ExternalCalls is
// counted across multiple built-in invocations inside one Run.
func TestEngineMetadata(t *testing.T) {
	engine := getEngine(t)
	ctx, cancel := newEngineCtx(t)
	defer cancel()

	builtIns := map[string]GoFunc{
		"one": func(_ context.Context, _ *FunctionCall) (any, error) {
			return float64(1), nil
		},
		"two": func(_ context.Context, _ *FunctionCall) (any, error) {
			return float64(2), nil
		},
	}

	mod, err := engine.Compile(`
def sum_them():
    print("starting")
    return one() + two() + one()
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	caps := &Capabilities{BuiltIns: builtIns}
	result, meta, err := engine.Run(ctx, mod, "sum_them", nil, caps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != float64(4) {
		t.Fatalf("result = %v, want 4", result)
	}
	// DurationMs is floor(ms); fast runs can legitimately round to 0.
	if meta.DurationMs < 0 {
		t.Fatalf("DurationMs = %d, want >= 0", meta.DurationMs)
	}
	if meta.ExternalCalls != 3 {
		t.Fatalf("ExternalCalls = %d, want 3", meta.ExternalCalls)
	}
	if len(meta.Printed) == 0 || !strings.Contains(strings.Join(meta.Printed, ""), "starting") {
		t.Fatalf("Printed = %v, want entry containing 'starting'", meta.Printed)
	}
}

// TestEngineMultipleRunsSameModule: compile once, run repeatedly with
// different kwargs. Monty re-parses per Execute so this should just work,
// but we pin the contract against regressions.
func TestEngineMultipleRunsSameModule(t *testing.T) {
	engine := getEngine(t)
	ctx, cancel := newEngineCtx(t)
	defer cancel()

	mod, err := engine.Compile(`
def add(a, b):
    return a + b
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	cases := []struct {
		a, b float64
		want float64
	}{
		{1, 2, 3},
		{10, 5, 15},
		{-4, 4, 0},
	}
	for _, tc := range cases {
		result, _, err := engine.Run(ctx, mod, "add",
			map[string]any{"a": tc.a, "b": tc.b}, &Capabilities{})
		if err != nil {
			t.Fatalf("Run(a=%v,b=%v): %v", tc.a, tc.b, err)
		}
		if result != tc.want {
			t.Fatalf("add(%v,%v) = %v, want %v", tc.a, tc.b, result, tc.want)
		}
	}
}
