package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

type fixture struct {
	pool   *pgxpool.Pool
	svc    *Service
	tenant *models.Tenant
	ctx    context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_ev_test_" + uuid.NewString()
	slug := models.SanitizeSlug("ev-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "ev-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	return &fixture{pool: pool, svc: NewService(pool), tenant: tenant, ctx: ctx}
}

func (f *fixture) create(t *testing.T, p CreateParams) *Event {
	t.Helper()
	if p.Title == "" {
		p.Title = "Test Event"
	}
	if p.StartsAt == "" {
		p.StartsAt = "2026-09-15 19:00"
	}
	e, err := f.svc.Create(f.ctx, f.tenant.ID, p)
	if err != nil {
		t.Fatalf("Create(%+v): %v", p, err)
	}
	return e
}

// Default-deny, verified end to end rather than only on the struct: an event
// created without an explicit visibility must not be publicly visible.
func TestCreateDefaultsToPrivateDraft(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{Title: "Ambiguous Event"})

	if e.Status != StatusDraft {
		t.Errorf("status = %q, want draft", e.Status)
	}
	if e.Visibility != VisibilityPrivate {
		t.Errorf("visibility = %q, want private", e.Visibility)
	}
	if e.IsPubliclyVisible() {
		t.Fatal("a freshly created event is publicly visible; default-deny is broken")
	}
	if e.Slug != "ambiguous-event" {
		t.Errorf("slug = %q", e.Slug)
	}
}

// Publishing settles an event without exposing it. This is the distinction the
// whole app hangs on, so it is asserted directly.
func TestPublishDoesNotMakePrivateEventPublic(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{Title: "Sarah's 40th", Visibility: VisibilityPrivate})

	res, err := f.svc.Publish(f.ctx, f.tenant.ID, e.ID)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.Event.Status != StatusPublished {
		t.Errorf("status = %q, want published", res.Event.Status)
	}
	if res.Event.IsPubliclyVisible() {
		t.Fatal("publishing a private booking exposed it publicly")
	}
}

func TestPublishPublicEventIsVisible(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{Title: "Trivia Night", Visibility: VisibilityPublic})
	res, err := f.svc.Publish(f.ctx, f.tenant.ID, e.ID)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !res.Event.IsPubliclyVisible() {
		t.Fatal("a published public event is not publicly visible")
	}
}

// The food-partner default encodes "a crowd shows up at the taproom".
func TestNotifyFoodPartnerDefault(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name  string
		vis   Visibility
		venue Venue
		want  bool
	}{
		{"public onsite", VisibilityPublic, VenueOnsite, true},
		{"private onsite", VisibilityPrivate, VenueOnsite, false},
		{"public offsite", VisibilityPublic, VenueOffsite, false},
		{"private offsite", VisibilityPrivate, VenueOffsite, false},
	}
	for _, c := range cases {
		e := f.create(t, CreateParams{Title: "Ev " + c.name, Visibility: c.vis, Venue: c.venue})
		if e.NotifyFoodPartner != c.want {
			t.Errorf("%s: notify = %v, want %v", c.name, e.NotifyFoodPartner, c.want)
		}
	}
	// ...and it stays overridable, because a private party may want the truck.
	no := false
	e := f.create(t, CreateParams{Title: "Override", Visibility: VisibilityPublic, NotifyFoodPartner: &no})
	if e.NotifyFoodPartner {
		t.Error("explicit false was overridden by the default")
	}
}

func TestSlugCollisionsGetSuffixed(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, CreateParams{Title: "Trivia Night"})
	b := f.create(t, CreateParams{Title: "Trivia Night"})
	if a.Slug != "trivia-night" || b.Slug != "trivia-night-2" {
		t.Fatalf("slugs = %q, %q; want trivia-night and trivia-night-2", a.Slug, b.Slug)
	}
}

// A published slug is a live public URL that may already be in a social post.
func TestSlugFrozenAfterPublish(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{Title: "Beer Release"})

	newSlug := "renamed-while-draft"
	if _, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{Slug: &newSlug}); err != nil {
		t.Fatalf("renaming a draft should be allowed: %v", err)
	}
	if _, err := f.svc.Publish(f.ctx, f.tenant.ID, e.ID); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	later := "renamed-after-publish"
	_, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{Slug: &later})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("renaming a published event should be refused, got %v", err)
	}
}

