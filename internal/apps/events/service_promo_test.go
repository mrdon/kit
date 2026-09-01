package events

import (
	"testing"
	"time"
)

// Database-backed tests for the persistence half. The list LOGIC is covered
// without a database in promo_test.go; what these pin is the round trip and
// the tenant boundary.

func (sf *syncFixture) channel(t *testing.T, c Channel) *Channel {
	t.Helper()
	out, err := sf.svc.SaveChannel(sf.ctx, sf.tenant.ID, c)
	if err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}
	return out
}

func (sf *syncFixture) promoList(t *testing.T) []PromoItem {
	t.Helper()
	items, err := sf.svc.PromoList(sf.ctx, sf.tenant.ID)
	if err != nil {
		t.Fatalf("PromoList: %v", err)
	}
	return items
}

func TestChannel_RoundTripsStepsAsJSON(t *testing.T) {
	sf := newSyncFixture(t)

	saved := sf.channel(t, Channel{
		Name:          "Louisville Chamber of Commerce",
		Mode:          ChannelManual,
		SubmitURL:     "https://chamber.example/submit",
		LeadTimeDays:  14,
		MinProminence: ProminenceFeatured,
		Active:        true,
		Steps: []Step{
			{Key: "submit", Label: "Add to chamber calendar", Kind: StepOneshot},
			{Key: "remind", Label: "Nudge", Kind: StepDrip, OffsetDays: 7, ExpiresAfterDays: 3},
		},
	})

	got, err := getChannel(sf.ctx, sf.pool, sf.tenant.ID, saved.ID)
	if err != nil {
		t.Fatalf("getChannel: %v", err)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(got.Steps))
	}
	if got.Steps[1].OffsetDays != 7 || got.Steps[1].ExpiresAfterDays != 3 {
		t.Errorf("drip timing did not survive the round trip: %+v", got.Steps[1])
	}
	if got.LeadTimeDays != 14 || got.MinProminence != ProminenceFeatured {
		t.Errorf("channel config did not round trip: %+v", got)
	}
}

// The end-to-end shape of the Monday page: an event, a channel, one
// outstanding item, then marking it done removes it from the outstanding
// count without deleting the record.
func TestPromoList_MarkDoneRemovesItFromOutstanding(t *testing.T) {
	sf := newSyncFixture(t)

	featured := ProminenceFeatured
	e := sf.publishedPublic(t, CreateParams{
		Title:      "Oktoberfest",
		StartsAt:   time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04"),
		Prominence: &featured,
	})
	c := sf.channel(t, Channel{
		Name:          "Chamber",
		Mode:          ChannelManual,
		MinProminence: ProminenceFeatured,
		Active:        true,
		Steps:         []Step{{Key: "submit", Label: "Submit", Kind: StepOneshot}},
	})

	before := sf.promoList(t)
	if len(before) != 1 || before[0].State != PromoTodo {
		t.Fatalf("expected one outstanding item, got %+v", before)
	}

	if err := sf.svc.MarkPromo(sf.ctx, sf.tenant.ID, e.ID, c.ID, "submit", PromoDone, "https://chamber.example/e/1", "", nil); err != nil {
		t.Fatalf("MarkPromo: %v", err)
	}

	after := sf.promoList(t)
	if len(after) != 1 {
		t.Fatalf("the item should still be listed as done, got %+v", after)
	}
	if after[0].State != PromoDone {
		t.Errorf("state = %s, want done", after[0].State)
	}
	if got := summarisePromo(after).Outstanding; got != 0 {
		t.Errorf("outstanding = %d, want 0", got)
	}
}

// Un-ticking deletes the row rather than storing a todo, because a to-do is
// the absence of state. If this ever stores instead, the computed list stops
// being the only thing that decides whether work applies.
func TestPromoList_UntickDeletesTheRow(t *testing.T) {
	sf := newSyncFixture(t)

	featured := ProminenceFeatured
	e := sf.publishedPublic(t, CreateParams{
		Title:      "Oktoberfest",
		StartsAt:   time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04"),
		Prominence: &featured,
	})
	c := sf.channel(t, Channel{
		Name: "Chamber", Mode: ChannelManual, MinProminence: ProminenceFeatured, Active: true,
		Steps: []Step{{Key: "submit", Kind: StepOneshot}},
	})

	if err := sf.svc.MarkPromo(sf.ctx, sf.tenant.ID, e.ID, c.ID, "submit", PromoDone, "", "", nil); err != nil {
		t.Fatalf("MarkPromo(done): %v", err)
	}
	if err := sf.svc.MarkPromo(sf.ctx, sf.tenant.ID, e.ID, c.ID, "submit", PromoTodo, "", "", nil); err != nil {
		t.Fatalf("MarkPromo(todo): %v", err)
	}

	var n int
	if err := sf.pool.QueryRow(sf.ctx,
		`SELECT count(*) FROM app_event_promos WHERE tenant_id = $1`, sf.tenant.ID).Scan(&n); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if n != 0 {
		t.Errorf("expected the row to be deleted, found %d", n)
	}
	if got := sf.promoList(t); len(got) != 1 || got[0].State != PromoTodo {
		t.Errorf("item should be outstanding again, got %+v", got)
	}
}

