package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func newCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// TestHostCallScalarRoundTrip: a host function takes a string, returns a dict.
// Confirms Python sees the dict and fields are intact.
func TestHostCallScalarRoundTrip(t *testing.T) {
	runner := testRunner
	ctx, cancel := newCtx(t)
	defer cancel()

	handler := func(_ context.Context, call *FunctionCall) (any, error) {
		if call.Name != "find_user" {
			return nil, fmt.Errorf("unexpected fn %q", call.Name)
		}
		name, _ := call.Args["name"].(string)
		return map[string]any{"id": "u_123", "name": name}, nil
	}

	code := `
u = find_user("jane")
{"id": u["id"], "name": u["name"]}
`
	result, err := runner.Execute(ctx, code, nil,
		WithExternalFunc(handler, Func("find_user", "name")))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["id"] != "u_123" {
		t.Fatalf("id = %v, want u_123", m["id"])
	}
	if m["name"] != "jane" {
		t.Fatalf("name = %v, want jane", m["name"])
	}
}

// TestHostCallKwargs verifies that Python-side keyword args are delivered to
// the Go callback via Args keyed by param name — same shape as positional.
func TestHostCallKwargs(t *testing.T) {
	runner := testRunner
	ctx, cancel := newCtx(t)
	defer cancel()

	var gotArgs map[string]any
	handler := func(_ context.Context, call *FunctionCall) (any, error) {
		gotArgs = call.Args
		return map[string]any{
			"ok":       true,
			"title":    call.Args["title"],
			"priority": call.Args["priority"],
		}, nil
	}

	// Pass as kwargs — monty maps both positional and kwargs into Args.
	code := `create_todo(title="x", priority="high")`
	result, err := runner.Execute(ctx, code, nil,
		WithExternalFunc(handler, Func("create_todo", "title", "priority")))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gotArgs["title"] != "x" {
		t.Fatalf("title = %v, want x", gotArgs["title"])
	}
	if gotArgs["priority"] != "high" {
		t.Fatalf("priority = %v, want high", gotArgs["priority"])
	}
	m := result.(map[string]any)
	if m["ok"] != true || m["title"] != "x" || m["priority"] != "high" {
		t.Fatalf("result = %v", m)
	}
}

// TestHostCallNestedData: return a list of dicts, verify Python can index and
// read fields, and Go gets it back too.
func TestHostCallNestedData(t *testing.T) {
	runner := testRunner
	ctx, cancel := newCtx(t)
	defer cancel()

	handler := func(_ context.Context, _ *FunctionCall) (any, error) {
		return []any{
			map[string]any{"id": "t1", "done": false},
			map[string]any{"id": "t2", "done": true},
		}, nil
	}

	code := `
todos = find_todos()
# prove list/dict access works in Python, then pass through to Go
[{"id": t["id"], "done": t["done"]} for t in todos]
`
	result, err := runner.Execute(ctx, code, nil,
		WithExternalFunc(handler, Func("find_todos")))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	list, ok := result.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected list of 2, got %T %v", result, result)
	}
	first := list[0].(map[string]any)
	second := list[1].(map[string]any)
	if first["id"] != "t1" || first["done"] != false {
		t.Fatalf("first = %v", first)
	}
	if second["id"] != "t2" || second["done"] != true {
		t.Fatalf("second = %v", second)
	}
}

// TestHostCallErrorPropagates: a Go host callback error aborts Execute and
// bubbles up as a Go error. In the current monty-go, Python cannot catch it
// via try/except — the interpreter is unwound and Execute returns.
func TestHostCallErrorPropagates(t *testing.T) {
	runner := testRunner
	ctx, cancel := newCtx(t)
	defer cancel()

	handler := func(_ context.Context, _ *FunctionCall) (any, error) {
		return nil, errors.New("db unreachable")
	}

	// Even with try/except around it, the error should still bubble to Go.
	code := `
try:
    explode()
except Exception:
    "caught"
"unreached"
`
	_, err := runner.Execute(ctx, code, nil,
		WithExternalFunc(handler, Func("explode")))
	if err == nil {
		t.Fatal("expected error from Execute, got nil")
	}
	if !strings.Contains(err.Error(), "db unreachable") {
		t.Fatalf("expected error to mention underlying cause, got %v", err)
	}
	// The wrapper mentions the function name — useful for debugging.
	if !strings.Contains(err.Error(), "explode") {
		t.Fatalf("expected error to mention fn name, got %v", err)
	}
}

