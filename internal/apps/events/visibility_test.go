package events

import "testing"

// The full cross product, written out rather than derived from a formula --
// a formula would just restate the implementation and pass even if both were
// wrong together. Twelve rows is cheap for the app's one security boundary.
func TestIsPubliclyVisibleTruthTable(t *testing.T) {
	cases := []struct {
		status Status
		vis    Visibility
		want   bool
	}{
		{StatusDraft, VisibilityPrivate, false},
		{StatusDraft, VisibilityPublic, false}, // not ready, regardless of intent
		{StatusPublished, VisibilityPrivate, false},
		{StatusPublished, VisibilityPublic, true}, // the only true row
		{StatusCancelled, VisibilityPrivate, false},
		{StatusCancelled, VisibilityPublic, false}, // called off, must come down

		// Values that should never reach the database still must not leak.
		{Status(""), VisibilityPublic, false},
		{StatusPublished, Visibility(""), false},
		{Status("PUBLISHED"), VisibilityPublic, false}, // case-sensitive by design
		{StatusPublished, Visibility("Public"), false},
		{Status("anything"), Visibility("anything"), false},
		{Status(""), Visibility(""), false},
	}

	for _, c := range cases {
		e := &Event{Status: c.status, Visibility: c.vis}
		if got := e.IsPubliclyVisible(); got != c.want {
			t.Errorf("status=%q visibility=%q: IsPubliclyVisible() = %v, want %v",
				c.status, c.vis, got, c.want)
		}
	}
}

// Exactly one combination may ever be public. If a future status is added
// without revisiting the predicate, this catches it.
func TestOnlyPublishedPublicIsVisible(t *testing.T) {
	statuses := []Status{StatusDraft, StatusPublished, StatusCancelled, Status("future_state")}
	visibilities := []Visibility{VisibilityPublic, VisibilityPrivate, Visibility("future_vis")}

	visible := 0
	for _, s := range statuses {
		for _, v := range visibilities {
			if (&Event{Status: s, Visibility: v}).IsPubliclyVisible() {
				visible++
				if s != StatusPublished || v != VisibilityPublic {
					t.Errorf("unexpected public combination: status=%q visibility=%q", s, v)
				}
			}
		}
	}
	if visible != 1 {
		t.Fatalf("%d combinations are publicly visible, want exactly 1", visible)
	}
}

func TestIsPubliclyVisibleNilSafe(t *testing.T) {
	var e *Event
	if e.IsPubliclyVisible() {
		t.Fatal("a nil event must not be publicly visible")
	}
}

// Private is the default so an event created with no explicit visibility --
// by a tool call that omitted the field, say -- cannot be public by accident.
func TestZeroValueEventIsNotPublic(t *testing.T) {
	if (&Event{}).IsPubliclyVisible() {
		t.Fatal("zero-value event is publicly visible; default-deny is broken")
	}
}

func TestEnumValidators(t *testing.T) {
	if !ValidStatus(StatusDraft) || !ValidStatus(StatusPublished) || !ValidStatus(StatusCancelled) {
		t.Error("a valid status was rejected")
	}
	if ValidStatus(Status("archived")) || ValidStatus(Status("")) {
		t.Error("an invalid status was accepted")
	}
	if !ValidVisibility(VisibilityPublic) || ValidVisibility(Visibility("unlisted")) {
		t.Error("visibility validation wrong")
	}
	if !ValidVenue(VenueOffsite) || ValidVenue(Venue("virtual")) {
		t.Error("venue validation wrong")
	}
	// 'buyout' is deliberately not storable yet.
	if !ValidSpaceImpact(SpaceImpactPartial) || ValidSpaceImpact(SpaceImpact("buyout")) {
		t.Error("space impact validation wrong")
	}
}