// Flipping a channel to subscribed retires its work with no migration and no
// cleanup -- the whole payoff of computing the list. Historical rows survive.
func TestPromoList_SubscribingRetiresWorkButKeepsHistory(t *testing.T) {
	sf := newSyncFixture(t)

	featured := ProminenceFeatured
	e := sf.publishedPublic(t, CreateParams{
		Title:      "Oktoberfest",
		StartsAt:   time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04"),
		Prominence: &featured,
	})
	c := sf.channel(t, Channel{
		Name: "Chamber", Mode: ChannelManual, MinProminence: ProminenceFeatured, Active: true,
		Steps: []Step{{Key: "submit", Kind: StepOneshot}},
	})
	if err := sf.svc.MarkPromo(sf.ctx, sf.tenant.ID, e.ID, c.ID, "submit", PromoDone, "https://chamber.example/e/1", "", nil); err != nil {
		t.Fatalf("MarkPromo: %v", err)
	}

	c.Mode = ChannelSubscribed
	c.FeedTier = TierFeatured
	if _, err := sf.svc.SaveChannel(sf.ctx, sf.tenant.ID, *c); err != nil {
		t.Fatalf("SaveChannel(subscribed): %v", err)
	}

	if got := sf.promoList(t); len(got) != 0 {
		t.Errorf("a subscribed channel should generate nothing, got %+v", got)
	}
	var n int
	if err := sf.pool.QueryRow(sf.ctx,
		`SELECT count(*) FROM app_event_promos WHERE tenant_id = $1`, sf.tenant.ID).Scan(&n); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if n != 1 {
		t.Errorf("history should survive the mode flip, found %d rows", n)
	}
}

// The IDs come from a browser, so a cross-tenant write must be refused rather
// than attaching one workspace's promotion record to another's event.
func TestMarkPromo_RefusesForeignIDs(t *testing.T) {
	sf := newSyncFixture(t)
	other := newSyncFixture(t)

	featured := ProminenceFeatured
	foreign := other.publishedPublic(t, CreateParams{
		Title:      "Someone Else's Party",
		StartsAt:   time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04"),
		Prominence: &featured,
	})
	c := sf.channel(t, Channel{
		Name: "Chamber", Mode: ChannelManual, Active: true,
		Steps: []Step{{Key: "submit", Kind: StepOneshot}},
	})

	if err := sf.svc.MarkPromo(sf.ctx, sf.tenant.ID, foreign.ID, c.ID, "submit", PromoDone, "", "", nil); err == nil {
		t.Error("expected a foreign event ID to be refused")
	}
}

// A subscribed channel is the one mode that fails silently, so the row that
// records when someone last confirmed it must survive a round trip.
func TestChannel_SubscribedTracksVerification(t *testing.T) {
	sf := newSyncFixture(t)

	when := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	saved := sf.channel(t, Channel{
		Name: "Chamber", Mode: ChannelSubscribed, FeedTier: TierFeatured,
		VerifiedAt: &when, Active: true,
	})
	if saved.VerifiedAt == nil || !saved.VerifiedAt.UTC().Truncate(time.Second).Equal(when) {
		t.Errorf("verified_at did not round trip: %+v", saved.VerifiedAt)
	}
	if saved.FeedTier != TierFeatured {
		t.Errorf("feed tier = %q, want featured", saved.FeedTier)
	}
}

// Switching away from subscribed clears the feed-tier and verification, so a
// channel cannot claim to be verified as pulling a feed it no longer uses.
func TestChannel_LeavingSubscribedClearsFeedFields(t *testing.T) {
	sf := newSyncFixture(t)

	when := time.Now().UTC()
	c := sf.channel(t, Channel{
		Name: "Chamber", Mode: ChannelSubscribed, FeedTier: TierFeatured,
		VerifiedAt: &when, Active: true,
	})

	c.Mode = ChannelManual
	got, err := sf.svc.SaveChannel(sf.ctx, sf.tenant.ID, *c)
	if err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}
	if got.FeedTier != "" || got.VerifiedAt != nil {
		t.Errorf("feed fields should be cleared when leaving subscribed: tier=%q verified=%v", got.FeedTier, got.VerifiedAt)
	}
}
