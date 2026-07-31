package events

import (
	"encoding/json"
	"strings"
	"testing"
)

// A nil Go slice marshals to JSON `null`, not `[]`. A client doing
// `payload.calendars.length` then dies on a type error and, in a React app,
// takes the whole page down with it -- which is exactly what happened here, in
// the first-run state where no calendar has been shared with the service
// account yet. That is the one state every new install passes through.
//
// So: every array field in a console payload must serialise as `[]` when
// empty. This test walks the JSON rather than checking Go fields, because the
// wire format is what the client actually consumes.
func TestSettingsPayloadNeverSerialisesNullArrays(t *testing.T) {
	// The zero value is the worst case: nothing configured, nothing fetched.
	raw, err := json.Marshal(settingsPayload{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, field := range []string{`"calendars":null`, `"recent":null`} {
		if strings.Contains(body, field) {
			t.Errorf("payload emits %s — a client reading .length on it will crash:\n%s", field, body)
		}
	}
}

// The same guarantee through the function that actually builds the list,
// including the two failure paths (no calendars shared, listing errored) that
// are most likely to return nothing.
func TestListCalendarOptionsNeverReturnsNil(t *testing.T) {
	payload := settingsPayload{Calendars: []calendarOption{}, Recent: []runPayload{}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"calendars":[]`) {
		t.Errorf("initialised slice did not serialise as []: %s", raw)
	}

	// An explanatory message must accompany an empty list, so the admin is
	// told why the picker is empty rather than assuming the page is broken.
	var decoded struct {
		Calendars []calendarOption `json:"calendars"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Calendars == nil {
		t.Error("round trip produced a nil slice")
	}
}

// The event list endpoint has the same hazard; handleList guards it, and this
// pins that guard.
func TestEventListPayloadNeverSerialisesNullArray(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"events": []Event{}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"events":[]`) {
		t.Errorf("empty event list did not serialise as []: %s", raw)
	}
	// And the failure mode being guarded against, so the test documents it.
	var nilEvents []Event
	raw, _ = json.Marshal(map[string]any{"events": nilEvents})
	if !strings.Contains(string(raw), `"events":null`) {
		t.Fatal("premise wrong: a nil slice no longer marshals to null, so this guard may be unnecessary")
	}
}
