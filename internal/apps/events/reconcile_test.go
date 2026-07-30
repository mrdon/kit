package events

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
)

// The most important test in the app.
//
// Reconcile is the only path that deletes calendar entries. The calendar is
// shared: it holds staff meetings, the Square shift sync's entries, and
// whatever else people put there. If the sweep ever listed the calendar
// instead of filtering by ownership stamp, it would cheerfully delete all of
// it — and every other test here would still pass.
func TestReconcileIgnoresUnownedEvents(t *testing.T) {
	sf := newSyncFixture(t)
	otherTenant := uuid.New()

	// A human's meeting: no stamp at all.
	sf.cal.put(testCalendar, googlecalendar.Event{
		ID: "humanmeeting", Summary: "Quarterly planning",
	})
	// Another Kit feature writing to the same calendar.
	shiftProps := googlecalendar.OwnerProps("square_shifts", sf.tenant.ID)
	sf.cal.put(testCalendar, googlecalendar.Event{
		ID: "shiftentry", Summary: "Alice · 6:00am–2:00pm",
		ExtendedProperties: &googlecalendar.ExtendedProperties{Private: shiftProps},
	})
	// This app, but a different tenant.
	otherProps := googlecalendar.OwnerProps(AppName, otherTenant)
	sf.cal.put(testCalendar, googlecalendar.Event{
		ID: "othertenant", Summary: "Someone else's event",
		ExtendedProperties: &googlecalendar.ExtendedProperties{Private: otherProps},
	})

	plan, err := sf.app.planReconcile(sf.ctx, sf.cal, sf.sett, sf.tenant.ID)
	if err != nil {
		t.Fatalf("planReconcile: %v", err)
	}
	if len(plan.Delete) != 0 {
		var names []string
		for _, ev := range plan.Delete {
			names = append(names, ev.Summary)
		}
		t.Fatalf("reconcile would delete entries it does not own: %v", names)
	}

	if _, err := sf.app.applyReconcile(sf.ctx, sf.cal, sf.sett, plan); err != nil {
		t.Fatalf("applyReconcile: %v", err)
	}
	for _, id := range []string{"humanmeeting", "shiftentry", "othertenant"} {
		if _, ok := sf.cal.get(testCalendar, id); !ok {
			t.Errorf("reconcile deleted %q, which it does not own", id)
		}
	}
}

// Reconcile restores an entry someone deleted directly in Google. The regular
// sync cannot: its content hash still matches, so it skips the row.
func TestReconcileRestoresEntryDeletedInGoogle(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Trivia"})
	sf.publish(t, e)
	sf.sync(t)

	// Someone deletes it in the Google UI.
	if err := sf.cal.DeleteEvent(sf.ctx, testCalendar, googleEventID(e.ID)); err != nil {
		t.Fatalf("seeding the out-of-band delete: %v", err)
	}
	if sum := sf.sync(t); sum.Created != 0 {
		t.Fatal("the hash-based sync should not notice an out-of-band delete")
	}

	sum, err := sf.app.RunReconcile(sf.ctx, sf.tenant.ID)
	if err != nil {
		t.Fatalf("RunReconcile: %v", err)
	}
	if sum.Created != 1 {
		t.Fatalf("reconcile did not restore the entry: %+v", sum)
	}
	if _, ok := sf.cal.get(testCalendar, googleEventID(e.ID)); !ok {
		t.Error("the entry is still missing after reconcile")
	}
}

