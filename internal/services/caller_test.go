package services

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// An agent never exceeds its owner: it IS the owner's caller, marked and
// labelled. Everything that reads UserID, Roles or IsAdmin sees the owner.
func TestNewAgentCallerKeepsOwnerAuthority(t *testing.T) {
	owner := &Caller{
		Kind: CallerUser, TenantID: uuid.New(), UserID: uuid.New(), Identity: "U1",
		Roles: []string{"admin", "member"}, RoleIDs: []uuid.UUID{uuid.New()}, IsAdmin: true, Timezone: "America/Denver",
	}
	agent := NewAgentCaller(owner, "Morning briefing job")
	if agent.Kind != CallerAgent || agent.Label != "Morning briefing job" || agent.IsUser() {
		t.Fatalf("agent = %+v", agent)
	}
	if agent.UserID != owner.UserID || agent.TenantID != owner.TenantID || agent.Identity != owner.Identity ||
		!agent.IsAdmin || agent.Timezone != owner.Timezone || strings.Join(agent.Roles, ",") != "admin,member" {
		t.Fatalf("agent does not carry the owner: %+v", agent)
	}
	agent.Roles[0] = "changed"
	if owner.Roles[0] != "admin" {
		t.Fatal("agent shares the owner's role slice")
	}
}

func TestAgentLabel(t *testing.T) {
	long := strings.Repeat("x", 70)
	cases := map[string]string{
		"Post the morning briefing to #ops":              "Post the morning briefing to #ops job",
		"  Sync Square shifts\nThen tell me what moved.": "Sync Square shifts job",
		"":     "Scheduled job",
		long:   strings.Repeat("x", 60) + "… job",
		"\n\n": "Scheduled job",
	}
	for in, want := range cases {
		if got := AgentLabel(in); got != want {
			t.Errorf("AgentLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
