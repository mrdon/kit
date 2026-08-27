package events

import (
	"strings"
	"testing"
	"time"
)

// dbNow reads the database's clock. See the note in
// TestUnpublishingCountsAsAPendingChange for why the host's will not do.
func dbNow(t *testing.T, sf *syncFixture) time.Time {
	t.Helper()
	var now time.Time
	if err := sf.pool.QueryRow(sf.ctx, "SELECT now()").Scan(&now); err != nil {
		t.Fatalf("reading the database clock: %v", err)
	}
	return now.UTC()
}

// The whole value of the pending list is that it only counts changes the
// PUBLIC would see. Private bookings, drafts and staff-note edits are the bulk
// of daily activity; counting them would nag for rebuilds that produce a
// byte-identical site.
func TestPendingChangesIgnoresNonPublicWork(t *testing.T) {
	sf := newSyncFixture(t)

	// Drafts and private events: invisible to the web, whatever happens.
	draft := sf.create(t, CreateParams{Title: "Draft Idea", Visibility: VisibilityPublic})
	party := sf.create(t, CreateParams{Title: "Sarah's 40th", Visibility: VisibilityPrivate})
	sf.publish(t, party)
	notes := "Reserve the back room"
	if _, err := sf.svc.Update(sf.ctx, sf.tenant.ID, party.ID, UpdateParams{PrepNotes: &notes}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	pending, err := sf.svc.PendingChanges(sf.ctx, sf.tenant.ID, nil, 50)
	if err != nil {
		t.Fatalf("PendingChanges: %v", err)
	}
	for _, c := range pending {
		t.Errorf("non-public work counted as pending: %s %s", c.Title, c.Verb())
	}

	// Publishing a public event is the thing that does count.
	sf.publish(t, draft)
	pending, err = sf.svc.PendingChanges(sf.ctx, sf.tenant.ID, nil, 50)
	if err != nil {
		t.Fatalf("PendingChanges: %v", err)
	}
	if len(pending) != 1 || pending[0].Title != "Draft Idea" || pending[0].Verb() != "published" {
		t.Fatalf("expected one 'published' change for Draft Idea, got %+v", pending)
	}
}

// Unpublishing removes an event from the site, so it must count even though
// the event is no longer public AFTER the change. Testing only the new state
// would miss it.
func TestUnpublishingCountsAsAPendingChange(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Bike Night", Visibility: VisibilityPublic})
	sf.publish(t, e)

	// The DB's clock, not the host's. Audit rows are stamped with Postgres
	// now(), and in a containerised Postgres the two clocks drift by tens of
	// milliseconds in either direction -- enough to put the row a hair before a
	// host-taken cutoff and make this test fail perhaps one run in five.
	// Production never has this problem: it passes site_built_at, which is
	// itself a DB timestamp, so both sides of the comparison share one clock.
	after := dbNow(t, sf)
	if _, err := sf.svc.Unpublish(sf.ctx, sf.tenant.ID, e.ID); err != nil {
		t.Fatalf("Unpublish: %v", err)
	}
	pending, err := sf.svc.PendingChanges(sf.ctx, sf.tenant.ID, &after, 50)
	if err != nil {
		t.Fatalf("PendingChanges: %v", err)
	}
	if len(pending) != 1 || pending[0].Verb() != "unpublished" {
		t.Fatalf("unpublishing a live event was not counted: %+v", pending)
	}
}

// `since` is what makes the list mean "since the last build" rather than
// "ever".
func TestPendingChangesRespectsTheCutoff(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Quiz", Visibility: VisibilityPublic})
	sf.publish(t, e)

	cutoff := dbNow(t, sf).Add(time.Second)
	pending, err := sf.svc.PendingChanges(sf.ctx, sf.tenant.ID, &cutoff, 50)
	if err != nil {
		t.Fatalf("PendingChanges: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("changes before the cutoff were still listed: %+v", pending)
	}
}

// Publishing with no hook must fail with an actionable message rather than a
// generic error -- the admin has to know where to get one.
func TestPublishSiteWithoutHookExplainsHowToSetOne(t *testing.T) {
	sf := newSyncFixture(t)
	_, err := sf.svc.PublishSite(sf.ctx, sf.tenant.ID, "test")
	if err == nil {
		t.Fatal("publishing with no build hook succeeded")
	}
	if !strings.Contains(err.Error(), "Build hooks") {
		t.Errorf("message does not say where to find a hook: %v", err)
	}
}

// The rendering both chat surfaces use.
func TestFormatSiteStatus(t *testing.T) {
	now := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	empty := FormatSiteStatus(SiteStatus{HookConfigured: true, BuiltAt: &now})
	if !strings.Contains(empty, "Nothing is waiting") {
		t.Errorf("clean state not reported: %s", empty)
	}
	busy := FormatSiteStatus(SiteStatus{
		HookConfigured: true, BuiltAt: &now,
		Pending: []PendingChange{{Action: actionEventPublished, Title: "Bike Night", CreatedAt: now}},
	})
	if !strings.Contains(busy, "1 change waiting") || !strings.Contains(busy, "Bike Night published") {
		t.Errorf("pending change not rendered: %s", busy)
	}
	if !strings.Contains(FormatSiteStatus(SiteStatus{}), "not been rebuilt") {
		t.Error("never-built state not reported")
	}
}
