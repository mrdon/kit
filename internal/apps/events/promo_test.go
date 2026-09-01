package events

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// buildPromoList is pure, so these tests carry no database at all. That is
// deliberate: the list logic is where the design's claims live -- expiry,
// cadence anchoring, series-vs-one-off -- and none of them should need a
// Postgres round trip to pin.

var promoNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func at(days int) time.Time { return promoNow.AddDate(0, 0, days) }

func testEvent(title string, startsInDays int, opts ...func(*Event)) Event {
	e := Event{
		ID:         uuid.New(),
		Title:      title,
		Slug:       title,
		StartsAt:   at(startsInDays),
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		Venue:      VenueOnsite,
		Prominence: ProminenceNormal,
		Timezone:   "America/Denver",
	}
	for _, o := range opts {
		o(&e)
	}
	return e
}

func weekly(e *Event)     { e.RRule = "FREQ=WEEKLY;BYDAY=TU" }
func background(e *Event) { e.Prominence = ProminenceBackground }
func featured(e *Event)   { e.Prominence = ProminenceFeatured }

func testChannel(name string, mode ChannelMode, steps ...Step) Channel {
	return Channel{
		ID:            uuid.New(),
		Name:          name,
		Mode:          mode,
		Steps:         steps,
		MinProminence: ProminenceNormal,
		Active:        true,
	}
}

func oneshot(key string) Step {
	return Step{Key: key, Label: key, Kind: StepOneshot}
}

func drip(key string, offset, expires int) Step {
	return Step{Key: key, Label: key, Kind: StepDrip, OffsetDays: offset, ExpiresAfterDays: expires}
}

func cadence(key string, interval int) Step {
	return Step{Key: key, Label: key, Kind: StepCadence, IntervalDays: interval}
}

func keysOf(items []PromoItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ChannelName + "/" + it.StepKey
	}
	return out
}

func noState() map[promoKey]promoRecord { return map[promoKey]promoRecord{} }

// A drip beat whose window has closed drops off entirely rather than sitting
// on the page in red. This is the anti-guilt-ledger rule and the single most
// load-bearing behaviour in the file.
func TestPromo_ExpiredDripDisappears(t *testing.T) {
	// Event is 3 days out; the "one week out" beat was due 4 days ago and
	// only stays actionable for 2 days.
	e := testEvent("Oktoberfest", 3)
	c := testChannel("Facebook", ChannelManual, drip("remind-1wk", 7, 2))

	got := buildPromoList([]Event{e}, []Channel{c}, noState(), promoNow)
	if len(got) != 0 {
		t.Errorf("expected the lapsed reminder to vanish, got %v", keysOf(got))
	}
}

func TestPromo_DripInsideItsWindowIsListed(t *testing.T) {
	// Event is 6 days out, so the "one week out" beat came due yesterday and
	// has a 2-day window.
	e := testEvent("Oktoberfest", 6)
	c := testChannel("Facebook", ChannelManual, drip("remind-1wk", 7, 2))

	got := buildPromoList([]Event{e}, []Channel{c}, noState(), promoNow)
	if len(got) != 1 {
		t.Fatalf("expected the reminder to be listed, got %v", keysOf(got))
	}
	if !got[0].Overdue {
		t.Error("a beat that came due yesterday should read as overdue")
	}
}

// Every beat is moot once the event has happened, whatever its expiry window.
func TestPromo_DripDropsAfterTheEvent(t *testing.T) {
	e := testEvent("Last Week's Party", -2)
	c := testChannel("Facebook", ChannelManual, drip("announce", 21, 0))

	if got := buildPromoList([]Event{e}, []Channel{c}, noState(), promoNow); len(got) != 0 {
		t.Errorf("expected nothing after the event, got %v", keysOf(got))
	}
}

