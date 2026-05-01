package models

import (
	"encoding/json"
	"testing"
)

// TestPolicy_AllowedToolsDistinguishesNilAndEmpty is the critical
// semantics test: nil means "no restriction"; empty slice means "allow
// nothing except infrastructure". JSON must round-trip without
// conflating the two.
func TestPolicy_AllowedToolsDistinguishesNilAndEmpty(t *testing.T) {
	// Nil pointer → no restriction.
	var pNil Policy
	if !pNil.IsAllowed("anything") {
		t.Error("nil AllowedTools should allow everything")
	}

	// Empty slice → infrastructure only.
	empty := []string{}
	pEmpty := Policy{AllowedTools: &empty}
	if pEmpty.IsAllowed("post_to_channel") {
		t.Error("empty AllowedTools should block non-infra tools")
	}
	if !pEmpty.IsAllowed("load_skill") {
		t.Error("empty AllowedTools should still permit infrastructure tools")
	}

	// Non-empty → listed tools plus infra.
	list := []string{"list_todos"}
	pListed := Policy{AllowedTools: &list}
	if !pListed.IsAllowed("list_todos") {
		t.Error("listed tool should be allowed")
	}
	if !pListed.IsAllowed("load_skill") {
		t.Error("infrastructure tool should always be allowed")
	}
	if pListed.IsAllowed("post_to_channel") {
		t.Error("unlisted non-infra tool should be blocked")
	}
}

// TestPolicy_RoundTripsAllowedToolsStates confirms the three AllowedTools
// states survive a JSON round-trip. The pointer discriminates absent
// (nil) from empty ([]) — a plain slice field would conflate them.
func TestPolicy_RoundTripsAllowedToolsStates(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantNil bool
		wantLen int
	}{
		{"absent", `{}`, true, 0},
		{"explicit null", `{"allowed_tools":null}`, true, 0},
		{"empty", `{"allowed_tools":[]}`, false, 0},
		{"non-empty", `{"allowed_tools":["a","b"]}`, false, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p Policy
			if err := json.Unmarshal([]byte(c.input), &p); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if (p.AllowedTools == nil) != c.wantNil {
				t.Errorf("AllowedTools nil? got=%v, want=%v", p.AllowedTools == nil, c.wantNil)
			}
			if !c.wantNil && len(*p.AllowedTools) != c.wantLen {
				t.Errorf("len = %d, want %d", len(*p.AllowedTools), c.wantLen)
			}
		})
	}
}

// TestMergePinnedArgs_DoesNotMutateInput — the pinning merge must
// produce a new json.RawMessage. A shared input slice mutated in place
// would race across goroutines and corrupt the caller's copy.
func TestMergePinnedArgs_DoesNotMutateInput(t *testing.T) {
	input := json.RawMessage(`{"channel":"C_AGENT","text":"hi"}`)
	originalBytes := append(json.RawMessage(nil), input...)

	pinned := map[string]any{"channel": "C_PINNED"}
	merged, changed, err := MergePinnedArgs(input, pinned)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !changed {
		t.Error("expected changed=true when pinning overrides")
	}
	if string(input) != string(originalBytes) {
		t.Errorf("input was mutated: %s != %s", string(input), string(originalBytes))
	}

	var out map[string]any
	if err := json.Unmarshal(merged, &out); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	if out["channel"] != "C_PINNED" {
		t.Errorf("channel = %v, want C_PINNED", out["channel"])
	}
	if out["text"] != "hi" {
		t.Errorf("text = %v, want hi", out["text"])
	}
}

// TestMergePinnedArgs_NoChangeWhenSameValue ensures the changed flag
// reports accurately — avoids emitting a policy_enforced event when the
// agent already happened to set the pinned value.
func TestMergePinnedArgs_NoChangeWhenSameValue(t *testing.T) {
	input := json.RawMessage(`{"channel":"C_PINNED","text":"hi"}`)
	pinned := map[string]any{"channel": "C_PINNED"}
	_, changed, err := MergePinnedArgs(input, pinned)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if changed {
		t.Error("expected changed=false when agent already matched pinned value")
	}
}

// TestConfigPolicyRoundTrip — SetConfigPolicy preserves other keys on
// the Config JSON (builder_script's script_id, etc.) and can be parsed
// back out via ParseConfigPolicy.
func TestConfigPolicyRoundTrip(t *testing.T) {
	existing := []byte(`{"script_id":"abc","fn_name":"main"}`)
	forceGate := []string{"post_to_channel"}
	p := &Policy{ForceGate: forceGate}

	out, err := SetConfigPolicy(existing, p)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	// Other keys preserved.
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(out, &wrapper); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(wrapper["script_id"]) != `"abc"` {
		t.Errorf("script_id lost: %s", wrapper["script_id"])
	}

	// Policy parses back.
	parsed, err := ParseConfigPolicy(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed == nil || len(parsed.ForceGate) != 1 || parsed.ForceGate[0] != "post_to_channel" {
		t.Errorf("policy did not round-trip: %+v", parsed)
	}

	// Setting nil deletes the policy key but keeps other keys.
	out2, err := SetConfigPolicy(out, nil)
	if err != nil {
		t.Fatalf("set nil: %v", err)
	}
	again, err := ParseConfigPolicy(out2)
	if err != nil {
		t.Fatalf("parse after delete: %v", err)
	}
	if again != nil {
		t.Errorf("expected nil policy after delete, got %+v", again)
	}
}

// TestPolicy_ForcesGate covers the simple set-membership helper.
func TestPolicy_ForcesGate(t *testing.T) {
	var nilP *Policy
	if nilP.ForcesGate("anything") {
		t.Error("nil policy should not force-gate anything")
	}
	p := &Policy{ForceGate: []string{"post_to_channel", "dm_user"}}
	if !p.ForcesGate("post_to_channel") {
		t.Error("listed tool should be force-gated")
	}
	if p.ForcesGate("list_todos") {
		t.Error("unlisted tool should not be force-gated")
	}
}
