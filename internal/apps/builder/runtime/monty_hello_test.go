package runtime

import (
	"context"
	"testing"
	"time"
)

// TestMontyHello is a proof-of-dependency smoke test: boot the Monty runner,
// run a trivial Python snippet that uses a named input, and verify the result.
func TestMontyHello(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runner, err := New()
	if err != nil {
		t.Fatalf("monty.New: %v", err)
	}
	defer func() {
		if cerr := runner.Close(); cerr != nil {
			t.Errorf("runner.Close: %v", cerr)
		}
	}()

	// Monty's Execute evaluates the code and returns the final expression's
	// value. Define a function, call it, and leave the call as the trailing
	// expression so it's returned to Go.
	code := `
def greet(name):
    return "hello " + name

greet(input_name)
`
	result, err := runner.Execute(ctx, code, map[string]any{"input_name": "kit"})
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
