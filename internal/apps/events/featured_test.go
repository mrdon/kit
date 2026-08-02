package events

import (
	"testing"
)

// Featured is editorial: it decides what the website leads with, and nothing
// else. It must reach the feed (the site cannot infer it) and must not leak
// into the surfaces it has no business changing.
func TestFeaturedReachesTheFeed(t *testing.T) {
	sf := newSyncFixture(t)
	yes := true

	plain := sf.create(t, CreateParams{Title: "Quiz Night", Visibility: VisibilityPublic})
	sf.publish(t, plain)
	star := sf.create(t, CreateParams{Title: "Anniversary", Visibility: VisibilityPublic, Featured: &yes})
	sf.publish(t, star)

	f, err := sf.svc.BuildFeed(sf.ctx, sf.tenant.ID, "")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	got := map[string]bool{}
	for _, e := range f.Events {
		got[e.Title] = e.Featured
	}
	if !got["Anniversary"] {
		t.Error("featured event did not reach the feed as featured")
	}
	if got["Quiz Night"] {
		t.Error("an unfeatured event was marked featured in the feed")
	}
}

// Toggling it off must actually clear the flag: a *bool that only ever set
// true would leave an event stuck at the top of the website forever.
func TestFeaturedCanBeTurnedOff(t *testing.T) {
	sf := newSyncFixture(t)
	yes, no := true, false

	e := sf.create(t, CreateParams{Title: "Anniversary", Visibility: VisibilityPublic, Featured: &yes})
	if !e.Featured {
		t.Fatal("create did not honour featured")
	}
	off, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID, UpdateParams{Featured: &no})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if off.Featured {
		t.Error("featured survived being switched off")
	}
	// An unrelated edit must leave it alone -- nil means "not mentioned".
	title := "Anniversary Party"
	back, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID, UpdateParams{Title: &title})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if back.Featured {
		t.Error("an unrelated edit turned featured back on")
	}
}

// It is editorial, so it must not alter what the calendar says: staff read
// that surface and nothing about their night changed.
func TestFeaturedDoesNotChangeTheCalendarBody(t *testing.T) {
	plain := &Event{Title: "Anniversary", Location: "Taproom", Timezone: "UTC"}
	starred := &Event{Title: "Anniversary", Location: "Taproom", Timezone: "UTC", Featured: true}
	if buildDescription(plain) != buildDescription(starred) {
		t.Error("featured leaked into the bartender's briefing")
	}
	if buildSummary(plain) != buildSummary(starred) {
		t.Error("featured leaked into the calendar title")
	}
}
