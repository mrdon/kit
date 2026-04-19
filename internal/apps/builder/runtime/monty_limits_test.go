package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// containsAny reports whether s contains any of the provided substrings,
// case-insensitive. Keeps the kill-criteria assertions lenient about exact
// wording from the Monty shim.
func containsAny(s string, subs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// TestWallClockLimit: a Python busy loop must be aborted by MaxDuration.
// We care that it actually aborts, not the exact number.
func TestWallClockLimit(t *testing.T) {
	runner := testRunner
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code := `
while True:
    pass
`
	start := time.Now()
	_, err := runner.Execute(ctx, code, nil,
		WithLimits(Limits{MaxDuration: 100 * time.Millisecond}))
	elapsed := time.Since(start)
	t.Logf("wall-clock limit elapsed: %v", elapsed)

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
	if !containsAny(montyErr.Message, "time", "duration", "timeout", "deadline", "limit") {
		t.Fatalf("expected message to hint at time/duration limit, got %q", montyErr.Message)
	}
}

// TestContextCancelInterrupt measures the latency between ctx.Cancel() and
// Execute returning. The kill criterion is p99 < 500ms. With no Monty-side
// limits, the only way out of a busy loop is the Go context.
func TestContextCancelInterrupt(t *testing.T) {
	runner := testRunner
	ctx, cancel := context.WithCancel(context.Background())

	code := `
while True:
    pass
`

	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)

	go func() {
		start := time.Now()
		// No Monty-side limits — rely entirely on ctx cancellation.
		_, err := runner.Execute(ctx, code, nil)
		done <- result{err: err, elapsed: time.Since(start)}
	}()

	// Let the Python loop actually get going, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancelAt := time.Now()
	cancel()

	select {
	case r := <-done:
		interruptLatency := time.Since(cancelAt)
		t.Logf("ctx.Cancel interrupt latency: %v (total Execute elapsed: %v)", interruptLatency, r.elapsed)

		if r.err == nil {
			t.Fatal("expected error after ctx cancel, got nil")
		}
		// The Go-side ctx.Err() check in the progress loop OR a wazero
		// CloseOnContextDone teardown should surface the cancellation.
		// We accept either a wrapped context.Canceled OR a MontyError
		// that came from the interpreter being torn down mid-flight.
		var montyErr *MontyError
		isCancel := errors.Is(r.err, context.Canceled)
		isMonty := errors.As(r.err, &montyErr)
		if !isCancel && !isMonty {
			t.Fatalf("expected context.Canceled or *MontyError, got %T: %v", r.err, r.err)
		}
		if interruptLatency > 500*time.Millisecond {
			t.Fatalf("interrupt latency %v exceeds 500ms p99 kill criterion", interruptLatency)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return within 5s of ctx.Cancel — interrupt is broken")
	}
}

// TestMemoryLimit: a Python script that tries to allocate ~400MB of list
// storage must fail when MaxMemoryBytes is 10MB.
func TestMemoryLimit(t *testing.T) {
	runner := testRunner
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := `x = [0] * 50_000_000
len(x)`

	start := time.Now()
	_, err := runner.Execute(ctx, code, nil,
		WithLimits(Limits{MaxMemoryBytes: 10_000_000}))
	t.Logf("memory limit elapsed: %v", time.Since(start))

	if err == nil {
		t.Fatal("expected error from MaxMemoryBytes, got nil")
	}
	var montyErr *MontyError
	if !errors.As(err, &montyErr) {
		t.Fatalf("expected *MontyError, got %T: %v", err, err)
	}
	if !containsAny(montyErr.Message, "memory", "alloc", "limit") {
		t.Fatalf("expected message to hint at memory limit, got %q", montyErr.Message)
	}
}

// TestRecursionLimit: unbounded recursion must trip MaxRecursionDepth.
func TestRecursionLimit(t *testing.T) {
	runner := testRunner
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := `
def f(n):
    return f(n + 1)
f(0)
`

	start := time.Now()
	_, err := runner.Execute(ctx, code, nil,
		WithLimits(Limits{MaxRecursionDepth: 100}))
	t.Logf("recursion limit elapsed: %v", time.Since(start))

	if err == nil {
		t.Fatal("expected error from MaxRecursionDepth, got nil")
	}
	var montyErr *MontyError
	if !errors.As(err, &montyErr) {
		t.Fatalf("expected *MontyError, got %T: %v", err, err)
	}
	if !containsAny(montyErr.Message, "recursion", "depth", "stack", "limit") {
		t.Fatalf("expected message to hint at recursion depth, got %q", montyErr.Message)
	}
}

// TestMaxAllocationsLimit: many small allocations in a tight loop must trip
// MaxAllocations well before the range() exhausts.
func TestMaxAllocationsLimit(t *testing.T) {
	runner := testRunner
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := `
for i in range(10_000_000):
    x = []
`

	start := time.Now()
	_, err := runner.Execute(ctx, code, nil,
		WithLimits(Limits{MaxAllocations: 10_000}))
	t.Logf("max-allocations limit elapsed: %v", time.Since(start))

	if err == nil {
		t.Fatal("expected error from MaxAllocations, got nil")
	}
	var montyErr *MontyError
	if !errors.As(err, &montyErr) {
		t.Fatalf("expected *MontyError, got %T: %v", err, err)
	}
	if !containsAny(montyErr.Message, "alloc", "memory", "limit") {
		t.Fatalf("expected message to hint at allocations/memory limit, got %q", montyErr.Message)
	}
}

// TestLimitsDontAffectNormalExecution: a trivial script under generous
// limits must return its result cleanly. Regression guard that the limits
// plumbing doesn't accidentally reject healthy programs.
func TestLimitsDontAffectNormalExecution(t *testing.T) {
	runner := testRunner
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := runner.Execute(ctx, "1 + 1", nil,
		WithLimits(Limits{
			MaxDuration:    10 * time.Second,
			MaxMemoryBytes: 100 * 1024 * 1024,
		}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != float64(2) {
		t.Fatalf("result = %v (%T), want 2.0", result, result)
	}
}
