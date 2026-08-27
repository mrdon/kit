package events

import (
	"errors"
	"testing"
	"time"
)

func TestCreateWithDateListStoresTheEarliestAsTheStart(t *testing.T) {
	f := newFixture(t)
	// Supplied out of order, and with one earlier than the nominal start.
	e := f.create(t, CreateParams{
		Title: "Supper Club", StartsAt: "2026-10-02 18:00",
		RepeatDates: []string{"2026-11-06 18:00", "2026-09-04 18:00"},
	})

	if got := e.StartsAt.In(e.Loc()).Format("2006-01-02"); got != "2026-09-04" {
		t.Errorf("start = %s, want the earliest date 2026-09-04", got)
	}
	if len(e.RDates) != 2 {
		t.Fatalf("expected 2 extra dates, got %v", e.RDates)
	}
	if !e.Repeats() {
		t.Error("an event with a date list repeats")
	}
	// The round trip through Postgres must preserve the array.
	reloaded, err := f.svc.Get(f.ctx, f.tenant.ID, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(reloaded.RDates) != 2 {
		t.Errorf("dates did not survive the round trip: %v", reloaded.RDates)
	}
}

func TestUpdateReplacesTheWholeDateList(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{
		Title: "Market", StartsAt: "2026-09-04 10:00",
		RepeatDates: []string{"2026-10-02 10:00", "2026-11-06 10:00"},
	})

	replacement := []string{"2026-12-04 10:00"}
	got, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{RepeatDates: &replacement})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got.RDates) != 1 {
		t.Fatalf("the list should be replaced wholesale, got %v", got.RDates)
	}
	if d := got.RDates[0].In(got.Loc()).Format("2006-01-02"); d != "2026-12-04" {
		t.Errorf("date = %s", d)
	}

	// An empty list is a real instruction, not an omission: back to a one-off.
	empty := []string{}
	got, err = f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{RepeatDates: &empty})
	if err != nil {
		t.Fatalf("Update clearing: %v", err)
	}
	if len(got.RDates) != 0 || got.Repeats() {
		t.Errorf("an empty list should clear the series, got %v", got.RDates)
	}
}

// Nil means "not sent". A patch touching only the title must not wipe the
// dates -- the console form and the chat agent edit the same rows.
func TestUpdateLeavesDatesAloneWhenNotSent(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{
		Title: "Market", StartsAt: "2026-09-04 10:00",
		RepeatDates: []string{"2026-10-02 10:00"},
	})

	title := "Winter Market"
	got, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{Title: &title})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got.RDates) != 1 {
		t.Errorf("an unrelated patch cleared the date list: %v", got.RDates)
	}
}

// Moving the zone preserves the wall clock. Every date has to move with the
// start, or a six-date series ends up with its first night at 7pm and the rest
// an hour out.
func TestUpdateRezonesTheWholeDateList(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{
		Title: "Tour", StartsAt: "2026-09-04 19:00", Timezone: "America/Denver",
		RepeatDates: []string{"2026-10-02 19:00"},
	})

	chicago := "America/Chicago"
	got, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{Timezone: &chicago})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got.RDates) != 1 {
		t.Fatalf("lost the date list: %v", got.RDates)
	}
	if h := got.RDates[0].In(got.Loc()).Hour(); h != 19 {
		t.Errorf("extra date drifted to %d:00; the wall clock should be preserved", h)
	}
}

// The date-list twin of TestListKeepsRecurringEventsWithPastStart: a series
// whose FIRST date has passed but which still has dates ahead must stay in the
// upcoming view.
func TestListKeepsDateListEventsWithPastStart(t *testing.T) {
	f := newFixture(t)
	past := time.Now().AddDate(0, -2, 0).Format("2006-01-02 15:04")
	future := time.Now().AddDate(0, 2, 0).Format("2006-01-02 15:04")

	live := f.create(t, CreateParams{
		Title: "Running Series", StartsAt: past, RepeatDates: []string{future},
	})
	f.create(t, CreateParams{Title: "Old One Off", StartsAt: past})

	from := time.Now()
	list, err := f.svc.List(f.ctx, f.tenant.ID, ListFilter{From: &from})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found, foundOneOff bool
	for _, e := range list {
		if e.ID == live.ID {
			found = true
		}
		if e.Title == "Old One Off" {
			foundOneOff = true
		}
	}
	if !found {
		t.Error("a series with dates still ahead was dropped from the upcoming list")
	}
	if foundOneOff {
		t.Error("a genuinely past one-off leaked into the upcoming list")
	}
}

// Unlike a rule, a date list is finite -- so once every date has passed the
// event must fall out of the upcoming view rather than pinning itself to the
// top of the list forever.
func TestListDropsDateListEventsOnceEveryDateHasPassed(t *testing.T) {
	f := newFixture(t)
	finished := f.create(t, CreateParams{
		Title:       "Finished Series",
		StartsAt:    time.Now().AddDate(0, -3, 0).Format("2006-01-02 15:04"),
		RepeatDates: []string{time.Now().AddDate(0, -1, 0).Format("2006-01-02 15:04")},
	})

	from := time.Now()
	list, err := f.svc.List(f.ctx, f.tenant.ID, ListFilter{From: &from})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range list {
		if e.ID == finished.ID {
			t.Fatal("a series whose dates have all passed is still listed as upcoming")
		}
	}
}

func TestCreateRejectsAllDayWithDateList(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(f.ctx, f.tenant.ID, CreateParams{
		Title: "AllDay", StartsAt: "2026-09-15", AllDay: true,
		RepeatDates: []string{"2026-10-02"},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("all-day plus a date list must be refused, got %v", err)
	}
}

// Monthly is now a supported frequency end to end, not just in the expander.
func TestCreateAcceptsMonthlyRule(t *testing.T) {
	f := newFixture(t)
	// 2026-09-04 is the first Friday of September.
	e := f.create(t, CreateParams{
		Title: "First Friday", StartsAt: "2026-09-04 18:00", RRule: "FREQ=MONTHLY;BYDAY=1FR",
	})
	if e.Rule() == nil || e.Rule().Freq != FreqMonthly {
		t.Fatalf("monthly rule did not survive: %q", e.RRule)
	}
	occ := e.Occurrences(e.StartsAt, e.StartsAt.AddDate(0, 3, 0))
	if len(occ) != 3 {
		t.Errorf("expected 3 occurrences in 3 months, got %d", len(occ))
	}
}

// The same DTSTART-agreement rule the weekly path has: RFC 5545 would treat the
// start as an occurrence even when the pattern excludes it.
func TestCreateRejectsMonthlyRuleThatMissesTheStart(t *testing.T) {
	f := newFixture(t)
	// 2026-09-18 is the third Friday, not the first.
	_, err := f.svc.Create(f.ctx, f.tenant.ID, CreateParams{
		Title: "Mismatch", StartsAt: "2026-09-18 18:00", RRule: "FREQ=MONTHLY;BYDAY=1FR",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a monthly rule that misses its own start must be refused, got %v", err)
	}
}
