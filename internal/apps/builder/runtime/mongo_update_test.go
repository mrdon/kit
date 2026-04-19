package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// TODO(Phase 2g integration): add a concurrency test against real Postgres
// that spawns N goroutines each running an UPDATE ... $push 'notes' ... and
// verifies all N entries land in the array. The translator already emits a
// single atomic expression per call, so the semantics to verify live at the
// UPDATE-statement level — out of scope for this unit test file, which has
// no DB dependency.

func TestTranslateUpdate_Set(t *testing.T) {
	set, params, err := TranslateUpdate(map[string]any{
		"$set": map[string]any{"email": "x@example.com", "age": 40},
	}, 2)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	wantContains := []string{
		`data = (data || $2::jsonb)`,
		`|| jsonb_build_object('_updated_at', now()::text)`,
		`updated_at = now()`,
	}
	for _, w := range wantContains {
		if !strings.Contains(set, w) {
			t.Errorf("SET missing %q\nfull: %s", w, set)
		}
	}

	if len(params) != 1 {
		t.Fatalf("want 1 param, got %d: %v", len(params), params)
	}
	// The $set param is a JSON blob with keys in sorted order.
	s, ok := params[0].(string)
	if !ok {
		t.Fatalf("param[0] not string: %T", params[0])
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(s), &got); err != nil {
		t.Fatalf("param[0] not valid json: %v", err)
	}
	if got["email"] != "x@example.com" || got["age"] != float64(40) {
		t.Errorf("unexpected $set blob: %v", got)
	}
}

