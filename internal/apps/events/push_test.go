package events

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The whole point of the hook is that it hangs off the SERVICE, so console,
// agent and MCP all get the immediate push. These drive the service directly,
// which is the shared path all three take.
func TestPublishPushesToCalendarImmediately(t *testing.T) {
	sf := newSyncFixture(t)
	var pushed []string
	sf.svc.push = func(_ context.Context, e *Event) error {
		pushed = append(pushed, e.Title+":"+string(e.Status))
		return nil
	}

	e := sf.create(t, CreateParams{Title: "Bike Night", Visibility: VisibilityPublic})
	if len(pushed) != 0 {
		t.Fatalf("a draft was pushed to the calendar: %v", pushed)
	}
	if _, err := sf.svc.Publish(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(pushed) != 1 || !strings.HasSuffix(pushed[0], ":published") {
		t.Fatalf("publish did not push immediately: %v", pushed)
	}

	if _, err := sf.svc.Cancel(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(pushed) != 2 || !strings.HasSuffix(pushed[1], ":cancelled") {
		t.Fatalf("cancel did not push immediately: %v", pushed)
	}
}

// An edit to a live event must reach the calendar now: staff reading a stale
// start time is worse than the edit not having happened.
func TestUpdatePushesToCalendarImmediately(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Quiz", Visibility: VisibilityPublic})
	sf.publish(t, e)

	pushes := 0
	sf.svc.push = func(context.Context, *Event) error { pushes++; return nil }

	title := "Quiz Night"
	if _, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID, UpdateParams{Title: &title}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if pushes != 1 {
		t.Errorf("update pushed %d times, want 1", pushes)
	}
}

// A calendar outage must never cost the edit. The row is already saved and the
// cron retries, so the push failing is a warning, not an error.
func TestPushFailureWarnsButDoesNotFailPublish(t *testing.T) {
	sf := newSyncFixture(t)
	sf.svc.push = func(context.Context, *Event) error { return errors.New("google is down") }

	e := sf.create(t, CreateParams{Title: "Bike Night", Visibility: VisibilityPublic})
	res, err := sf.svc.Publish(sf.ctx, sf.tenant.ID, e.ID)
	if err != nil {
		t.Fatalf("a calendar outage failed the publish: %v", err)
	}
	if res.Event.Status != StatusPublished {
		t.Fatalf("event not published despite the row being saved: %s", res.Event.Status)
	}
	var found bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "next sync") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning told the user the calendar lagged: %v", res.Warnings)
	}
}

// With no hook installed (tests, or an unconfigured deployment) everything
// still works and simply waits for the cron.
func TestNoHookIsHarmless(t *testing.T) {
	sf := newSyncFixture(t)
	sf.svc.push = nil
	e := sf.create(t, CreateParams{Title: "Bike Night", Visibility: VisibilityPublic})
	if _, err := sf.svc.Publish(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Publish without a hook: %v", err)
	}
}
