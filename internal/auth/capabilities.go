package auth

import "github.com/mrdon/kit/internal/services"

// Capabilities are what a non-user actor (a paired device) may do. There
// are a handful, so they are constants here rather than an app registry.
//
// Each one declares its human default: the least a person needs to pass
// the same check. That is how one route wrapper serves both kinds of
// caller without changing anything for people: a user passes when their
// role meets the human default, a device passes when it holds the
// capability. Where one capability spans routes with different human
// checks (trivia hosting is member work; its feedback-channel setting is
// admin work), the route overrides the default rather than growing a
// second capability.
const (
	CapMenuHappyHour     = "menu.happy_hour"
	CapMenuGlutenReduced = "menu.gluten_reduced"
	CapMenuPrint         = "menu.print"
	CapKioskRepoint      = "kiosk.repoint"
	CapTriviaHost        = "trivia.host"
)

// HumanCheck is the role test a person must pass for a capability.
type HumanCheck int

const (
	// HumanMember: any signed-in member of the workspace.
	HumanMember HumanCheck = iota
	// HumanAdmin: an admin only.
	HumanAdmin
)

// Capability describes one grant.
type Capability struct {
	Name string
	// Label is what the pairing and devices pages show.
	Label string
	// Human is the default role test for people on routes with this
	// capability.
	Human HumanCheck
}

// Capabilities lists every capability, in the order the UI shows them.
var Capabilities = []Capability{
	{Name: CapMenuHappyHour, Label: "Start and end happy hour", Human: HumanAdmin},
	{Name: CapMenuPrint, Label: "Print the menu", Human: HumanMember},
	{Name: CapMenuGlutenReduced, Label: "Tick gluten reduced beers", Human: HumanAdmin},
	{Name: CapKioskRepoint, Label: "Repoint wall screens", Human: HumanMember},
	{Name: CapTriviaHost, Label: "Host trivia", Human: HumanMember},
}

// LookupCapability finds a capability by name.
func LookupCapability(name string) (Capability, bool) {
	for _, c := range Capabilities {
		if c.Name == name {
			return c, true
		}
	}
	return Capability{}, false
}

// IsCapability reports whether name is a known capability. Pairing and
// device edits refuse anything else, so a typo can't become a silent
// grant of nothing.
func IsCapability(name string) bool {
	_, ok := LookupCapability(name)
	return ok
}

// Allows reports whether caller may use a route guarded by this
// capability, with human as the role test for people. A person never
// passes on capabilities (they hold none); a device never passes on
// roles (it holds none). Any other caller kind is refused.
func (c Capability) Allows(caller *services.Caller, human HumanCheck) bool {
	if caller == nil {
		return false
	}
	switch {
	case caller.IsUser():
		return human == HumanMember || caller.IsAdmin
	case caller.Kind == services.CallerDevice:
		return caller.HasCapability(c.Name)
	default:
		return false
	}
}
