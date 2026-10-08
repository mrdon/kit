package auth

import (
	"testing"

	"github.com/mrdon/kit/internal/services"
)

// One rule serves both kinds of caller: people are judged by role against
// the human default, devices by whether they hold the capability.
func TestCapabilityAllows(t *testing.T) {
	admin := &services.Caller{Kind: services.CallerUser, IsAdmin: true}
	member := &services.Caller{Kind: services.CallerUser}
	legacy := &services.Caller{} // zero Kind: a person
	withCap := &services.Caller{Kind: services.CallerDevice, Capabilities: []string{CapTriviaHost}}
	withoutCap := &services.Caller{Kind: services.CallerDevice, Capabilities: []string{CapMenuPrint}}
	widget := &services.Caller{Kind: services.CallerWidget}
	host, _ := LookupCapability(CapTriviaHost)

	cases := []struct {
		name   string
		caller *services.Caller
		human  HumanCheck
		want   bool
	}{
		{"admin, member route", admin, HumanMember, true},
		{"admin, admin route", admin, HumanAdmin, true},
		{"member, member route", member, HumanMember, true},
		{"member, admin route", member, HumanAdmin, false},
		{"zero-kind caller is a person", legacy, HumanMember, true},
		{"device with cap, member route", withCap, HumanMember, true},
		{"device with cap, admin route", withCap, HumanAdmin, true},
		{"device without cap", withoutCap, HumanMember, false},
		{"widget", widget, HumanMember, false},
		{"nil", nil, HumanMember, false},
	}
	for _, c := range cases {
		if got := host.Allows(c.caller, c.human); got != c.want {
			t.Errorf("%s: Allows = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCapabilityLookup(t *testing.T) {
	for _, c := range Capabilities {
		if !IsCapability(c.Name) {
			t.Errorf("%s not found by name", c.Name)
		}
	}
	if IsCapability("trivia.hots") {
		t.Error("a typo passed as a capability")
	}
}
