package voting

import "testing"

// Pure-Go state-machine tests — no DB, no LLM, no card service.

func TestAllResolved(t *testing.T) {
	tests := []struct {
		name string
		in   []Participant
		want bool
	}{
		{"empty list", []Participant{}, false},
		{"all pending", []Participant{{}, {}}, false},
		{"one pending", []Participant{{Verdict: VerdictApprove}, {}}, false},
		{"all resolved approve", []Participant{{Verdict: VerdictApprove}, {Verdict: VerdictApprove}}, true},
		{"mixed verdicts", []Participant{{Verdict: VerdictApprove}, {Verdict: VerdictObject}, {Verdict: VerdictAbstain}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allResolved(tt.in); got != tt.want {
				t.Errorf("allResolved = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildTally(t *testing.T) {
	parts := []Participant{
		{Verdict: VerdictApprove},
		{Verdict: VerdictApprove},
		{Verdict: VerdictObject},
		{Verdict: VerdictAbstain},
		{}, // no response
	}
	tally := buildTally(parts)
	if tally.Approve != 2 {
		t.Errorf("approve = %d, want 2", tally.Approve)
	}
	if tally.Object != 1 {
		t.Errorf("object = %d, want 1", tally.Object)
	}
	if tally.Abstain != 1 {
		t.Errorf("abstain = %d, want 1", tally.Abstain)
	}
	if tally.NoResponse != 1 {
		t.Errorf("no_response = %d, want 1", tally.NoResponse)
	}
}

func TestBuildHeadline(t *testing.T) {
	tests := []struct {
		name  string
		tally Tally
		want  string
	}{
		{"unanimous approve", Tally{Approve: 3}, "unanimous approve"},
		{"one objection", Tally{Approve: 2, Object: 1}, "1 objection"},
		{"two objections", Tally{Object: 2}, "2 objections"},
		{"only no-response", Tally{NoResponse: 1}, "1 no response"},
		{"two no-response", Tally{Approve: 1, NoResponse: 2}, "2 no response"},
		// Objections take precedence over non-responders in the headline.
		{"object beats no-response", Tally{Object: 1, NoResponse: 2}, "1 objection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildHeadline(tt.tally); got != tt.want {
				t.Errorf("buildHeadline = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVerdictFromAction(t *testing.T) {
	tests := []struct {
		action string
		want   VoteVerdict
		ok     bool
	}{
		{actionApprove, VerdictApprove, true},
		{actionObject, VerdictObject, true},
		{actionAbstain, VerdictAbstain, true},
		{"bogus", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, err := verdictFromAction(tt.action)
		if (err == nil) != tt.ok {
			t.Errorf("action=%q: err=%v, want ok=%v", tt.action, err, tt.ok)
			continue
		}
		if got != tt.want {
			t.Errorf("action=%q: got %q, want %q", tt.action, got, tt.want)
		}
	}
}
