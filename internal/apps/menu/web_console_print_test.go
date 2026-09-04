package menu

import (
	"encoding/json"
	"strings"
	"testing"
)

// A nil Go slice marshals to `null`, and a client doing `.map(...)` on it dies
// on a type error and takes the page down. The first-run state -- nothing
// configured -- is exactly where that would bite, so the wire shape guarantees
// empty collections instead.
func TestPrintConfigPayloadNeverSendsNull(t *testing.T) {
	raw, err := json.Marshal(printConfigPayload{})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if strings.Contains(string(raw), ":null") {
		t.Errorf("payload carries a null: %s", raw)
	}

	var back struct {
		Config struct {
			Colors map[string]string `json:"colors"`
			Notes  map[string]string `json:"notes"`
			Extras []Beer            `json:"extras"`
		} `json:"config"`
		Sections []string `json:"sections"`
		Palette  []string `json:"palette"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.Sections == nil || back.Palette == nil ||
		back.Config.Extras == nil || back.Config.Colors == nil || back.Config.Notes == nil {
		t.Errorf("an empty collection came back nil: %s", raw)
	}
}

// The console writes the same document the tool does. If the two ever disagree
// about a field name, a setting saved in one surface vanishes in the other.
func TestPrintConfigRoundTripsThroughTheToolShape(t *testing.T) {
	cfg := samplePrintConfig()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	// The tool decodes with DisallowUnknownFields, so anything the console can
	// produce must be a field the tool already knows.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var back PrintConfig
	if err := dec.Decode(&back); err != nil {
		t.Fatalf("the tool would reject what the console writes: %v", err)
	}
	if back.Brand != cfg.Brand || len(back.Extras) != len(cfg.Extras) ||
		len(back.Colors) != len(cfg.Colors) {
		t.Error("config did not survive the round trip intact")
	}
}
