package events

import (
	"testing"
)

func promPtr(p Prominence) *Prominence { return &p }

// Featured is editorial: it decides what the website leads with, and nothing
// else. It must reach the feed (the site cannot infer it) and must not leak
// into the surfaces it has no business changing.
func TestFeaturedReachesTheFeed(t *testing.T) {
	sf := newSyncFixture(t)

	plain := sf.create(t, CreateParams{Title: "Quiz Night", Visibility: VisibilityPublic})
	sf.publish(t, plain)
	star := sf.create(t, CreateParams{Title: "Anniversary", Visibility: VisibilityPublic,
		Prominence: promPtr(ProminenceFeatured)})
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

// Demoting must actually take effect: a field that only ever set the top value
// would leave an event stuck at the top of the website forever.
func TestProminenceCanBeChangedAndLeftAlone(t *testing.T) {
	sf := newSyncFixture(t)

	e := sf.create(t, CreateParams{Title: "Anniversary", Visibility: VisibilityPublic,
		Prominence: promPtr(ProminenceFeatured)})
	if !e.IsFeatured() {
		t.Fatal("create did not honour prominence")
	}
	off, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID,
		UpdateParams{Prominence: promPtr(ProminenceNormal)})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if off.IsFeatured() {
		t.Error("featured survived being demoted to normal")
	}
	// An unrelated edit must leave it alone -- nil means "not mentioned".
	title := "Anniversary Party"
	back, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID, UpdateParams{Title: &title})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if back.Prominence != ProminenceNormal {
		t.Errorf("an unrelated edit changed prominence to %q", back.Prominence)
	}
}

// The default is the load-bearing value: a normal public event headlines its
// own day without anyone marking it as anything.
func TestProminenceDefaultsToNormal(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Bike Night", Visibility: VisibilityPublic})
	if e.Prominence != ProminenceNormal {
		t.Fatalf("prominence = %q, want normal", e.Prominence)
	}
	if e.IsFeatured() {
		t.Error("a plain event should not be featured")
	}
}

// Background is public and published like anything else -- it is not a
// visibility trick. It must reach the feed, carrying its own label so the
// website can decide what to do with it.
func TestBackgroundReachesTheFeedAsBackground(t *testing.T) {
	sf := newSyncFixture(t)
	deal := sf.create(t, CreateParams{Title: "Half-price calzones", Visibility: VisibilityPublic,
		Prominence: promPtr(ProminenceBackground)})
	sf.publish(t, deal)

	f, err := sf.svc.BuildFeed(sf.ctx, sf.tenant.ID, "")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	if len(f.Events) != 1 {
		t.Fatalf("feed has %d events, want 1", len(f.Events))
	}
	if got := f.Events[0]; got.Prominence != "background" || got.Featured {
		t.Fatalf("feed item = %+v, want background and not featured", got)
	}
}

// The boolean predecessor keeps working: a saved agent prompt or an MCP client
// written against the old schema must not start failing.
func TestResolveProminenceAcceptsTheLegacyBoolean(t *testing.T) {
	yes, no := true, false
	if got := ResolveProminence(nil, &yes); got == nil || *got != ProminenceFeatured {
		t.Errorf("featured:true = %v, want featured", got)
	}
	// false only ever meant "not the website's lead", never "background".
	if got := ResolveProminence(nil, &no); got == nil || *got != ProminenceNormal {
		t.Errorf("featured:false = %v, want normal", got)
	}
	if got := ResolveProminence(nil, nil); got != nil {
		t.Errorf("neither field = %v, want nil (not mentioned)", got)
	}
	// The specific statement wins over the vague one.
	if got := ResolveProminence(promPtr(ProminenceBackground), &yes); got == nil || *got != ProminenceBackground {
		t.Errorf("both set = %v, want background", got)
	}
}

// It is editorial, so it must not alter what the calendar says: staff read
// that surface and nothing about their night changed.
func TestFeaturedDoesNotChangeTheCalendarBody(t *testing.T) {
	plain := &Event{Title: "Anniversary", Location: "Taproom", Timezone: "UTC"}
	starred := &Event{Title: "Anniversary", Location: "Taproom", Timezone: "UTC",
		Prominence: ProminenceFeatured}
	if buildDescription(plain) != buildDescription(starred) {
		t.Error("featured leaked into the bartender's briefing")
	}
	if buildSummary(plain) != buildSummary(starred) {
		t.Error("featured leaked into the calendar title")
	}
}
