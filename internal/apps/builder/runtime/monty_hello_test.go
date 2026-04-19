package runtime

import (
	"testing"
)

// TestMontyHello is a proof-of-dependency smoke test: boot the Monty runner,
// run a trivial Python snippet that uses a named input, and verify the result.
func TestMontyHello(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	// Monty's Execute evaluates the code and returns the final expression's
	// value. Define a function, call it, and leave the call as the trailing
	// expression so it's returned to Go.
	code := `
def greet(name):
    return "hello " + name

greet(input_name)
`
	result, err := testRunner.Execute(ctx, code, map[string]any{"input_name": "kit"})
	if err != nil {
		t.Fatalf("runner.Execute: %v", err)
	}

	got, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T: %v", result, result)
	}
	if want := "hello kit"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
