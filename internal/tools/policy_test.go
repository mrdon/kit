package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/tools/approval"
)

// stubHandlerDef returns a minimal tool Def whose handler records its
// input and returns "ran". For tests that need to inspect what the
// registry forwarded to the handler.
func stubHandlerDef(name string, called *int, gotInput *json.RawMessage, denyCallerGate bool) Def {
	return Def{
		Name:           name,
		Schema:         map[string]any{"type": "object", "properties": map[string]any{"channel": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}},
		DenyCallerGate: denyCallerGate,
		Handler: func(_ *ExecContext, input json.RawMessage) (string, error) {
			*called++
			if gotInput != nil {
				*gotInput = append(json.RawMessage(nil), input...)
			}
			return "ran", nil
		},
	}
}

// TestPolicy_AllowListBlocks ensures a tool outside allowed_tools
// errors out before dispatching the handler.
func TestPolicy_AllowListBlocks(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("post_to_channel", &called, nil, false))
	r.Register(stubHandlerDef("list_todos", &called, nil, false))

	allowed := []string{"list_todos"}
	ec := &ExecContext{Ctx: context.Background(), JobPolicy: &models.Policy{AllowedTools: &allowed}}

	_, err := r.ExecuteWithResult(ec, "post_to_channel", json.RawMessage(`{"channel":"C1","text":"hi"}`))
	if err == nil {
		t.Fatal("expected error for disallowed tool, got nil")
	}
	if called != 0 {
		t.Errorf("handler should not have run; called=%d", called)
	}
}

// TestPolicy_AllowListPermits confirms a tool listed in allowed_tools
// dispatches normally.
func TestPolicy_AllowListPermits(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("list_todos", &called, nil, false))

	allowed := []string{"list_todos"}
	ec := &ExecContext{Ctx: context.Background(), JobPolicy: &models.Policy{AllowedTools: &allowed}}

	res, err := r.ExecuteWithResult(ec, "list_todos", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output != "ran" || called != 1 {
		t.Errorf("unexpected: out=%q called=%d", res.Output, called)
	}
}

// TestPolicy_AllowListEmptyBlocksNonInfra ensures allowed_tools == [] is
// a real restriction: everything except the infrastructure set is blocked.
// Regression guard against treating nil and empty slice identically.
func TestPolicy_AllowListEmptyBlocksNonInfra(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("post_to_channel", &called, nil, false))
	r.Register(stubHandlerDef("load_skill", &called, nil, false))

	empty := []string{}
	ec := &ExecContext{Ctx: context.Background(), JobPolicy: &models.Policy{AllowedTools: &empty}}

	// Non-infra tool is blocked.
	_, err := r.ExecuteWithResult(ec, "post_to_channel", json.RawMessage(`{}`))
	if err == nil {
		t.Error("empty allow-list should block non-infra tool")
	}
	// Infra tool still runs.
	res, err := r.ExecuteWithResult(ec, "load_skill", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("load_skill: %v", err)
	}
	if res.Output != "ran" {
		t.Errorf("load_skill should still dispatch; got %q", res.Output)
	}
}

