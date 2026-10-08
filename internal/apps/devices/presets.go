package devices

import (
	"slices"

	"github.com/mrdon/kit/internal/auth"
)

// Preset is a starting capability set the approver picks from. The trivia
// driver is exact; the taproom admin comes pre-ticked and editable.
type Preset struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Capabilities []string `json:"capabilities"`
}

var presets = []Preset{
	{Key: "trivia", Label: "Trivia driver", Capabilities: []string{auth.CapTriviaHost}},
	{Key: "taproom", Label: "Taproom admin", Capabilities: []string{
		auth.CapMenuHappyHour, auth.CapMenuPrint, auth.CapMenuGlutenReduced, auth.CapEventsTopper, auth.CapKioskRepoint,
	}},
}

// capabilityInfo is what the console lists for the checkboxes.
type capabilityInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

func capabilityInfos() []capabilityInfo {
	out := make([]capabilityInfo, 0, len(auth.Capabilities))
	for _, c := range auth.Capabilities {
		out = append(out, capabilityInfo{Name: c.Name, Label: c.Label})
	}
	return out
}

// normaliseCapabilities validates, de-duplicates and orders a requested set.
// Returns ok=false naming nothing specific; the caller reports the input.
func normaliseCapabilities(in []string) ([]string, bool) {
	out := make([]string, 0, len(in))
	for _, c := range auth.Capabilities {
		if slices.Contains(in, c.Name) {
			out = append(out, c.Name)
		}
	}
	for _, name := range in {
		if !auth.IsCapability(name) {
			return nil, false
		}
	}
	return out, true
}