// The regression for the copied window filter.
//
// A Google recurring master carries the start of its FIRST occurrence. The
// shift sync spares anything starting outside a rolling window, so copying
// that would leave an orphaned weekly series — whose master start is years
// past — on the calendar forever. Every other reconcile test passes with the
// window in place; only this one fails.
func TestReconcileDeletesOrphanedRecurringSeriesWithPastStart(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{
		Title: "Long Running Trivia", StartsAt: "2024-01-02 19:00",
		RRule: "FREQ=WEEKLY;BYDAY=TU",
	})
	sf.publish(t, e)
	sf.sync(t)

	entry, ok := sf.cal.get(testCalendar, googleEventID(e.ID))
	if !ok {
		t.Fatal("setup: series not on the calendar")
	}
	if !strings.Contains(entry.Start.DateTime, "2024") {
		t.Fatalf("setup: master start is %q, expected the 2024 first occurrence", entry.Start.DateTime)
	}

	// The event goes away in Kit, but the sync fails to clean up (say Google
	// was down), so the series is left orphaned on the calendar.
	if _, err := sf.svc.Cancel(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := clearCalendarState(sf.ctx, sf.pool, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("simulating the lost handle: %v", err)
	}

	sum, err := sf.app.RunReconcile(sf.ctx, sf.tenant.ID)
	if err != nil {
		t.Fatalf("RunReconcile: %v", err)
	}
	if sum.Deleted != 1 {
		t.Fatalf("orphaned recurring series was not removed: %+v — a rolling time window would cause exactly this", sum)
	}
}

func TestReconcileRemovesOrphanedEntry(t *testing.T) {
	sf := newSyncFixture(t)
	// An entry this app owns, for an event that no longer exists in Kit.
	props := googlecalendar.OwnerProps(AppName, sf.tenant.ID)
	props["kitEventId"] = uuid.NewString()
	sf.cal.put(testCalendar, googlecalendar.Event{
		ID: googleEventID(uuid.New()), Summary: "🍺 Ghost Event",
		ExtendedProperties: &googlecalendar.ExtendedProperties{Private: props},
	})

	sum, err := sf.app.RunReconcile(sf.ctx, sf.tenant.ID)
	if err != nil {
		t.Fatalf("RunReconcile: %v", err)
	}
	if sum.Deleted != 1 {
		t.Fatalf("orphan was not removed: %+v", sum)
	}
	if sf.cal.count(testCalendar) != 0 {
		t.Error("the orphaned entry is still on the calendar")
	}
}

// A dry run must issue zero writes — not merely leave state unchanged. The op
// log is what makes that assertable.
func TestPreviewReconcileIsReadOnly(t *testing.T) {
	sf := newSyncFixture(t)
	props := googlecalendar.OwnerProps(AppName, sf.tenant.ID)
	sf.cal.put(testCalendar, googlecalendar.Event{
		ID: googleEventID(uuid.New()), Summary: "🍺 Ghost Event",
		ExtendedProperties: &googlecalendar.ExtendedProperties{Private: props},
	})
	live := sf.create(t, CreateParams{Title: "Trivia"})
	sf.publish(t, live)

	sf.cal.resetOps()
	plan, err := sf.app.planReconcile(sf.ctx, sf.cal, sf.sett, sf.tenant.ID)
	if err != nil {
		t.Fatalf("planReconcile: %v", err)
	}
	if ops := sf.cal.writeOps(); len(ops) != 0 {
		t.Fatalf("a preview issued writes: %v", ops)
	}
	if len(plan.Delete) != 1 || len(plan.Create) != 1 {
		t.Fatalf("plan = %d deletes, %d creates; want 1 and 1", len(plan.Delete), len(plan.Create))
	}

	// The rendering must name what would be deleted; "4 entries" is not
	// something an operator can meaningfully approve.
	out := FormatReconcilePlan(plan)
	if !strings.Contains(out, "Ghost Event") || !strings.Contains(out, "Trivia") {
		t.Errorf("plan rendering does not itemise the changes:\n%s", out)
	}
}

func TestReconcileCleanPassIsNoOp(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Trivia"})
	sf.publish(t, e)
	sf.sync(t)

	plan, err := sf.app.planReconcile(sf.ctx, sf.cal, sf.sett, sf.tenant.ID)
	if err != nil {
		t.Fatalf("planReconcile: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("a synced calendar should reconcile clean, got %+v", plan)
	}
	if out := FormatReconcilePlan(plan); !strings.Contains(out, "already matches") {
		t.Errorf("clean plan rendering = %q", out)
	}
}