// The kind rules: you do not run an announce/remind campaign for something
// that happens every Tuesday, and there is no series to periodically remind
// anyone about for a one-off.
func TestPromo_StepKindsMatchEventShape(t *testing.T) {
	c := testChannel("Facebook", ChannelManual,
		oneshot("create-fb-event"),
		drip("announce", 21, 0),
		cadence("mention-it", 28),
	)

	series := testEvent("Trivia", 3, weekly)
	got := keysOf(buildPromoList([]Event{series}, []Channel{c}, noState(), promoNow))
	assertSameSet(t, "series", got, []string{"Facebook/create-fb-event", "Facebook/mention-it"})

	oneOff := testEvent("Oktoberfest", 30)
	got = keysOf(buildPromoList([]Event{oneOff}, []Channel{c}, noState(), promoNow))
	assertSameSet(t, "one-off", got, []string{"Facebook/create-fb-event", "Facebook/announce"})
}

// A cadence is anchored to its last completion, not a calendar grid: posting
// on a Tuesday pushes the next one a full interval from THEN. That is what
// stops everything bunching onto the first of the month.
func TestPromo_CadenceAnchorsToLastCompletion(t *testing.T) {
	e := testEvent("Trivia", 3, weekly)
	c := testChannel("Facebook", ChannelManual, cadence("mention-it", 28))

	lastDone := at(-10)
	state := map[promoKey]promoRecord{
		{e.ID, c.ID, "mention-it"}: {
			State:      PromoDone,
			LastDoneAt: &lastDone,
			LastURL:    "https://fb.example/post/1",
		},
	}

	got := buildPromoList([]Event{e}, []Channel{c}, state, promoNow)
	if len(got) != 1 {
		t.Fatalf("expected one cadence item, got %v", keysOf(got))
	}
	if want := lastDone.AddDate(0, 0, 28); !got[0].DueAt.Equal(want) {
		t.Errorf("due %v, want last-done + interval = %v", got[0].DueAt, want)
	}
	if got[0].Overdue {
		t.Error("done 10 days ago on a 28-day cadence is not yet due")
	}
	if got[0].LastURL == "" {
		t.Error("cadence row must carry the previous post's link, so you can see what you said last time")
	}
}

// Miss a cycle and you owe one, never two. Falls out of anchoring, and this
// pins it so a future change to a calendar-grid schedule gets caught.
func TestPromo_CadenceNeverStacks(t *testing.T) {
	e := testEvent("Trivia", 3, weekly)
	c := testChannel("Facebook", ChannelManual, cadence("mention-it", 28))

	// Three intervals ago -- a naive scheduler would owe three posts.
	lastDone := at(-90)
	state := map[promoKey]promoRecord{
		{e.ID, c.ID, "mention-it"}: {State: PromoDone, LastDoneAt: &lastDone},
	}

	got := buildPromoList([]Event{e}, []Channel{c}, state, promoNow)
	if len(got) != 1 {
		t.Fatalf("expected exactly one outstanding cadence item, got %d: %v", len(got), keysOf(got))
	}
	if !got[0].Overdue {
		t.Error("90 days since the last post on a 28-day cadence should be overdue")
	}
}

// A subscribed channel pulls the feed itself, so it generates no work at all.
// Flipping mode is the only change required -- no migration, no cleanup.
func TestPromo_SubscribedChannelGeneratesNothing(t *testing.T) {
	e := testEvent("Oktoberfest", 30, featured)
	c := testChannel("Chamber", ChannelManual, oneshot("submit"))

	if got := buildPromoList([]Event{e}, []Channel{c}, noState(), promoNow); len(got) != 1 {
		t.Fatalf("manual chamber should want a submission, got %v", keysOf(got))
	}

	c.Mode = ChannelSubscribed
	if got := buildPromoList([]Event{e}, []Channel{c}, noState(), promoNow); len(got) != 0 {
		t.Errorf("a subscribed channel must generate no work, got %v", keysOf(got))
	}
}

