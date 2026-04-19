package runtime

import (
	"fmt"
	"os"
	"testing"
)

// testRunner is a package-scoped Runner shared across every test in this
// package. Building one costs ~5s (wazero compile + WASM module bring-up),
// so paying it once per `go test` invocation — instead of once per test file —
// is the difference between a ~25s and a ~5s suite.
//
// Runner is safe to reuse across Execute calls; each Execute spins its own
// isolated module instance internally. Do NOT call testRunner.Close() from
// individual tests — TestMain owns the lifecycle.
var testRunner *Runner

// testEngine is a package-scoped MontyEngine wrapping testRunner. Tests
// that need engine-level behaviour (Compile/Run, built-ins, Metadata) should
// use this directly instead of constructing their own engine — otherwise they
// pay the Runner cold-start a second time. The engine does NOT own the
// Runner; TestMain handles Runner.Close().
var testEngine *MontyEngine

func TestMain(m *testing.M) {
	r, err := New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create test runner: %v\n", err)
		os.Exit(1)
	}
	testRunner = r
	testEngine = NewMontyEngine(r)
	code := m.Run()
	_ = r.Close()
	os.Exit(code)
}
