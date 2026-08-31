package events

import (
	"strings"
	"testing"
	"time"
)

// The calendar body is the bartender's briefing, so the operational facts must
// come BEFORE the marketing copy -- "reserve the back room for 30" is useless
// buried under two paragraphs of blurb on a phone.
func TestBuildDescriptionPutsOperationalFactsFirst(t *testing.T) {
	n := 30
	e := &Event{
		Title:              "Volunteer party",
		Summary:            "A thank-you for the volunteers.",
		Description:        "Long public blurb that customers read.",
		PrepNotes:          "Three big tables plus a hightop.",
		Location:           "Taproom",
		SpaceImpact:        SpaceImpactPartial,
		ExpectedAttendance: &n,
	}
	got := buildDescription(e)

	where := strings.Index(got, "Where: Taproom")
	reserve := strings.Index(got, "Reserve: part of the room, for ~30 people.")
	blurb := strings.Index(got, "Long public blurb")
	notes := strings.Index(got, "Staff notes:")

	for name, idx := range map[string]int{"Where": where, "Reserve": reserve, "blurb": blurb, "staff notes": notes} {
		if idx < 0 {
			t.Fatalf("%s missing from description:\n%s", name, got)
		}
	}
	if where > blurb || reserve > blurb {
		t.Errorf("operational facts appear after the public copy:\n%s", got)
	}
	if notes < blurb {
		t.Errorf("staff notes should follow the public copy:\n%s", got)
	}
}

// Space and headcount answer one question together. Each combination must read
// as a sentence rather than emitting a stray fragment.
func TestBriefingSpaceAndHeadcountCombinations(t *testing.T) {
	n := 20
	cases := []struct {
		name  string
		event Event
		want  string
	}{
		{"partial with headcount", Event{SpaceImpact: SpaceImpactPartial, ExpectedAttendance: &n},
			"Reserve: part of the room, for ~20 people."},
		{"partial alone", Event{SpaceImpact: SpaceImpactPartial}, "Reserve: part of the room."},
		{"headcount alone", Event{SpaceImpact: SpaceImpactNone, ExpectedAttendance: &n},
			"Expect: ~20 people. Room stays open as usual."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(briefingLines(&tc.event), "\n")
			if !strings.Contains(got, tc.want) {
				t.Errorf("got %q, want it to contain %q", got, tc.want)
			}
		})
	}
	// Neither set: no line at all, rather than an empty "Reserve:".
	if got := strings.Join(briefingLines(&Event{SpaceImpact: SpaceImpactNone}), "\n"); strings.Contains(got, "Reserve") || strings.Contains(got, "Expect") {
		t.Errorf("emitted a space/headcount line with nothing to say: %q", got)
	}
}

// The briefing is internal, but it is assembled next to the public feed. This
// pins that prep notes never migrate into the feed payload.
func TestPrepNotesStayOutOfTheFeedProjection(t *testing.T) {
	e := &Event{Title: "X", Slug: "x", PrepNotes: "SECRET-STAFF-ONLY", Timezone: "UTC"}
	item := feedItem(e, Settings{}, time.Now().AddDate(0, feedWindowMonths, 0))
	blob := item.Title + item.Summary + item.Description + item.Location
	if strings.Contains(blob, "SECRET-STAFF-ONLY") {
		t.Fatal("prep notes reached the public feed payload")
	}
	if !strings.Contains(buildDescription(e), "SECRET-STAFF-ONLY") {
		t.Fatal("prep notes missing from the calendar body, where staff need them")
	}
}