// TestPolicy_ForceGateCreatesCard confirms force_gate pushes a tool
// through the approval path even when the agent didn't set
// require_approval and the tool isn't DefaultPolicy=PolicyGate.
func TestPolicy_ForceGateCreatesCard(t *testing.T) {
	stub := withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("post_to_channel", &called, nil, false))

	ec := &ExecContext{Ctx: context.Background(), JobPolicy: &models.Policy{ForceGate: []string{"post_to_channel"}}}

	res, err := r.ExecuteWithResult(ec, "post_to_channel", json.RawMessage(`{"channel":"C1","text":"hi"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Halted {
		t.Error("expected Halted=true from force_gate")
	}
	if called != 0 {
		t.Error("handler should not have run before approval")
	}
	if len(stub.calls) != 1 {
		t.Fatalf("expected one gate card, got %d", len(stub.calls))
	}
}

// TestPolicy_ForceGateOverridesDenyCallerGate verifies that a
// job-level force_gate wins over DenyCallerGate (which only suppresses
// the agent's own opt-in).
func TestPolicy_ForceGateOverridesDenyCallerGate(t *testing.T) {
	stub := withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("locked", &called, nil, true))

	ec := &ExecContext{Ctx: context.Background(), JobPolicy: &models.Policy{ForceGate: []string{"locked"}}}

	res, err := r.ExecuteWithResult(ec, "locked", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Halted {
		t.Error("force_gate should trigger card even on DenyCallerGate tool")
	}
	if len(stub.calls) != 1 {
		t.Errorf("expected one gate card, got %d", len(stub.calls))
	}
}

// TestPolicy_PinnedArgsOverrideAgentInput asserts that the handler
// receives the pinned value, not the agent's original input.
func TestPolicy_PinnedArgsOverrideAgentInput(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	var gotInput json.RawMessage
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("post_to_channel", &called, &gotInput, false))

	ec := &ExecContext{
		Ctx: context.Background(),
		JobPolicy: &models.Policy{
			PinnedArgs: map[string]map[string]any{
				"post_to_channel": {"channel": "C09PINNED"},
			},
		},
	}

	_, err := r.ExecuteWithResult(ec, "post_to_channel", json.RawMessage(`{"channel":"C_AGENT","text":"hi"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if called != 1 {
		t.Fatalf("handler should have run; called=%d", called)
	}
	var parsed map[string]any
	if err := json.Unmarshal(gotInput, &parsed); err != nil {
		t.Fatalf("parse handler input: %v", err)
	}
	if parsed["channel"] != "C09PINNED" {
		t.Errorf("expected pinned channel, got %v", parsed["channel"])
	}
	if parsed["text"] != "hi" {
		t.Errorf("text should pass through, got %v", parsed["text"])
	}
}

// TestPolicy_PinnedArgsVisibleOnCard ensures the pinned values are the
// ones stored on the gate card — the user's approval reflects the true
// (pinned) arguments.
func TestPolicy_PinnedArgsVisibleOnCard(t *testing.T) {
	stub := withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("post_to_channel", &called, nil, false))

	ec := &ExecContext{
		Ctx: context.Background(),
		JobPolicy: &models.Policy{
			ForceGate: []string{"post_to_channel"},
			PinnedArgs: map[string]map[string]any{
				"post_to_channel": {"channel": "C09PINNED"},
			},
		},
	}

	_, err := r.ExecuteWithResult(ec, "post_to_channel", json.RawMessage(`{"channel":"C_AGENT","text":"hi"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("expected one gate card, got %d", len(stub.calls))
	}
	var cardArgs map[string]any
	if err := json.Unmarshal(stub.calls[0].args, &cardArgs); err != nil {
		t.Fatalf("card args: %v", err)
	}
	if cardArgs["channel"] != "C09PINNED" {
		t.Errorf("card should carry pinned channel, got %v", cardArgs["channel"])
	}
}

// TestPolicy_ApprovalTokenRunsPinnedArgs — after approval, the handler
// receives the pinned arguments (they were frozen into the card's stored
// arguments at creation; on re-execute with an approval token, the
// registry runs the pinned shape).
func TestPolicy_ApprovalTokenRunsPinnedArgs(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	var gotInput json.RawMessage
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("post_to_channel", &called, &gotInput, false))

	ctx := approval.WithToken(context.Background(), approval.Mint(uuid.New(), uuid.New()))
	ec := &ExecContext{
		Ctx: ctx,
		JobPolicy: &models.Policy{
			ForceGate: []string{"post_to_channel"},
			PinnedArgs: map[string]map[string]any{
				"post_to_channel": {"channel": "C09PINNED"},
			},
		},
	}

	_, err := r.ExecuteWithResult(ec, "post_to_channel", json.RawMessage(`{"channel":"C_AGENT","text":"hi"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if called != 1 {
		t.Fatalf("handler should have run with approval token; called=%d", called)
	}
	var parsed map[string]any
	_ = json.Unmarshal(gotInput, &parsed)
	if parsed["channel"] != "C09PINNED" {
		t.Errorf("expected pinned channel on approved run, got %v", parsed["channel"])
	}
}

// TestPolicy_NilPolicyUnchanged covers the interactive path: no policy
// = today's behaviour.
func TestPolicy_NilPolicyUnchanged(t *testing.T) {
	withGateCreator(t, nil)
	called := 0
	r := &Registry{handlers: map[string]HandlerFunc{}}
	r.Register(stubHandlerDef("post_to_channel", &called, nil, false))

	ec := &ExecContext{Ctx: context.Background()} // JobPolicy is nil

	res, err := r.ExecuteWithResult(ec, "post_to_channel", json.RawMessage(`{"channel":"C1","text":"hi"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Halted || called != 1 {
		t.Errorf("nil policy should dispatch directly; halted=%v called=%d", res.Halted, called)
	}
}
