// Unit tests for meta_common.go — these are pure-Go helpers (no DB) aside
// from loadBuilderAppByName, which is covered via meta_apps_test.go where the
// fixture is already in play. Keeping the meta_common tests pool-free means
// they run in milliseconds and don't depend on Postgres being up.
package builder

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mrdon/kit/internal/services"
)

func TestArgString_MissingAndPresent(t *testing.T) {
	// Present: the existing builder argString is shared with Monty host
	// calls; re-check here so refactors of that helper don't break the
	// meta-tool call sites that rely on the same semantics.
	got, err := argString(map[string]any{"name": "crm"}, "name")
	if err != nil {
		t.Fatalf("argString: %v", err)
	}
	if got != "crm" {
		t.Errorf("argString = %q, want %q", got, "crm")
	}

	// Missing key.
	if _, err := argString(map[string]any{}, "name"); err == nil {
		t.Error("argString(missing): want error, got nil")
	}

	// Wrong type (number instead of string).
	if _, err := argString(map[string]any{"name": 42}, "name"); err == nil {
		t.Error("argString(wrong type): want error, got nil")
	}

	// Empty string — existing argString rejects to keep Monty call-sites
	// tidy; meta-tools that want to allow empty pass through argOptionalString.
	if _, err := argString(map[string]any{"name": ""}, "name"); err == nil {
		t.Error("argString(empty): want error, got nil")
	}
}

func TestArgStringList_EmptyAndNonList(t *testing.T) {
	// Absent key → empty slice, not nil. Handlers that range can skip a
	// nil check.
	got, err := argStringList(map[string]any{}, "tags")
	if err != nil {
		t.Fatalf("argStringList(missing): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("argStringList(missing) = %v, want empty slice", got)
	}

	// Explicit nil also returns empty.
	got, err = argStringList(map[string]any{"tags": nil}, "tags")
	if err != nil {
		t.Fatalf("argStringList(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("argStringList(nil) = %v, want empty", got)
	}

	// Populated list.
	got, err = argStringList(map[string]any{"tags": []any{"a", "b"}}, "tags")
	if err != nil {
		t.Fatalf("argStringList(list): %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("argStringList(list) = %v, want [a b]", got)
	}

	// Not a list at all — string, map, etc.
	if _, err := argStringList(map[string]any{"tags": "oops"}, "tags"); err == nil {
		t.Error("argStringList(non-list): want error, got nil")
	}

	// List with a non-string item — we refuse to coerce.
	if _, err := argStringList(map[string]any{"tags": []any{"a", 7}}, "tags"); err == nil {
		t.Error("argStringList(mixed types): want error, got nil")
	}
}

func TestGuardAdmin_ForbiddenPath(t *testing.T) {
	// Nil caller → forbidden. MCP handlers resolve the caller from ctx and
	// WithCaller already returns an auth error when it's missing, but the
	// guard is defensive — a direct call with nil should never panic.
	if err := guardAdmin(nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("guardAdmin(nil) = %v, want ErrForbidden", err)
	}

	// Non-admin caller.
	nonAdmin := &services.Caller{IsAdmin: false}
	if err := guardAdmin(nonAdmin); !errors.Is(err, ErrForbidden) {
		t.Errorf("guardAdmin(non-admin) = %v, want ErrForbidden", err)
	}

	// Admin — passes.
	admin := &services.Caller{IsAdmin: true}
	if err := guardAdmin(admin); err != nil {
		t.Errorf("guardAdmin(admin) = %v, want nil", err)
	}
}

func TestRequireConfirm(t *testing.T) {
	// Missing confirm → ErrMissingConfirm. Destructive ops should never
	// fire accidentally.
	if err := requireConfirm(map[string]any{}); !errors.Is(err, ErrMissingConfirm) {
		t.Errorf("requireConfirm(missing) = %v, want ErrMissingConfirm", err)
	}

	// Explicit false → same.
	if err := requireConfirm(map[string]any{"confirm": false}); !errors.Is(err, ErrMissingConfirm) {
		t.Errorf("requireConfirm(false) = %v, want ErrMissingConfirm", err)
	}

	// Explicit true → nil.
	if err := requireConfirm(map[string]any{"confirm": true}); err != nil {
		t.Errorf("requireConfirm(true) = %v, want nil", err)
	}

	// Wrong type — a string "true" is NOT accepted. We insist on a real
	// bool so a hallucinated stringified flag doesn't silently drop the
	// user into a destructive path they didn't opt into.
	if err := requireConfirm(map[string]any{"confirm": "true"}); err == nil {
		t.Error("requireConfirm(string): want error, got nil")
	}
}

func TestParseInput(t *testing.T) {
	// Empty raw → empty map (not nil) so handlers can pass it straight to
	// arg* helpers without a nil guard.
	m, err := parseInput(nil)
	if err != nil {
		t.Fatalf("parseInput(nil): %v", err)
	}
	if m == nil {
		t.Error("parseInput(nil) returned nil map, want empty")
	}

	// Valid JSON object.
	m, err = parseInput(json.RawMessage(`{"name":"crm"}`))
	if err != nil {
		t.Fatalf("parseInput(object): %v", err)
	}
	if m["name"] != "crm" {
		t.Errorf("parseInput(object) = %v, want name=crm", m)
	}

	// A JSON array is not a valid meta-tool input shape.
	if _, err := parseInput(json.RawMessage(`[1,2,3]`)); err == nil {
		t.Error("parseInput(array): want error, got nil")
	}
}

func TestArgOptionalJSON(t *testing.T) {
	// Absent key → nil, no error. Handlers use this for free-form subtrees
	// (args_schema, data payloads) so downstream Phase 4 subtasks can tell
	// "caller didn't send anything" apart from "caller sent an empty object".
	got, err := argOptionalJSON(map[string]any{}, "args_schema")
	if err != nil {
		t.Fatalf("absent: %v", err)
	}
	if got != nil {
		t.Errorf("absent = %s, want nil", string(got))
	}

	// Explicit null is also nil.
	got, err = argOptionalJSON(map[string]any{"args_schema": nil}, "args_schema")
	if err != nil {
		t.Fatalf("null: %v", err)
	}
	if got != nil {
		t.Errorf("null = %s, want nil", string(got))
	}

	// Populated object round-trips as JSON.
	got, err = argOptionalJSON(
		map[string]any{"args_schema": map[string]any{"type": "object"}},
		"args_schema",
	)
	if err != nil {
		t.Fatalf("object: %v", err)
	}
	if string(got) != `{"type":"object"}` {
		t.Errorf("object = %s, want {\"type\":\"object\"}", string(got))
	}
}

func TestFormatToolResult(t *testing.T) {
	s, err := formatToolResult(map[string]any{"deleted": "crm"})
	if err != nil {
		t.Fatalf("formatToolResult: %v", err)
	}
	// Must round-trip through json.Unmarshal — meta-tools rely on JSON
	// output so the LLM can reason about the result structurally.
	var back map[string]any
	if err := json.Unmarshal([]byte(s), &back); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if back["deleted"] != "crm" {
		t.Errorf("round-trip lost field: %v", back)
	}
}