// Prominence gates both the channel and the individual step, which is what
// keeps happy hour off the chamber's calendar with no list of exceptions.
func TestPromo_ProminenceGatesChannelAndStep(t *testing.T) {
	chamber := testChannel("Chamber", ChannelManual, oneshot("submit"))
	chamber.MinProminence = ProminenceFeatured

	fb := testChannel("Facebook", ChannelManual,
		Step{Key: "announce", Kind: StepDrip, OffsetDays: 21},
		Step{Key: "remind", Kind: StepDrip, OffsetDays: 7, MinProminence: ProminenceFeatured},
	)
	fb.MinProminence = ProminenceBackground

	channels := []Channel{chamber, fb}

	// Background: below the chamber's floor entirely; Facebook takes it for
	// the announce but not the featured-only reminder.
	happyHour := testEvent("Happy Hour", 25, background)
	got := keysOf(buildPromoList([]Event{happyHour}, channels, noState(), promoNow))
	assertSameSet(t, "background", got, []string{"Facebook/announce"})

	// Featured clears everything.
	okt := testEvent("Oktoberfest", 25, featured)
	got = keysOf(buildPromoList([]Event{okt}, channels, noState(), promoNow))
	assertSameSet(t, "featured", got, []string{"Chamber/submit", "Facebook/announce", "Facebook/remind"})
}

// An offsite event is someone else's — the chamber and the city already carry
// it from the actual organiser, so queueing a submission duplicates their
// listing and wastes the trip. But "come see us at GABF" is exactly what a
// social channel wants, so it is a per-channel choice rather than a global
// rule. Mirrors the ICS tiers, where offsite appears only in `all`.
func TestPromo_OffsiteExcludedUnlessChannelOptsIn(t *testing.T) {
	// Featured on purpose: the exclusion is independent of prominence, so the
	// most tempting event to syndicate is the one that proves the rule.
	gabf := testEvent("GABF Pour", 20, featured)
	gabf.Venue = VenueOffsite

	chamber := testChannel("Chamber", ChannelManual, oneshot("submit"))
	chamber.MinProminence = ProminenceFeatured

	facebook := testChannel("Facebook", ChannelManual, oneshot("post"))
	facebook.IncludeOffsite = true

	got := keysOf(buildPromoList([]Event{gabf}, []Channel{chamber, facebook}, noState(), promoNow))
	assertSameSet(t, "offsite", got, []string{"Facebook/post"})
}

// The gate that matters most: a private booking must never turn into a task
// telling someone to post it to the chamber.
func TestPromo_NeverPromotesNonPublicEvents(t *testing.T) {
	c := testChannel("Chamber", ChannelManual, oneshot("submit"))

	private := testEvent("Sarah's 40th", 20)
	private.Visibility = VisibilityPrivate

	draft := testEvent("Unfinished", 20)
	draft.Status = StatusDraft

	got := buildPromoList([]Event{private, draft}, []Channel{c}, noState(), promoNow)
	if len(got) != 0 {
		t.Errorf("private and draft events must generate no promotion work, got %v", keysOf(got))
	}
}

// Ignoring is per event x channel x step and is final -- it does not come back
// next week, and it does not expire into something else.
func TestPromo_IgnoredStaysGone(t *testing.T) {
	e := testEvent("Oktoberfest", 30, featured)
	c := testChannel("Chamber", ChannelManual, oneshot("submit"))

	state := map[promoKey]promoRecord{
		{e.ID, c.ID, "submit"}: {State: PromoIgnored},
	}
	if got := buildPromoList([]Event{e}, []Channel{c}, state, promoNow); len(got) != 0 {
		t.Errorf("an ignored item must not reappear, got %v", keysOf(got))
	}
}

// Automatability is per step, not per channel: Facebook can auto-post but can
// never auto-create the annual recurring event, so that row stays manual even
// on an automated channel.
func TestPromo_AutomatableIsPerStep(t *testing.T) {
	e := testEvent("Trivia", 3, weekly)
	c := testChannel("Facebook", ChannelAutomated,
		Step{Key: "create-fb-event", Kind: StepOneshot, Automatable: false},
		Step{Key: "mention-it", Kind: StepCadence, IntervalDays: 28, Automatable: true},
	)
	c.Connector = "meta"

	got := buildPromoList([]Event{e}, []Channel{c}, noState(), promoNow)
	manual := map[string]bool{}
	for _, it := range got {
		manual[it.StepKey] = it.Manual
	}
	if !manual["create-fb-event"] {
		t.Error("creating the annual Facebook event has no API and must stay manual")
	}
	if manual["mention-it"] {
		t.Error("an automatable step on an automated channel should not be manual work")
	}
}

