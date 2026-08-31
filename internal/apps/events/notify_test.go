package events

import (
	"strings"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/apps/square"
)

// The whole point of a shift notice is that a bartender is not surprised by a
// private booking. The website's default-deny gate is therefore exactly wrong
// here, and this is the test that fails if someone "fixes" the notice to reuse
// IsPubliclyVisible.
func TestNoticeBodyCarriesPrivateBookings(t *testing.T) {
	loc := mustLoc(t, "America/Denver")
	restoreNow(t, time.Date(2026, 8, 28, 7, 0, 0, 0, loc))

	thirty := 30
	private := Event{
		Title:              "Gollata wedding rehearsal",
		Status:             StatusPublished,
		Visibility:         VisibilityPrivate,
		SpaceImpact:        SpaceImpactPartial,
		ExpectedAttendance: &thirty,
		PrepNotes:          "Cash bar. They bring their own cake.",
	}
	start := time.Date(2026, 8, 28, 19, 0, 0, 0, loc)
	body := buildDetail(
		[]dayEvent{{Event: private, Start: start, End: start.Add(2 * time.Hour)}},
		Settings{Timezone: "America/Denver"}, loc,
	)

	for _, want := range []string{
		"Gollata wedding rehearsal",
		"7:00pm",
		"part of the room",
		"30 people",
		"Cash bar",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("notice is missing %q:\n%s", want, body)
		}
	}
}

// A private event has no website page, so offering one would send staff to a
// 404. Only publicly visible events carry a link.
func TestNoticeLinksOnlyPublicEvents(t *testing.T) {
	loc := mustLoc(t, "America/Denver")
	restoreNow(t, time.Date(2026, 8, 28, 7, 0, 0, 0, loc))
	settings := Settings{
		Timezone:          "America/Denver",
		PublicURLTemplate: "https://gravity.example/events/{slug}",
	}
	start := time.Date(2026, 8, 28, 19, 0, 0, 0, loc)

	private := dayEvent{
		Event: Event{Title: "Private hire", Slug: "private-hire", Status: StatusPublished, Visibility: VisibilityPrivate},
		Start: start, End: start.Add(time.Hour),
	}
	if body := buildDetail([]dayEvent{private}, settings, loc); strings.Contains(body, "gravity.example") {
		t.Errorf("private event leaked a website link:\n%s", body)
	}

	public := dayEvent{
		Event: Event{Title: "Trivia", Slug: "trivia", Status: StatusPublished, Visibility: VisibilityPublic},
		Start: start, End: start.Add(time.Hour),
	}
	if body := buildDetail([]dayEvent{public}, settings, loc); !strings.Contains(body, "gravity.example/events/trivia") {
		t.Errorf("public event should carry its link:\n%s", body)
	}
}

// Someone working a split day gets one notice naming both blocks, not two
// notices or a single range pretending they never left.
func TestNoticeRendersSplitShift(t *testing.T) {
	loc := mustLoc(t, "America/Denver")
	restoreNow(t, time.Date(2026, 8, 28, 7, 0, 0, 0, loc))

	got := shiftHours([]square.EnrichedShift{
		shiftAt(t, loc, 11, 15),
		shiftAt(t, loc, 18, 22),
	}, loc)
	if !strings.Contains(got, "and") || !strings.Contains(got, "11:00am") || !strings.Contains(got, "6:00pm") {
		t.Errorf("split shift should name both blocks, got %q", got)
	}
}

// The notice hash is what stops a second run re-DMing an unchanged day, and
// what lets a genuinely changed day through. Both directions matter.
//
// The stability half builds the body twice rather than hashing one string
// twice: the point is that buildNoticeBody itself is deterministic. Iterate a
// map somewhere in there and the same day would hash differently on every run,
// re-sending the same notice every morning until the events changed.
func TestNoticeHashTracksContent(t *testing.T) {
	loc := mustLoc(t, "America/Denver")
	restoreNow(t, time.Date(2026, 8, 28, 7, 0, 0, 0, loc))

	start := time.Date(2026, 8, 28, 19, 0, 0, 0, loc)
	day := []dayEvent{{
		Event: Event{Title: "Trivia", Status: StatusPublished, PrepNotes: "Two mics."},
		Start: start, End: start.Add(time.Hour),
	}}
	settings := Settings{Timezone: "America/Denver"}

	first := hashBody(buildDetail(day, settings, loc))
	second := hashBody(buildDetail(day, settings, loc))
	if first != second {
		t.Error("the same day must hash identically, or every run re-sends the same notice")
	}

	moved := []dayEvent{{
		Event: day[0].Event,
		Start: start.Add(time.Hour), End: start.Add(2 * time.Hour),
	}}
	if hashBody(buildDetail(moved, settings, loc)) == first {
		t.Error("a moved event must change the hash, or the follow-up never goes out")
	}
}

