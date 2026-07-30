package events

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
)

const testCalendar = "events@example.com"

// syncFixture wires a fixture's pool into the package singleton so the sync
// methods (which hang off *App) can run against the test database.
type syncFixture struct {
	*fixture
	app  *App
	cal  *fakeCalendar
	sett Settings
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	f := newFixture(t)

	sett, err := f.svc.SaveSettings(f.ctx, Settings{
		TenantID:          f.tenant.ID,
		CalendarID:        testCalendar,
		Timezone:          "America/Denver",
		PublicURLTemplate: "https://example.com/events/{slug}",
	})
	if err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	sf := &syncFixture{
		fixture: f,
		app:     &App{pool: f.pool, svc: f.svc},
		cal:     newFakeCalendar(),
		sett:    sett,
	}
	// Inject the fake through the same seam production uses, so RunSync and
	// RunReconcile are exercised end to end rather than only their inner parts.
	sf.app.resolveWriter = func(context.Context, uuid.UUID) (calendarWriter, Settings, error) {
		if !sf.sett.CalendarConfigured() {
			return nil, sf.sett, ErrNoCalendar
		}
		return sf.cal, sf.sett, nil
	}
	return sf
}

// sync runs the tenant's events through syncOne, which is the unit the cron
// path also uses.
func (sf *syncFixture) sync(t *testing.T) Summary {
	t.Helper()
	events, err := listSyncCandidates(sf.ctx, sf.pool, sf.tenant.ID)
	if err != nil {
		t.Fatalf("listSyncCandidates: %v", err)
	}
	var sum Summary
	for i := range events {
		d, err := sf.app.syncOne(sf.ctx, sf.cal, sf.sett, &events[i])
		if err != nil {
			t.Fatalf("syncOne(%s): %v", events[i].Title, err)
		}
		switch d {
		case deltaCreated:
			sum.Created++
		case deltaUpdated:
			sum.Updated++
		case deltaDeleted:
			sum.Deleted++
		case deltaNone:
			sum.Skipped++
		}
	}
	return sum
}

func (sf *syncFixture) publish(t *testing.T, e *Event) *Event {
	t.Helper()
	res, err := sf.svc.Publish(sf.ctx, sf.tenant.ID, e.ID)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return res.Event
}

func TestSyncWritesPublishedEventOnly(t *testing.T) {
	sf := newSyncFixture(t)
	draft := sf.create(t, CreateParams{Title: "Draft Night"})
	live := sf.create(t, CreateParams{Title: "Trivia Night", Visibility: VisibilityPublic})
	sf.publish(t, live)

	if sum := sf.sync(t); sum.Created != 1 {
		t.Fatalf("expected 1 create, got %+v", sum)
	}
	if _, ok := sf.cal.get(testCalendar, googleEventID(live.ID)); !ok {
		t.Error("published event is not on the calendar")
	}
	if _, ok := sf.cal.get(testCalendar, googleEventID(draft.ID)); ok {
		t.Error("a draft reached the calendar; drafts must appear nowhere")
	}
}