// TestHostCallUnknownFunction: if Python calls a name not registered in the
// allowlist, the script gets a NameError — surfacing in Go as *MontyError.
// (Compile succeeds; the failure is at runtime.)
func TestHostCallUnknownFunction(t *testing.T) {
	runner := testRunner
	ctx, cancel := newCtx(t)
	defer cancel()

	handler := func(_ context.Context, call *FunctionCall) (any, error) {
		t.Errorf("handler should not be invoked for unregistered names, got %q", call.Name)
		return nil, fmt.Errorf("unreachable: handler invoked for %q", call.Name)
	}

	_, err := runner.Execute(ctx, "not_registered()", nil,
		WithExternalFunc(handler, Func("registered_only")))
	if err == nil {
		t.Fatal("expected error for unregistered function, got nil")
	}
	var montyErr *MontyError
	if !errors.As(err, &montyErr) {
		t.Fatalf("expected *MontyError, got %T: %v", err, err)
	}
	if !strings.Contains(montyErr.Message, "NameError") {
		t.Fatalf("expected NameError in message, got %q", montyErr.Message)
	}
}

// TestHostCallMultipleFunctions: register two functions in one Execute, call
// both, verify each fires with correct args.
func TestHostCallMultipleFunctions(t *testing.T) {
	runner := testRunner
	ctx, cancel := newCtx(t)
	defer cancel()

	var mu sync.Mutex
	calls := []string{}
	handler := func(_ context.Context, call *FunctionCall) (any, error) {
		mu.Lock()
		calls = append(calls, call.Name)
		mu.Unlock()
		switch call.Name {
		case "get_count":
			return float64(7), nil
		case "double":
			n, _ := call.Args["n"].(float64)
			return n * 2, nil
		default:
			return nil, fmt.Errorf("unknown: %s", call.Name)
		}
	}

	code := `double(get_count())`
	result, err := runner.Execute(ctx, code, nil,
		WithExternalFunc(handler,
			Func("get_count"),
			Func("double", "n"),
		))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != float64(14) {
		t.Fatalf("result = %v, want 14", result)
	}
	if len(calls) != 2 || calls[0] != "get_count" || calls[1] != "double" {
		t.Fatalf("call sequence = %v, want [get_count double]", calls)
	}
}

// TestHostCallChained: Python calls find_user, uses the result to call
// create_todo. This is the real pattern skills will follow — one tool's
// output feeds the next.
func TestHostCallChained(t *testing.T) {
	runner := testRunner
	ctx, cancel := newCtx(t)
	defer cancel()

	var createdFor string
	handler := func(_ context.Context, call *FunctionCall) (any, error) {
		switch call.Name {
		case "find_user":
			name, _ := call.Args["name"].(string)
			if name != "alice" {
				return nil, fmt.Errorf("unexpected name %q", name)
			}
			return map[string]any{"id": "u_42", "name": name}, nil
		case "create_todo":
			createdFor, _ = call.Args["assignee_id"].(string)
			title, _ := call.Args["title"].(string)
			return map[string]any{"id": "todo_1", "title": title, "assignee_id": createdFor}, nil
		default:
			return nil, fmt.Errorf("unknown: %s", call.Name)
		}
	}

	code := `
user = find_user(name="alice")
todo = create_todo(title="ship it", assignee_id=user["id"])
{"todo_id": todo["id"], "assignee_id": todo["assignee_id"]}
`
	result, err := runner.Execute(ctx, code, nil,
		WithExternalFunc(handler,
			Func("find_user", "name"),
			Func("create_todo", "title", "assignee_id"),
		))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T %v", result, result)
	}
	if m["todo_id"] != "todo_1" {
		t.Fatalf("todo_id = %v", m["todo_id"])
	}
	if m["assignee_id"] != "u_42" {
		t.Fatalf("assignee_id = %v", m["assignee_id"])
	}
	if createdFor != "u_42" {
		t.Fatalf("Go-side createdFor = %q, want u_42", createdFor)
	}
}