// Priority is distance to the SUBMISSION deadline, not to the event. A
// chamber wanting two weeks' notice outranks a Facebook post for an event on
// the same day -- ordering by event date would tie them.
func TestPromo_OrdersByDeadlineNotEventDate(t *testing.T) {
	e := testEvent("Oktoberfest", 16, featured)

	chamber := testChannel("Chamber", ChannelManual, oneshot("submit"))
	chamber.MinProminence = ProminenceFeatured
	chamber.LeadTimeDays = 14 // due in 2 days

	fb := testChannel("Facebook", ChannelManual, drip("announce", 7, 0)) // due in 9 days

	got := buildPromoList([]Event{e}, []Channel{chamber, fb}, noState(), promoNow)
	if len(got) != 2 {
		t.Fatalf("expected both items, got %v", keysOf(got))
	}
	if got[0].ChannelName != "Chamber" {
		t.Errorf("the chamber's 14-day lead time makes it more urgent; got order %v", keysOf(got))
	}
}

// Overdue sorts above everything, so the top of the page is what is late.
func TestPromo_OverdueSortsFirst(t *testing.T) {
	late := testEvent("Late One", 20, featured)
	soon := testEvent("Soon One", 40, featured)

	c := testChannel("Chamber", ChannelManual, oneshot("submit"))
	c.MinProminence = ProminenceFeatured
	c.LeadTimeDays = 30 // late: due 10 days ago; soon: due in 10 days

	got := buildPromoList([]Event{soon, late}, []Channel{c}, noState(), promoNow)
	if len(got) != 2 {
		t.Fatalf("expected two items, got %v", keysOf(got))
	}
	if got[0].EventTitle != "Late One" || !got[0].Overdue {
		t.Errorf("overdue work belongs at the top, got %v", keysOf(got))
	}
}

func TestPromo_SummaryCountsOnlyActionable(t *testing.T) {
	e := testEvent("Oktoberfest", 30, featured)
	c := testChannel("Chamber", ChannelManual, oneshot("submit"), oneshot("poster"))
	c.MinProminence = ProminenceFeatured

	state := map[promoKey]promoRecord{
		{e.ID, c.ID, "poster"}: {State: PromoAutoDone},
	}
	got := summarisePromo(buildPromoList([]Event{e}, []Channel{c}, state, promoNow))
	if got.Outstanding != 1 {
		t.Errorf("outstanding = %d, want 1 (a completed item is not work)", got.Outstanding)
	}
}

// A failed automation is actionable work, not a log line. A channel that
// quietly stopped posting is worse than one that was never automated, because
// the checklist has stopped watching it too.
func TestPromo_AutoFailureIsActionable(t *testing.T) {
	e := testEvent("Oktoberfest", 30, featured)
	c := testChannel("Facebook", ChannelAutomated, Step{Key: "announce", Kind: StepDrip, OffsetDays: 21, Automatable: true})
	c.Connector = "meta"
	c.MinProminence = ProminenceFeatured

	state := map[promoKey]promoRecord{
		{e.ID, c.ID, "announce"}: {State: PromoAutoFailed, Note: "token expired"},
	}
	got := buildPromoList([]Event{e}, []Channel{c}, state, promoNow)
	if len(got) != 1 || got[0].State != PromoAutoFailed {
		t.Fatalf("a failed post must surface as work, got %v", got)
	}
	if summarisePromo(got).Outstanding != 1 {
		t.Error("a failed automation should count towards outstanding work")
	}
}

// An automated channel with no connector would silently do nothing. Falling
// back to manual keeps the work visible instead.
func TestPromo_AutomatedWithoutConnectorFallsBackToManual(t *testing.T) {
	c := testChannel("Facebook", ChannelAutomated, oneshot("announce"))
	c.Connector = ""
	normaliseChannel(&c)

	if c.Mode != ChannelManual {
		t.Errorf("mode = %s, want manual: an automated channel with no connector posts nothing", c.Mode)
	}
}

func assertSameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSet := map[string]bool{}
	for _, g := range got {
		gotSet[g] = true
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for w := range wantSet {
		if !gotSet[w] {
			t.Errorf("%s: missing %q (got %v)", label, w, got)
		}
	}
	for g := range gotSet {
		if !wantSet[g] {
			t.Errorf("%s: unexpected %q (got %v)", label, g, got)
		}
	}
}