// A private booking belongs on the shared team calendar — staff need it and
// the food partner caters it. Visibility gates the website, not the calendar.
func TestSyncWritesPrivateEventToCalendar(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Sarah's 40th", Visibility: VisibilityPrivate})
	sf.publish(t, e)
	sf.sync(t)

	ev, ok := sf.cal.get(testCalendar, googleEventID(e.ID))
	if !ok {
		t.Fatal("a published private booking did not reach the team calendar")
	}
	if ev.Summary != "🔒 Private — Sarah's 40th" {
		t.Errorf("summary = %q, want the private prefix", ev.Summary)
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Trivia"})
	sf.publish(t, e)
	sf.sync(t)

	sf.cal.resetOps()
	sum := sf.sync(t)
	if sum.Created+sum.Updated+sum.Deleted != 0 {
		t.Errorf("second run changed something: %+v", sum)
	}
	if ops := sf.cal.writeOps(); len(ops) != 0 {
		t.Errorf("second run issued writes: %v", ops)
	}
}

// The regression for the copied-hash bug: the shift sync's contentHash omits
// recurrence, so editing the repeat day would produce an identical hash and
// the write would be silently skipped.
func TestSyncRewritesWhenRecurrenceChanges(t *testing.T) {
	sf := newSyncFixture(t)
	// 2026-09-15 is a Tuesday.
	e := sf.create(t, CreateParams{
		Title: "Trivia", StartsAt: "2026-09-15 19:00", RRule: "FREQ=WEEKLY;BYDAY=TU",
	})
	sf.publish(t, e)
	sf.sync(t)

	before, _ := sf.cal.get(testCalendar, googleEventID(e.ID))
	if len(before.Recurrence) != 1 {
		t.Fatalf("recurrence not written to the calendar: %+v", before.Recurrence)
	}

	// Move it to Wednesdays — 2026-09-16.
	newStart, newRule := "2026-09-16 19:00", "FREQ=WEEKLY;BYDAY=WE"
	if _, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID, UpdateParams{
		StartsAt: &newStart, RRule: &newRule,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if sum := sf.sync(t); sum.Updated != 1 {
		t.Fatalf("changing the repeat day did not trigger a rewrite: %+v", sum)
	}
	after, _ := sf.cal.get(testCalendar, googleEventID(e.ID))
	if after.Recurrence[0] == before.Recurrence[0] {
		t.Errorf("calendar still holds the old series: %v", after.Recurrence)
	}
}

// Google expands a recurring series in the calendar's own default zone unless
// the event carries a named zone, which silently drifts it at every DST change.
func TestSyncSetsTimeZoneOnRecurringEvents(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{
		Title: "Trivia", StartsAt: "2026-09-15 19:00",
		Timezone: "America/Denver", RRule: "FREQ=WEEKLY;BYDAY=TU",
	})
	sf.publish(t, e)
	sf.sync(t)

	ev, _ := sf.cal.get(testCalendar, googleEventID(e.ID))
	if ev.Start == nil || ev.Start.TimeZone != "America/Denver" {
		t.Errorf("start timezone = %+v, want America/Denver", ev.Start)
	}
	if ev.End == nil || ev.End.TimeZone != "America/Denver" {
		t.Errorf("end timezone = %+v, want America/Denver", ev.End)
	}
}

func TestSyncRemovesCancelledEvent(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Doomed"})
	sf.publish(t, e)
	sf.sync(t)
	if sf.cal.count(testCalendar) != 1 {
		t.Fatal("setup: event not on the calendar")
	}

	if _, err := sf.svc.Cancel(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if sum := sf.sync(t); sum.Deleted != 1 {
		t.Fatalf("cancel did not remove the calendar entry: %+v", sum)
	}
	if sf.cal.count(testCalendar) != 0 {
		t.Error("calendar entry survived a cancel")
	}
}

func TestSyncRemovesUnpublishedEvent(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Back To Draft"})
	sf.publish(t, e)
	sf.sync(t)

	if _, err := sf.svc.Unpublish(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Unpublish: %v", err)
	}
	if sum := sf.sync(t); sum.Deleted != 1 {
		t.Fatalf("unpublish did not remove the calendar entry: %+v", sum)
	}
}

// Republishing reuses the deterministic id, so the entry comes back in place
// rather than as a duplicate.
func TestSyncRepublishReusesSameEntry(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "On Again Off Again"})
	sf.publish(t, e)
	sf.sync(t)
	if _, err := sf.svc.Cancel(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	sf.sync(t)
	if _, err := sf.svc.Reopen(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	sf.publish(t, e)
	sf.sync(t)

	if sf.cal.count(testCalendar) != 1 {
		t.Fatalf("expected exactly 1 entry after republish, got %d", sf.cal.count(testCalendar))
	}
	ev, ok := sf.cal.get(testCalendar, googleEventID(e.ID))
	if !ok {
		t.Fatal("republished event is missing")
	}
	if ev.Status != "confirmed" {
		t.Errorf("status = %q, want confirmed asserted explicitly", ev.Status)
	}
}

// Google first, database second. If the delete fails, the row must keep its
// handle so the next run can retry — losing it strands a live entry.
func TestSyncKeepsHandleWhenDeleteFails(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Stubborn"})
	sf.publish(t, e)
	sf.sync(t)
	if _, err := sf.svc.Cancel(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	sf.cal.failDelete = errors.New("google is down")
	events, _ := listSyncCandidates(sf.ctx, sf.pool, sf.tenant.ID)
	if _, err := sf.app.syncOne(sf.ctx, sf.cal, sf.sett, &events[0]); err == nil {
		t.Fatal("expected the delete failure to propagate")
	}

	reloaded, err := sf.svc.Get(sf.ctx, sf.tenant.ID, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.GCalEventID == "" {
		t.Fatal("the calendar handle was cleared despite the delete failing; the entry is now stranded")
	}
	// The retry succeeds and cleans up.
	if sum := sf.sync(t); sum.Deleted != 1 {
		t.Fatalf("retry did not remove the entry: %+v", sum)
	}
}

// Repointing the app at a new calendar must drain the old one, or every
// existing entry is stranded where reconcile will never look.
func TestSyncDrainsPreviousCalendarOnRepoint(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Moving House"})
	sf.publish(t, e)
	sf.sync(t)
	if sf.cal.count(testCalendar) != 1 {
		t.Fatal("setup: not on the original calendar")
	}

	const newCalendar = "new-events@example.com"
	sf.sett.CalendarID = newCalendar
	sf.sync(t)

	if sf.cal.count(testCalendar) != 0 {
		t.Error("the entry was left behind on the old calendar")
	}
	if _, ok := sf.cal.get(newCalendar, googleEventID(e.ID)); !ok {
		t.Error("the entry did not appear on the new calendar")
	}
}

func TestSyncSkipsWhenNoCalendarConfigured(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Nowhere To Go"})
	sf.publish(t, e)

	sf.sett.CalendarID = ""
	if sum := sf.sync(t); sum.Created != 0 {
		t.Fatalf("wrote to a calendar that is not configured: %+v", sum)
	}
	if len(sf.cal.writeOps()) != 0 {
		t.Errorf("issued writes with no calendar configured: %v", sf.cal.writeOps())
	}
}

func TestBuildSummaryPrefixes(t *testing.T) {
	cases := []struct {
		name string
		e    *Event
		want string
	}{
		{"public onsite", &Event{Title: "Trivia", Visibility: VisibilityPublic, Venue: VenueOnsite}, "🍺 Trivia"},
		{"private", &Event{Title: "Sarah's 40th", Visibility: VisibilityPrivate, Venue: VenueOnsite}, "🔒 Private — Sarah's 40th"},
		{"offsite", &Event{Title: "Beer Fest", Visibility: VisibilityPublic, Venue: VenueOffsite}, "🚚 Offsite — Beer Fest"},
		{"cancelled", &Event{Title: "Trivia", Status: StatusCancelled}, "❌ CANCELLED — Trivia"},
		{"untitled", &Event{Visibility: VisibilityPublic}, "🍺 Event"},
	}
	for _, c := range cases {
		if got := buildSummary(c.e); got != c.want {
			t.Errorf("%s: buildSummary = %q, want %q", c.name, got, c.want)
		}
	}
}

// prep_notes reach the calendar (staff read it) but must never reach the feed.
func TestBuildDescriptionIncludesPrepNotes(t *testing.T) {
	e := &Event{Title: "Trivia", Description: "Come play", PrepNotes: "Prep 40 glasses"}
	got := buildDescription(e)
	for _, want := range []string{"Come play", "Prep 40 glasses", "Managed by Kit"} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing %q:\n%s", want, got)
		}
	}
}

func TestContentHashCoversRecurrenceAndStatus(t *testing.T) {
	base := &googlecalendar.Event{
		ID: "x", Summary: "Trivia", Status: "confirmed",
		Start: &googlecalendar.EventDateTime{DateTime: "2026-09-15T19:00:00-06:00", TimeZone: "America/Denver"},
		End:   &googlecalendar.EventDateTime{DateTime: "2026-09-15T21:00:00-06:00", TimeZone: "America/Denver"},
	}
	h := contentHash(base, testCalendar)

	withRule := *base
	withRule.Recurrence = []string{"RRULE:FREQ=WEEKLY;BYDAY=TU"}
	if contentHash(&withRule, testCalendar) == h {
		t.Error("adding a recurrence rule did not change the hash")
	}

	otherRule := *base
	otherRule.Recurrence = []string{"RRULE:FREQ=WEEKLY;BYDAY=WE"}
	if contentHash(&withRule, testCalendar) == contentHash(&otherRule, testCalendar) {
		t.Error("two different repeat days produced the same hash")
	}

	cancelled := *base
	cancelled.Status = "cancelled"
	if contentHash(&cancelled, testCalendar) == h {
		t.Error("changing status did not change the hash")
	}

	if contentHash(base, "other@example.com") == h {
		t.Error("changing the target calendar did not change the hash")
	}
}
