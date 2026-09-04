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
	// synced_at is the one field allowed to be null, and it means something:
	// nobody has synced yet. It is read as a value, never mapped over.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	for name, val := range fields {
		if name != "synced_at" && string(val) == "null" {
			t.Errorf("%s came back null: %s", name, raw)
		}
	}
	if _, ok := fields["synced_at"]; !ok {
		t.Error("synced_at must be present even when null — the page branches on it")
	}

	var back struct {
		Config struct {
			Colors map[string]string `json:"colors"`
			Notes  map[string]string `json:"notes"`
			Blurbs map[string]string `json:"blurbs"`
			Extras []Beer            `json:"extras"`
		} `json:"config"`
		Sections []string    `json:"sections"`
		Palette  []string    `json:"palette"`
		Beers    []printBeer `json:"beers"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.Sections == nil || back.Palette == nil || back.Beers == nil ||
		back.Config.Extras == nil || back.Config.Colors == nil ||
		back.Config.Notes == nil || back.Config.Blurbs == nil {
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

// The sync response must carry its report. printConfigPayload has a custom
// MarshalJSON, and embedding it here would promote that method and quietly
// emit the payload alone -- a bug with no symptom but a Sync button that never
// says what it did.
func TestSyncResponseKeepsItsReport(t *testing.T) {
	raw, err := json.Marshal(syncResponse{
		State:   printConfigPayload{},
		Report:  SyncReport{Rows: 16, Described: 14, Missing: []string{"Mars Water"}},
		Summary: "Synced 16 beers from Untappd.",
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	for _, key := range []string{"state", "report", "summary"} {
		if _, ok := got[key]; !ok {
			t.Errorf("response lost %q: %s", key, raw)
		}
	}
}