// Cancelled rows keep their slug so a new event cannot inherit a URL that
// already points at different content in someone's feed.
func TestCancelledSlugIsNotRecycled(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{Title: "Cancelled Thing"})
	if _, err := f.svc.Cancel(f.ctx, f.tenant.ID, e.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	again := f.create(t, CreateParams{Title: "Cancelled Thing"})
	if again.Slug == e.Slug {
		t.Fatalf("a cancelled event's slug %q was recycled", e.Slug)
	}
}

// Update is a partial patch: the console form and the chat agent edit the same
// event, and each must only touch what it was given.
func TestUpdateIsPartialPatch(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{
		Title: "Original", Summary: "keep me", Description: "keep me too",
		Location: "Back room",
	})
	newTitle := "Changed"
	got, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Title != "Changed" {
		t.Errorf("title not applied: %q", got.Title)
	}
	if got.Summary != "keep me" || got.Description != "keep me too" || got.Location != "Back room" {
		t.Errorf("untouched fields were clobbered: %+v", got)
	}
}

// Changing the zone keeps the wall clock: a 7pm event stays at 7pm. Preserving
// the instant instead would shift a whole recurring series by an hour.
func TestTimezoneChangePreservesWallClock(t *testing.T) {
	f := newFixture(t)
	e := f.create(t, CreateParams{
		Title: "Trivia", StartsAt: "2026-09-15 19:00", Timezone: "America/Denver",
	})
	if h := e.StartsAt.In(e.Loc()).Hour(); h != 19 {
		t.Fatalf("setup: hour = %d, want 19", h)
	}
	chicago := "America/Chicago"
	got, err := f.svc.Update(f.ctx, f.tenant.ID, e.ID, UpdateParams{Timezone: &chicago})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if h := got.StartsAt.In(got.Loc()).Hour(); h != 19 {
		t.Errorf("after moving zone, hour = %d, want 19 (wall clock should be preserved)", h)
	}
	if got.StartsAt.Equal(e.StartsAt) {
		t.Error("the instant did not move; the wall clock was not preserved")
	}
}