func TestTranslateUpdate_Unset(t *testing.T) {
	set, params, err := TranslateUpdate(map[string]any{
		"$unset": map[string]any{"email": "", "phone": ""},
	}, 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Two subtractions, alphabetical order.
	if !strings.Contains(set, `(data - $1)`) {
		t.Errorf("missing first subtraction: %s", set)
	}
	if !strings.Contains(set, `- $2)`) {
		t.Errorf("missing second subtraction: %s", set)
	}
	if len(params) != 2 || params[0] != "email" || params[1] != "phone" {
		t.Errorf("want params [email phone], got %v", params)
	}
}

func TestTranslateUpdate_Push(t *testing.T) {
	set, params, err := TranslateUpdate(map[string]any{
		"$push": map[string]any{"notes": map[string]any{"text": "hi"}},
	}, 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := `jsonb_set(data, '{"notes"}', COALESCE(data->'notes', '[]'::jsonb) || $1::jsonb)`
	if !strings.Contains(set, want) {
		t.Errorf("missing push expr %q\nfull: %s", want, set)
	}
	if len(params) != 1 {
		t.Fatalf("want 1 param, got %d", len(params))
	}
	// Push wraps the value in a one-element array.
	var arr []map[string]any
	if err := json.Unmarshal([]byte(params[0].(string)), &arr); err != nil {
		t.Fatalf("param not array json: %v", err)
	}
	if len(arr) != 1 || arr[0]["text"] != "hi" {
		t.Errorf("unexpected push payload: %v", arr)
	}
}

func TestTranslateUpdate_Pull(t *testing.T) {
	set, params, err := TranslateUpdate(map[string]any{
		"$pull": map[string]any{"tags": "old"},
	}, 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(set, `jsonb_array_elements(COALESCE(data->'tags', '[]'::jsonb))`) {
		t.Errorf("missing jsonb_array_elements: %s", set)
	}
	if !strings.Contains(set, `WHERE x <> $1::jsonb`) {
		t.Errorf("missing filter clause: %s", set)
	}
	if len(params) != 1 || params[0] != `"old"` {
		t.Errorf("want param [\"old\"], got %v", params)
	}
}

func TestTranslateUpdate_AddToSet(t *testing.T) {
	set, params, err := TranslateUpdate(map[string]any{
		"$addToSet": map[string]any{"tags": "new"},
	}, 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(set, `@> $1::jsonb`) {
		t.Errorf("missing containment check: %s", set)
	}
	if !strings.Contains(set, `|| $1::jsonb`) {
		t.Errorf("missing append branch: %s", set)
	}
	if len(params) != 1 {
		t.Fatalf("want 1 param, got %d", len(params))
	}
	var arr []string
	if err := json.Unmarshal([]byte(params[0].(string)), &arr); err != nil {
		t.Fatalf("param not array json: %v", err)
	}
	if len(arr) != 1 || arr[0] != "new" {
		t.Errorf("unexpected addToSet payload: %v", arr)
	}
}

func TestTranslateUpdate_Inc(t *testing.T) {
	set, params, err := TranslateUpdate(map[string]any{
		"$inc": map[string]any{"visits": 3},
	}, 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := `jsonb_set(data, '{"visits"}', to_jsonb(COALESCE((data->>'visits')::numeric, 0) + $1))`
	if !strings.Contains(set, want) {
		t.Errorf("missing inc expr %q\nfull: %s", want, set)
	}
	if len(params) != 1 || params[0] != float64(3) {
		t.Errorf("want params [3.0], got %v", params)
	}
}

func TestTranslateUpdate_Composed(t *testing.T) {
	// Matches the walk-through in the task spec: $set + $inc + $push.
	set, params, err := TranslateUpdate(map[string]any{
		"$set":  map[string]any{"email": "x"},
		"$inc":  map[string]any{"visits": 1},
		"$push": map[string]any{"tags": "new"},
	}, 2)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// Three params allocated: one for $set, one for $inc, one for $push.
	if len(params) != 3 {
		t.Fatalf("want 3 params, got %d: %v", len(params), params)
	}
	// Order matches apply order: set ($2), inc ($3), push ($4).
	checks := []string{
		`$2::jsonb`,    // $set
		`+ $3)`,        // $inc
		`|| $4::jsonb`, // $push
		`updated_at = now()`,
		`|| jsonb_build_object('_updated_at', now()::text)`,
	}
	for _, c := range checks {
		if !strings.Contains(set, c) {
			t.Errorf("composed SET missing %q\nfull: %s", c, set)
		}
	}

	// Sanity: the $set jsonb should land at $2.
	if params[0] == nil {
		t.Errorf("param 0 nil")
	}
	if params[1] != float64(1) {
		t.Errorf("param 1 (inc) = %v, want 1.0", params[1])
	}
	// Push value is a wrapped array.
	var arr []string
	if err := json.Unmarshal([]byte(params[2].(string)), &arr); err != nil {
		t.Fatalf("param 2 not array json: %v", err)
	}
	if len(arr) != 1 || arr[0] != "new" {
		t.Errorf("push payload wrong: %v", arr)
	}
}

func TestTranslateUpdate_ComposedTwoOps(t *testing.T) {
	set, _, err := TranslateUpdate(map[string]any{
		"$set":   map[string]any{"a": 1},
		"$unset": map[string]any{"b": ""},
	}, 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// $set runs first (wrapping data with ||), $unset wraps that.
	// Expected overall shape: ((data || $1::jsonb) - $2) || jsonb_build_object(...)
	if !strings.Contains(set, `((data || $1::jsonb) - $2)`) {
		t.Errorf("expected nested composition, got: %s", set)
	}
}

func TestTranslateUpdate_UpdatedAtAlwaysAppended(t *testing.T) {
	// Even a minimal update injects _updated_at as the last merge.
	set, _, err := TranslateUpdate(map[string]any{
		"$set": map[string]any{"x": 1},
	}, 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// The _updated_at merge must be the FINAL `||`, so any user-supplied
	// _updated_at inside $set is overwritten.
	if !strings.HasSuffix(
		strings.TrimSpace(strings.Split(set, ", updated_at = now()")[0]),
		`|| jsonb_build_object('_updated_at', now()::text)`,
	) {
		t.Errorf("_updated_at not the final || expression: %s", set)
	}
}

func TestTranslateUpdate_UpdatedAtOverridesUserSet(t *testing.T) {
	// If the user $sets _updated_at to something bogus, the always-append
	// merge still wins.
	set, _, err := TranslateUpdate(map[string]any{
		"$set": map[string]any{"_updated_at": "bogus"},
	}, 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// The `|| jsonb_build_object('_updated_at', now()::text)` must appear
	// after the $set merge in the source order.
	setIdx := strings.Index(set, `$1::jsonb`)
	updIdx := strings.Index(set, `jsonb_build_object('_updated_at'`)
	if setIdx < 0 || updIdx < 0 || updIdx < setIdx {
		t.Errorf("_updated_at not appended after $set: %s", set)
	}
}

func TestTranslateUpdate_UnknownOperator(t *testing.T) {
	_, _, err := TranslateUpdate(map[string]any{
		"$rename": map[string]any{"a": "b"},
	}, 1)
	if err == nil {
		t.Fatal("expected error for unknown operator")
	}
	if !strings.Contains(err.Error(), "$rename") {
		t.Errorf("error should name the operator: %v", err)
	}
}

func TestTranslateUpdate_IncNonNumeric(t *testing.T) {
	_, _, err := TranslateUpdate(map[string]any{
		"$inc": map[string]any{"visits": "three"},
	}, 1)
	if err == nil {
		t.Fatal("expected error for non-numeric $inc")
	}
	if !strings.Contains(err.Error(), "not numeric") {
		t.Errorf("error should say 'not numeric': %v", err)
	}
}

func TestTranslateUpdate_IncBoolRejected(t *testing.T) {
	// bools masquerading as numbers are a common bug source — reject.
	_, _, err := TranslateUpdate(map[string]any{
		"$inc": map[string]any{"visits": true},
	}, 1)
	if err == nil {
		t.Fatal("expected error for bool $inc")
	}
}

func TestTranslateUpdate_Empty(t *testing.T) {
	_, _, err := TranslateUpdate(map[string]any{}, 1)
	if err == nil {
		t.Fatal("expected error for empty update")
	}
	if !strings.Contains(err.Error(), "at least one operator") {
		t.Errorf("error should mention missing operator: %v", err)
	}
}

func TestTranslateUpdate_EmptyOperatorObject(t *testing.T) {
	// $set: {} is user error — fail fast.
	_, _, err := TranslateUpdate(map[string]any{
		"$set": map[string]any{},
	}, 1)
	if err == nil {
		t.Fatal("expected error for empty $set")
	}
}

func TestTranslateUpdate_OperatorValueNotObject(t *testing.T) {
	_, _, err := TranslateUpdate(map[string]any{
		"$set": "not-an-object",
	}, 1)
	if err == nil {
		t.Fatal("expected error for $set with non-object value")
	}
}

func TestTranslateUpdate_ParamIndexing(t *testing.T) {
	// Caller reserves $1..$3 for WHERE args, so startIdx=4. First generated
	// placeholder must be $4.
	set, _, err := TranslateUpdate(map[string]any{
		"$set": map[string]any{"a": 1},
	}, 4)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(set, "$4::jsonb") {
		t.Errorf("expected $4 placeholder, got: %s", set)
	}
	if strings.Contains(set, "$1::jsonb") || strings.Contains(set, "$2::jsonb") {
		t.Errorf("leaked low-index placeholder: %s", set)
	}
}

// TestTranslateUpdate_ComposedExampleSQL renders the composed example from
// the task spec and prints it so humans can eyeball the generated SQL. Not
// a strict assertion — the checks above already pin the structure.
func TestTranslateUpdate_ComposedExampleSQL(t *testing.T) {
	set, params, err := TranslateUpdate(map[string]any{
		"$set":  map[string]any{"email": "x"},
		"$inc":  map[string]any{"visits": 1},
		"$push": map[string]any{"tags": "new"},
	}, 2)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Logf("composed SET:\n%s", set)
	t.Logf("params: %v", params)
}
