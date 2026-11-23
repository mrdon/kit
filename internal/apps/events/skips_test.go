package events

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// weeklyFixture creates a weekly 19:00-21:00 series starting a week out and
// returns it with the date two weeks after its start, the night tests skip.
func weeklyFixture(t *testing.T, f *fixture) (*Event, time.Time) {
	t.Helper()
	start := time.Now().AddDate(0, 0, 7)
	day := start.Format(skipDayLayout)
	e := f.create(t, CreateParams{
		Title: "Trivia", StartsAt: day + " 19:00", EndsAt: day + " 21:00", RRule: "FREQ=WEEKLY",
	})
	return e, e.StartsAt.In(e.Loc()).AddDate(0, 0, 14)
}

func hasOccurrenceOn(e *Event, day time.Time) bool {
	loc := e.Loc()
	d := day.In(loc)
	from := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	return len(e.Occurrences(from, from.AddDate(0, 0, 1))) > 0
}

func TestSkipDateDropsOnlyThatNight(t *testing.T) {
	f := newFixture(t)
	e, skip := weeklyFixture(t, f)
	skips := []string{skip.Format(skipDayLayout)}
	got, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{SkipDates: &skips})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if hasOccurrenceOn(got, skip) {
		t.Fatalf("%s still has an occurrence after being skipped", skip.Format(skipDayLayout))
	}
	if !hasOccurrenceOn(got, skip.AddDate(0, 0, -7)) || !hasOccurrenceOn(got, skip.AddDate(0, 0, 7)) {
		t.Fatal("skipping one night removed its neighbours too")
	}
	want := "EXDATE;TZID=" + got.Timezone + ":" + skip.Format("20060102") + "T190000"
	if line := exdateLine(got); line != want {
		t.Fatalf("exdate line = %q, want %q", line, want)
	}
	if !strings.Contains(strings.Join(recurrenceLines(got), "|"), want) {
		t.Fatal("the Google recurrence lines do not carry the skip")
	}
}

// Moving the series' start time must not bring a skipped night back: the skip
// matches by date, and the calendar line is re-timed to the new start.
func TestSkipSurvivesATimeChange(t *testing.T) {
	f := newFixture(t)
	e, skip := weeklyFixture(t, f)
	skips := []string{skip.Format(skipDayLayout)}
	if _, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{SkipDates: &skips}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	later := e.StartsAt.In(e.Loc()).Format(skipDayLayout) + " 20:00"
	got, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{StartsAt: &later})
	if err != nil {
		t.Fatalf("moving the start: %v", err)
	}
	if hasOccurrenceOn(got, skip) {
		t.Fatal("changing the start time un-skipped the night")
	}
	if !strings.HasSuffix(exdateLine(got), "T200000") {
		t.Fatalf("exdate not re-timed to the new start: %q", exdateLine(got))
	}
}

func TestSkipRejectsADayTheEventDoesNotHappen(t *testing.T) {
	f := newFixture(t)
	e, skip := weeklyFixture(t, f)
	wrong := []string{skip.AddDate(0, 0, 1).Format(skipDayLayout)}
	_, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{SkipDates: &wrong})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for a day with no occurrence", err)
	}
}

func TestSkipRejectsAOneOff(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{Title: "One night"})
	skips := []string{e.StartsAt.In(e.Loc()).Format(skipDayLayout)}
	_, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{SkipDates: &skips})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for skipping a one-off", err)
	}
}

func TestEmptySkipListUnskips(t *testing.T) {
	f := newFixture(t)
	e, skip := weeklyFixture(t, f)
	skips := []string{skip.Format(skipDayLayout)}
	if _, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{SkipDates: &skips}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	none := []string{}
	got, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{SkipDates: &none})
	if err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if !hasOccurrenceOn(got, skip) {
		t.Fatal("an empty skip list did not bring the night back")
	}
}
