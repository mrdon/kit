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

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools/approval"
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

// stubGateCreator captures the tool/args it was asked to gate and
// returns a stable card id. Lets the caller-gate tests observe the
// gate path without spinning up the real cards app.
type stubGateCreator struct {
	calls []stubGateCall
}

type stubGateCall struct {
	toolName string
	args     json.RawMessage
	preview  GateCardPreview
}

func (s *stubGateCreator) CreateGateCard(_ context.Context, _ *ExecContext, toolName string, args json.RawMessage, preview GateCardPreview) (uuid.UUID, string, error) {
	s.calls = append(s.calls, stubGateCall{toolName: toolName, args: args, preview: preview})
	return uuid.New(), "", nil
}

func withGateCreator(t *testing.T, g GateCreator) *stubGateCreator {
	t.Helper()
	prev := currentGateCreator
	stub, ok := g.(*stubGateCreator)
	if !ok {
		stub = &stubGateCreator{}
		g = stub
	}
	SetGateCreator(g)
	t.Cleanup(func() { SetGateCreator(prev) })
	return stub
}

func TestRegister_AutoInjectsRequireApprovalSchema(t *testing.T) {
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(Def{
		Name: "demo",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
		},
		Handler: func(_ *ExecContext, _ json.RawMessage) (string, error) { return "", nil },
	})
	def, _ := r.defByName("demo")
	props, _ := def.Schema["properties"].(map[string]any)
	if _, ok := props[requireApprovalField]; !ok {
		t.Fatalf("expected require_approval in properties, got %v", props)
	}
}

func TestRegister_DenyCallerGateSkipsInjection(t *testing.T) {
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(Def{
		Name: "locked",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
		DenyCallerGate: true,
		Handler:        func(_ *ExecContext, _ json.RawMessage) (string, error) { return "", nil },
	})
	def, _ := r.defByName("locked")
	props, _ := def.Schema["properties"].(map[string]any)
	if _, ok := props[requireApprovalField]; ok {
		t.Fatal("require_approval should not be injected on a DenyCallerGate tool")
	}
}

func TestRegister_DoesNotMutateSharedSchema(t *testing.T) {
	// Two registries built from the same Def literal must not share
	// a mutated properties map.
	shared := map[string]any{
		"type":       "object",
		"properties": map[string]any{"x": map[string]any{"type": "string"}},
	}
	r1 := &Registry{handlers: map[string]HandlerFunc{}}
	r1.Register(Def{Name: "t", Schema: shared, Handler: func(_ *ExecContext, _ json.RawMessage) (string, error) { return "", nil }})
	props, _ := shared["properties"].(map[string]any)
	if _, leaked := props[requireApprovalField]; leaked {
		t.Fatal("Register mutated the caller's shared schema map")
	}
}

func TestReadRequireApproval(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		flag  bool
		clean string
	}{
		{"absent", `{"text":"hi"}`, false, `{"text":"hi"}`},
		{"false", `{"text":"hi","require_approval":false}`, false, `{"text":"hi"}`},
		{"true", `{"text":"hi","require_approval":true}`, true, `{"text":"hi"}`},
		{"empty", ``, false, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			flag, cleaned := services.ReadRequireApproval(json.RawMessage(c.in))
			if flag != c.flag {
				t.Errorf("flag = %v, want %v", flag, c.flag)
			}
			// Compare by re-parsing so key order doesn't matter.
			var got, want map[string]any
			_ = json.Unmarshal(cleaned, &got)
			_ = json.Unmarshal([]byte(c.clean), &want)
			if len(got) != len(want) {
				t.Errorf("cleaned keys = %v, want %v", got, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("cleaned[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestExecute_CallerGateTriggersCard(t *testing.T) {
	stub := withGateCreator(t, nil)
	handlerRan := false
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(Def{
		Name: "demo",
		Schema: map[string]any{
			"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
		Handler: func(_ *ExecContext, _ json.RawMessage) (string, error) {
			handlerRan = true
			return "ran", nil
		},
	})

	res, err := r.ExecuteWithResult(&ExecContext{Ctx: context.Background()}, "demo", json.RawMessage(`{"text":"hi","require_approval":true}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Halted {
		t.Error("expected Halted=true when require_approval=true without token")
	}
	if handlerRan {
		t.Error("handler should not have run on a caller-gated call")
	}
	if len(stub.calls) != 1 {
		t.Fatalf("gate creator calls = %d, want 1", len(stub.calls))
	}
	// The require_approval field should be stripped from the args
	// forwarded to the card so the handler (and card UI) never see it.
	var forwarded map[string]any
	if err := json.Unmarshal(stub.calls[0].args, &forwarded); err != nil {
		t.Fatalf("card args: %v", err)
	}
	if _, leaked := forwarded[requireApprovalField]; leaked {
		t.Error("require_approval should be stripped before gate creation")
	}
}

func TestExecute_CallerGateFalsePassesThrough(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(Def{
		Name: "demo",
		Schema: map[string]any{
			"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
		Handler: func(_ *ExecContext, input json.RawMessage) (string, error) {
			called++
			// Handler must not see require_approval.
			var args map[string]any
			_ = json.Unmarshal(input, &args)
			if _, leaked := args[requireApprovalField]; leaked {
				t.Error("handler received require_approval in input")
			}
			return "ran", nil
		},
	})
	res, err := r.ExecuteWithResult(&ExecContext{Ctx: context.Background()}, "demo", json.RawMessage(`{"text":"hi","require_approval":false}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Halted || res.Output != "ran" || called != 1 {
		t.Errorf("unexpected result: halted=%v out=%q called=%d", res.Halted, res.Output, called)
	}
}

func TestExecute_ApprovalTokenBypassesCallerGate(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(Def{
		Name:    "demo",
		Schema:  map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		Handler: func(_ *ExecContext, _ json.RawMessage) (string, error) { called++; return "ran", nil },
	})
	ctx := approval.WithToken(context.Background(), approval.Mint(uuid.New(), uuid.New()))
	res, err := r.ExecuteWithResult(&ExecContext{Ctx: ctx}, "demo", json.RawMessage(`{"text":"hi","require_approval":true}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Halted || called != 1 {
		t.Errorf("expected direct dispatch with approval token; halted=%v called=%d", res.Halted, called)
	}
}

func TestExecute_DenyCallerGateIgnoresFlag(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(Def{
		Name:           "locked",
		Schema:         map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		DenyCallerGate: true,
		Handler: func(_ *ExecContext, input json.RawMessage) (string, error) {
			called++
			// DenyCallerGate tools must still see require_approval in
			// the payload if the agent somehow sends it — no stripping.
			var args map[string]any
			_ = json.Unmarshal(input, &args)
			if _, present := args[requireApprovalField]; !present {
				t.Error("DenyCallerGate tool should receive require_approval unmodified")
			}
			return "ran", nil
		},
	})
	res, err := r.ExecuteWithResult(&ExecContext{Ctx: context.Background()}, "locked", json.RawMessage(`{"text":"hi","require_approval":true}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Halted || called != 1 {
		t.Errorf("DenyCallerGate tool should run directly; halted=%v called=%d", res.Halted, called)
	}
}

func TestDefaultGateCardPreview(t *testing.T) {
	// Backstop check that defaults humanise tool name.
	p := defaultGateCardPreview("post_to_channel")
	if p.Title == "" || p.ApproveLabel == "" || p.SkipLabel == "" {
		t.Errorf("default preview fields should be populated: %+v", p)
	}
	if want := "Run post to channel?"; p.Title != want {
		t.Errorf("title = %q, want %q", p.Title, want)
	}
}