// Ambiguity here misdelivers one person's brief to another, so a partial match
// on several people must fail rather than pick.
func TestResolveTeamMemberRefusesAmbiguity(t *testing.T) {
	roster := []StaffMember{
		{TeamMemberID: "TM1", Name: "Don Brown"},
		{TeamMemberID: "TM2", Name: "Don Smith"},
		{TeamMemberID: "TM3", Name: "Sean Stewart"},
	}
	if _, err := resolveTeamMember(roster, "Don"); err == nil {
		t.Error("an ambiguous name must be refused, not guessed")
	}
	m, err := resolveTeamMember(roster, "Sean")
	if err != nil || m.TeamMemberID != "TM3" {
		t.Errorf("unique partial should resolve, got %v / %v", m, err)
	}
	// An exact name wins over the partial that also matches it.
	if m, err := resolveTeamMember(roster, "Don Brown"); err != nil || m.TeamMemberID != "TM1" {
		t.Errorf("exact name should resolve, got %v / %v", m, err)
	}
}

// The unmapped list is the actionable half of the mapping view — someone
// working who silently gets nothing. It must never be omitted.
func TestFormatStaffMapNamesTheUnmapped(t *testing.T) {
	roster := []StaffMember{
		{TeamMemberID: "TM1", Name: "Don Brown", Shifts: 2},
		{TeamMemberID: "TM2", Name: "Sean Stewart", Shifts: 5},
	}
	mappings := []StaffMapping{
		{SquareTeamMemberID: "TM1", SlackUserID: "U1", DisplayName: "Don Brown"},
	}
	got := FormatStaffMap(mappings, roster, nil)
	if !strings.Contains(got, "NOT mapped") || !strings.Contains(got, "Sean Stewart") {
		t.Errorf("unmapped staff must be named:\n%s", got)
	}
	if !strings.Contains(got, "Don Brown → Don Brown") {
		t.Errorf("mapped staff should be shown:\n%s", got)
	}
}

func shiftAt(t *testing.T, loc *time.Location, startHour, endHour int) square.EnrichedShift {
	t.Helper()
	day := timeNow().In(loc)
	start := time.Date(day.Year(), day.Month(), day.Day(), startHour, 0, 0, 0, loc)
	end := time.Date(day.Year(), day.Month(), day.Day(), endHour, 0, 0, 0, loc)
	return square.EnrichedShift{
		TeamMemberID: "TM1",
		Member:       "Don Brown",
		StartAt:      start.Format(time.RFC3339),
		EndAt:        end.Format(time.RFC3339),
	}
}

// restoreNow pins the package clock for one test and puts it back after, so a
// notice test cannot pass in the morning and fail after lunch.
func restoreNow(t *testing.T, at time.Time) {
	t.Helper()
	prev := timeNow
	timeNow = func() time.Time { return at }
	t.Cleanup(func() { timeNow = prev })
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("loading %s: %v", name, err)
	}
	return loc
}

// The headline is what the channel sees without opening anything, so it has to
// carry the date, who is on, and what is on. Mapped staff become pings;
// unmapped staff are still named, because a missing name reads as "nobody is
// covering that" rather than "setup is incomplete".
func TestHeadlineMentionsMappedAndNamesUnmapped(t *testing.T) {
	loc := mustLoc(t, "America/Denver")
	restoreNow(t, time.Date(2026, 9, 2, 8, 0, 0, 0, loc))
	day := startOfToday(loc)
	start := time.Date(2026, 9, 2, 17, 30, 0, 0, loc)

	events := []dayEvent{{
		Event: Event{Title: "Bike Night", Status: StatusPublished},
		Start: start, End: start.Add(2 * time.Hour),
	}}
	roster := "<@U123> 2:00pm–8:00pm, Coleridge Gollata 5:00pm–9:00pm"
	got := buildHeadline(day, roster, events, loc)

	if !strings.Contains(got, "<@U123>") {
		t.Errorf("a mapped person should be mentioned:\n%s", got)
	}
	if !strings.Contains(got, "Coleridge Gollata") {
		t.Errorf("an unmapped person should still be named:\n%s", got)
	}
	if !strings.Contains(got, "Bike Night") || !strings.Contains(got, "Wednesday 2 September") {
		t.Errorf("headline should carry the date and the day's events:\n%s", got)
	}
	// The detail belongs in the thread; a headline that inlined prep notes
	// would defeat the point of threading it.
	if strings.Contains(got, "Staff notes") {
		t.Errorf("detail must not leak into the headline:\n%s", got)
	}
}

// An event with nobody rostered is exactly the thing worth surfacing, so the
// headline says so rather than quietly omitting the roster.
func TestHeadlineSaysWhenNobodyIsOn(t *testing.T) {
	loc := mustLoc(t, "America/Denver")
	restoreNow(t, time.Date(2026, 9, 2, 8, 0, 0, 0, loc))
	start := time.Date(2026, 9, 2, 19, 0, 0, 0, loc)
	got := buildHeadline(startOfToday(loc), "", []dayEvent{{
		Event: Event{Title: "Quiz", Status: StatusPublished},
		Start: start, End: start.Add(time.Hour),
	}}, loc)
	if !strings.Contains(got, "nobody on the published schedule") {
		t.Errorf("an unstaffed day should say so:\n%s", got)
	}
}
