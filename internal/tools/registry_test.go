// Registry-side tests for the Phase 4d visibility rules. The registry owns
// two filtering axes — AdminOnly and VisibleToRoles — and these tests
// exercise each independently plus the interaction between them. No DB is
// required; the exposed-tool runner is mocked via a tiny stub that
// implements ExposedToolRunner.
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/services"
)

// stubRunner implements ExposedToolRunner for the registry tests. It
// returns the defs passed in at construction — no pool, no DB.
type stubRunner struct {
	defs []ExposedToolDef
	err  error
}

func (s *stubRunner) List(_ context.Context, _ *services.Caller) ([]ExposedToolDef, error) {
	return s.defs, s.err
}

// restoreRunner lets tests swap the package-global runner in and out
// without leaking state into sibling tests.
func withRunner(t *testing.T, r ExposedToolRunner) {
	t.Helper()
	prev := currentExposedToolRunner
	SetExposedToolRunner(r)
	t.Cleanup(func() { SetExposedToolRunner(prev) })
}

func TestAnyIntersect(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both empty", nil, nil, false},
		{"a empty", nil, []string{"x"}, false},
		{"b empty", []string{"x"}, nil, false},
		{"no overlap", []string{"x"}, []string{"y"}, false},
		{"one overlap", []string{"x", "y"}, []string{"y"}, true},
		{"case sensitive", []string{"X"}, []string{"x"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := anyIntersect(c.a, c.b); got != c.want {
				t.Errorf("anyIntersect(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestIsDefVisible_AdminOnly(t *testing.T) {
	admin := &services.Caller{IsAdmin: true}
	user := &services.Caller{IsAdmin: false}

	adminTool := Def{Name: "purge", AdminOnly: true}
	openTool := Def{Name: "search"}

	if !IsDefVisible(adminTool, admin) {
		t.Error("admin should see adminTool")
	}
	if IsDefVisible(adminTool, user) {
		t.Error("non-admin should not see adminTool")
	}
	if !IsDefVisible(openTool, user) {
		t.Error("non-admin should see openTool")
	}
}

func TestIsDefVisible_RoleGated(t *testing.T) {
	bartender := &services.Caller{Roles: []string{"bartender"}}
	manager := &services.Caller{Roles: []string{"manager"}}

	gated := Def{Name: "lookup", VisibleToRoles: []string{"bartender"}}

	if !IsDefVisible(gated, bartender) {
		t.Error("bartender should see gated tool")
	}
	if IsDefVisible(gated, manager) {
		t.Error("manager should not see gated tool")
	}
	// Nil caller is maximally restrictive.
	if IsDefVisible(gated, nil) {
		t.Error("nil caller should not see gated tool")
	}
}

func TestIsDefVisible_AdminAndRole(t *testing.T) {
	// AdminOnly + VisibleToRoles is AND, not OR. An admin without the role
	// is still excluded, and a role-holder without admin is excluded.
	adminNoRole := &services.Caller{IsAdmin: true}
	roleNoAdmin := &services.Caller{Roles: []string{"bartender"}}
	both := &services.Caller{IsAdmin: true, Roles: []string{"bartender"}}

	d := Def{Name: "x", AdminOnly: true, VisibleToRoles: []string{"bartender"}}

	if IsDefVisible(d, adminNoRole) {
		t.Error("admin without role should not see tool")
	}
	if IsDefVisible(d, roleNoAdmin) {
		t.Error("role holder without admin should not see tool")
	}
	if !IsDefVisible(d, both) {
		t.Error("admin+role should see tool")
	}
}

func TestNewRegistry_ExposedToolsFilteredByRole(t *testing.T) {
	// Two exposed tools: one for bartenders, one for managers. A bartender
	// caller sees only the bartender one. A manager caller sees only theirs.
	withRunner(t, &stubRunner{defs: []ExposedToolDef{
		{
			ToolName:       "lookup",
			Description:    "bartender only",
			ArgsSchema:     map[string]any{"type": "object"},
			VisibleToRoles: []string{"bartender"},
			Invoke:         func(_ context.Context, _ *ExecContext, _ map[string]any) (string, error) { return "", nil },
		},
		{
			ToolName:       "reports",
			Description:    "manager only",
			ArgsSchema:     map[string]any{"type": "object"},
			VisibleToRoles: []string{"manager"},
			Invoke:         func(_ context.Context, _ *ExecContext, _ map[string]any) (string, error) { return "", nil },
		},
	}})

	ctx := context.Background()
	bartender := &services.Caller{Roles: []string{"bartender"}}
	manager := &services.Caller{Roles: []string{"manager"}}

	rBart := NewRegistry(ctx, bartender, true)
	defsBart := rBart.DefinitionsFor(bartender)
	if !hasToolByName(defsBart, "lookup") {
		t.Error("bartender registry missing 'lookup'")
	}
	if hasToolByName(defsBart, "reports") {
		t.Error("bartender should not see 'reports'")
	}

	rMgr := NewRegistry(ctx, manager, true)
	defsMgr := rMgr.DefinitionsFor(manager)
	if hasToolByName(defsMgr, "lookup") {
		t.Error("manager should not see 'lookup'")
	}
	if !hasToolByName(defsMgr, "reports") {
		t.Error("manager registry missing 'reports'")
	}
}

func TestNewRegistry_ExposedToolExecutes(t *testing.T) {
	// End-to-end: a bartender invokes an exposed tool and the runner's
	// Invoke closure fires with the unmarshalled args.
	var captured map[string]any
	withRunner(t, &stubRunner{defs: []ExposedToolDef{
		{
			ToolName:       "lookup",
			Description:    "test",
			ArgsSchema:     map[string]any{"type": "object"},
			VisibleToRoles: []string{"bartender"},
			Invoke: func(_ context.Context, _ *ExecContext, args map[string]any) (string, error) {
				captured = args
				return `{"ok":true}`, nil
			},
		},
	}})

	ctx := context.Background()
	caller := &services.Caller{Roles: []string{"bartender"}}
	r := NewRegistry(ctx, caller, true)

	input := json.RawMessage(`{"name":"jane"}`)
	out, err := r.Execute(&ExecContext{Ctx: ctx}, "lookup", input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != `{"ok":true}` {
		t.Errorf("out = %q", out)
	}
	if captured["name"] != "jane" {
		t.Errorf("captured = %v, want name=jane", captured)
	}
}

// hasToolByName searches a tool list for a given name. Tiny helper so
// the assertion call-sites read at a glance.
func hasToolByName(list []anthropic.Tool, name string) bool {
	for _, t := range list {
		if t.Name == name {
			return true
		}
	}
	return false
}