func TestCreateRejectsUnsupportedRecurrence(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(f.ctx, f.tenant.ID, CreateParams{
		Title: "Monthly", StartsAt: "2026-09-15 19:00", RRule: "FREQ=MONTHLY;BYSETPOS=-1",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a rule the expander cannot read must be refused on write, got %v", err)
	}
}

// RFC 5545 would treat DTSTART as an occurrence even when BYDAY excludes it,
// leaving Kit and Google disagreeing about a stray first instance.
func TestCreateRejectsStartWeekdayNotInRule(t *testing.T) {
	f := newFixture(t)
	// 2026-09-14 is a Monday; the rule says Tuesdays.
	_, err := f.svc.Create(f.ctx, f.tenant.ID, CreateParams{
		Title: "Mismatch", StartsAt: "2026-09-14 19:00", RRule: "FREQ=WEEKLY;BYDAY=TU",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("start weekday outside the repeat rule must be refused, got %v", err)
	}
}

func TestCreateRejectsOffsiteWithSpaceImpact(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(f.ctx, f.tenant.ID, CreateParams{
		Title: "Fest", StartsAt: "2026-09-15 19:00",
		Venue: VenueOffsite, SpaceImpact: SpaceImpactPartial,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("offsite events cannot reserve taproom space, got %v", err)
	}
}

func TestCreateRejectsRecurringAllDay(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(f.ctx, f.tenant.ID, CreateParams{
		Title: "AllDay", StartsAt: "2026-09-15", AllDay: true, RRule: "FREQ=WEEKLY",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("recurring all-day is out of scope and must be refused, got %v", err)
	}
}

// A weekly series' stored start is its FIRST occurrence, which may be years
// past. A naive date lower bound would drop it from every listing forever.
func TestListKeepsRecurringEventsWithPastStart(t *testing.T) {
	f := newFixture(t)
	long := f.create(t, CreateParams{
		Title: "Long Running Trivia", StartsAt: "2024-01-02 19:00", RRule: "FREQ=WEEKLY;BYDAY=TU",
	})
	f.create(t, CreateParams{Title: "Old One Off", StartsAt: "2024-01-03 19:00"})

	from := time.Now()
	list, err := f.svc.List(f.ctx, f.tenant.ID, ListFilter{From: &from})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawRecurring, sawOneOff bool
	for _, e := range list {
		switch e.ID {
		case long.ID:
			sawRecurring = true
		default:
			if e.Title == "Old One Off" {
				sawOneOff = true
			}
		}
	}
	if !sawRecurring {
		t.Error("a weekly series with a past start was dropped from the listing")
	}
	if sawOneOff {
		t.Error("a genuinely past one-off event was returned")
	}
}

func TestTenantIsolation(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	e := a.create(t, CreateParams{Title: "Tenant A Event"})

	if _, err := b.svc.Get(b.ctx, b.tenant.ID, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant could read the event: %v", err)
	}
	newTitle := "hijacked"
	if _, err := b.svc.Update(b.ctx, b.tenant.ID, e.ID, UpdateParams{Title: &newTitle}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant could write the event: %v", err)
	}
}

func TestSaveSettingsMintsFeedToken(t *testing.T) {
	f := newFixture(t)
	s, err := f.svc.SaveSettings(f.ctx, Settings{TenantID: f.tenant.ID, CalendarID: "cal@example.com"})
	if err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if s.FeedToken == "" {
		t.Fatal("no feed token was minted")
	}
	if s.Timezone != DefaultTimezone {
		t.Errorf("timezone = %q, want the default", s.Timezone)
	}
	again, err := f.svc.SaveSettings(f.ctx, s)
	if err != nil {
		t.Fatalf("SaveSettings (second): %v", err)
	}
	if again.FeedToken != s.FeedToken {
		t.Error("the feed token was rotated on an unrelated save")
	}
}

func TestSettingsCanonicalURL(t *testing.T) {
	s := Settings{PublicURLTemplate: "https://example.com/events/{slug}"}
	if got := s.CanonicalURL("trivia-night"); got != "https://example.com/events/trivia-night" {
		t.Errorf("CanonicalURL = %q", got)
	}
	if got := (Settings{}).CanonicalURL("x"); got != "" {
		t.Errorf("with no template, CanonicalURL should be empty, got %q", got)
	}
}

// A cancelled event is not something anyone is planning around, so the default
// upcoming view leaves it out even though its date is still ahead. It stays
// reachable through the same toggle that reveals past events -- reopening one
// means finding it first.
func TestListExcludesCancelledWhenAsked(t *testing.T) {
	f := newFixture(t)
	future := time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04")
	live := f.create(t, CreateParams{Title: "Still On", StartsAt: future})
	off := f.create(t, CreateParams{Title: "Called Off", StartsAt: future})
	if _, err := f.svc.Cancel(f.ctx, f.tenant.ID, off.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	from := time.Now()
	list, err := f.svc.List(f.ctx, f.tenant.ID, ListFilter{From: &from, ExcludeCancelled: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawLive bool
	for _, e := range list {
		if e.ID == off.ID {
			t.Error("a cancelled event leaked into the upcoming list")
		}
		if e.ID == live.ID {
			sawLive = true
		}
	}
	if !sawLive {
		t.Error("the filter took a live event with it")
	}

	// Without the flag the archive view still shows it, or it could never be
	// reopened.
	all, err := f.svc.List(f.ctx, f.tenant.ID, ListFilter{From: &from})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawCancelled bool
	for _, e := range all {
		if e.ID == off.ID {
			sawCancelled = true
		}
	}
	if !sawCancelled {
		t.Error("the cancelled event is unreachable — it cannot be reopened")
	}
}

// The list is read to find out what is coming up next, and it shows each
// event's next date -- so that is what it must be ordered by. Ordering by
// starts_at instead floats an established series to the top, because its stored
// start is its first occurrence and may be years old.
func TestListOrdersByNextOccurrence(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	// A monthly series that began well before today, whose next date lands
	// three weeks out -- after the one-off below.
	old := now.AddDate(0, -6, 0)
	series := f.create(t, CreateParams{
		Title:    "Battles and Brews",
		StartsAt: old.AddDate(0, 0, 21).Format("2006-01-02") + " 19:00",
		RRule:    "FREQ=MONTHLY",
	})
	soon := f.create(t, CreateParams{
		Title:    "Next Week",
		StartsAt: now.AddDate(0, 0, 7).Format("2006-01-02") + " 19:00",
	})

	from := now
	list, err := f.svc.List(f.ctx, f.tenant.ID, ListFilter{From: &from})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var order []uuid.UUID
	for _, e := range list {
		if e.ID == series.ID || e.ID == soon.ID {
			order = append(order, e.ID)
		}
	}
	if len(order) != 2 {
		t.Fatalf("expected both events in the listing, got %d", len(order))
	}
	if order[0] != soon.ID {
		t.Error("a series whose next date is weeks away sorted above an event happening sooner")
	}
}
