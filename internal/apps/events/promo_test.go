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

// wednesdayTrivia is a long-running weekly series -- StartsAt in the PAST,
// which is what a real standing night looks like. Fixtures that start in the
// future have no past occurrences and quietly hide the interesting cases.
func wednesdayTrivia() Event {
	e := testEvent("Trivia", 0)
	e.StartsAt = time.Date(2026, 6, 3, 19, 0, 0, 0, time.UTC) // a Wednesday
	e.RRule = "FREQ=WEEKLY;BYDAY=WE"
	return e
}

// The interval is a FLOOR, not the due date. Due is the first occurrence at
// least that far after the last post, so the reminder lands against a night
// that is actually happening.
func TestPromo_CadenceDueOnFirstOccurrenceAfterTheFloor(t *testing.T) {
	e := wednesdayTrivia()
	c := testChannel("Facebook", ChannelManual, cadence("mention-it", 28))

	lastDone := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC) // a Saturday
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
	// 28-day interval less its 7-day grace lands on Sat 12 Sep; the next quiz
	// is Wed 16 Sep.
	want := time.Date(2026, 9, 16, 19, 0, 0, 0, time.UTC)
	if !got[0].DueAt.Equal(want) {
		t.Errorf("due %v, want the first Wednesday past the floor, %v", got[0].DueAt, want)
	}
	if got[0].Overdue {
		t.Error("a future due date is not overdue")
	}
	if got[0].LastURL == "" {
		t.Error("cadence row must carry the previous post's link, so you can see what you said last time")
	}
}

// Miss a cycle and you owe one, never two. Falls out of anchoring, and this
// pins it so a future change to a calendar-grid schedule gets caught.
func TestPromo_CadenceNeverStacks(t *testing.T) {
	e := wednesdayTrivia()
	c := testChannel("Facebook", ChannelManual, cadence("mention-it", 28))

	// Three intervals ago -- a naive scheduler would owe three posts.
	lastDone := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	state := map[promoKey]promoRecord{
		{e.ID, c.ID, "mention-it"}: {State: PromoDone, LastDoneAt: &lastDone},
	}

	got := buildPromoList([]Event{e}, []Channel{c}, state, promoNow)
	if len(got) != 1 {
		t.Fatalf("expected exactly one outstanding cadence item, got %d: %v", len(got), keysOf(got))
	}
	if !got[0].Overdue {
		t.Error("a quiz night has gone by unpromoted since the last post; that is overdue")
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

// The rarity rule, which is the whole point of aligning a cadence to the
// series rather than to the calendar.
//
// One setting -- "no more often than every 21 days" -- has to produce very
// different behaviour for a weekly quiz and a monthly game night, because a
// rarer thing deserves proportionally more attention per occurrence. Nobody
// should have to classify that by hand.
func TestPromo_CadenceScalesWithHowRareTheSeriesIs(t *testing.T) {
	c := testChannel("Facebook", ChannelManual, cadence("mention", 21))

	// Weekly trivia: 21 days after a post lands mid-week, so the next due date
	// is the following Wednesday -- roughly every third or fourth quiz, not
	// every one.
	trivia := testEvent("Trivia", 2)
	trivia.RRule = "FREQ=WEEKLY;BYDAY=WE"
	trivia.StartsAt = time.Date(2026, 9, 2, 19, 0, 0, 0, time.UTC) // a Wednesday

	// A game night every four weeks is rare enough that every single one is
	// worth a post.
	dnd := testEvent("Battles & Brews", 4)
	dnd.RRule = "FREQ=WEEKLY;INTERVAL=4;BYDAY=TU"
	dnd.StartsAt = time.Date(2026, 9, 1, 19, 0, 0, 0, time.UTC) // a Tuesday

	lastDone := promoNow
	state := func(e Event) map[promoKey]promoRecord {
		return map[promoKey]promoRecord{
			{e.ID, c.ID, "mention"}: {State: PromoDone, LastDoneAt: &lastDone},
		}
	}

	got := buildPromoList([]Event{trivia}, []Channel{c}, state(trivia), promoNow)
	if len(got) != 1 {
		t.Fatalf("expected a trivia cadence item, got %v", keysOf(got))
	}
	triviaGap := got[0].DueAt.Sub(lastDone).Hours() / 24
	if triviaGap < 21 {
		t.Errorf("weekly trivia should wait out the 21-day floor, next due in %.0f days", triviaGap)
	}

	got = buildPromoList([]Event{dnd}, []Channel{c}, state(dnd), promoNow)
	if len(got) != 1 {
		t.Fatalf("expected a game-night cadence item, got %v", keysOf(got))
	}
	dndGap := got[0].DueAt.Sub(lastDone).Hours() / 24
	if dndGap > 29 {
		t.Errorf("a 4-weekly series should be promoted before each night, next due in %.0f days", dndGap)
	}
}

// The due date has to land on a real occurrence, not wherever the arithmetic
// falls. Posting "quiz night!" on a day with no quiz is the failure a floating
// interval produced.
func TestPromo_CadenceLandsOnAnOccurrence(t *testing.T) {
	c := testChannel("Facebook", ChannelManual, cadence("mention", 21))
	trivia := testEvent("Trivia", 1)
	trivia.RRule = "FREQ=WEEKLY;BYDAY=WE"
	trivia.StartsAt = time.Date(2026, 9, 2, 19, 0, 0, 0, time.UTC)

	got := buildPromoList([]Event{trivia}, []Channel{c}, noState(), promoNow)
	if len(got) != 1 {
		t.Fatalf("expected one item, got %v", keysOf(got))
	}
	if wd := got[0].DueAt.Weekday(); wd != time.Wednesday {
		t.Errorf("due on a %s; a weekly Wednesday quiz should be promoted against a Wednesday", wd)
	}
}

// A channel that wants notice gets the post scheduled ahead of the night.
func TestPromo_CadenceRespectsLeadTime(t *testing.T) {
	c := testChannel("Facebook", ChannelManual, cadence("mention", 21))
	c.LeadTimeDays = 3
	trivia := testEvent("Trivia", 1)
	trivia.RRule = "FREQ=WEEKLY;BYDAY=WE"
	trivia.StartsAt = time.Date(2026, 9, 2, 19, 0, 0, 0, time.UTC)

	got := buildPromoList([]Event{trivia}, []Channel{c}, noState(), promoNow)
	if len(got) != 1 {
		t.Fatalf("expected one item, got %v", keysOf(got))
	}
	if wd := got[0].DueAt.Weekday(); wd != time.Sunday {
		t.Errorf("with 3 days' notice a Wednesday quiz should be due on Sunday, got %s", wd)
	}
}

// A series that has run out of dates has nothing left to promote, and must not
// sit on the list forever asking to be posted about.
func TestPromo_CadenceStopsWhenTheSeriesEnds(t *testing.T) {
	c := testChannel("Facebook", ChannelManual, cadence("mention", 21))
	ended := testEvent("Summer Series", -400)
	ended.RRule = "FREQ=WEEKLY;BYDAY=WE;UNTIL=20260101T000000Z"

	if got := buildPromoList([]Event{ended}, []Channel{c}, noState(), promoNow); len(got) != 0 {
		t.Errorf("a finished series should generate nothing, got %v", keysOf(got))
	}
}

// The rule in one line: promote roughly monthly, but never skip a night in a
// series that already runs at about that rhythm.
//
// A strict floor got the second half wrong. A game night every 26 days against
// a 28-day floor never qualifies on its own date, so every other game went
// unpromoted -- the exact opposite of "a rarer thing deserves more attention".
func TestPromo_CadenceDoesNotSkipANearlyMonthlySeries(t *testing.T) {
	c := testChannel("Facebook", ChannelManual, cadence("mention", 28))

	// Every 26 days, just inside the 28-day floor.
	dnd := testEvent("Battles & Brews", 0)
	dnd.StartsAt = time.Date(2026, 6, 2, 19, 0, 0, 0, time.UTC)
	dnd.RDates = []time.Time{
		time.Date(2026, 6, 28, 19, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 24, 19, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 19, 19, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 14, 19, 0, 0, 0, time.UTC),
	}

	lastDone := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC) // posted for the August game
	state := map[promoKey]promoRecord{
		{dnd.ID, c.ID, "mention"}: {State: PromoDone, LastDoneAt: &lastDone},
	}

	got := buildPromoList([]Event{dnd}, []Channel{c}, state, promoNow)
	if len(got) != 1 {
		t.Fatalf("expected one cadence item, got %v", keysOf(got))
	}
	// The very next game, 26 days later -- not the one after it.
	want := time.Date(2026, 9, 14, 19, 0, 0, 0, time.UTC)
	if !got[0].DueAt.Equal(want) {
		t.Errorf("due %v, want the next game night %v — a near-monthly series must not be skipped",
			got[0].DueAt, want)
	}
}

// The same setting on a weekly series still spaces posts about a month apart,
// which is the other half of the rule.
func TestPromo_CadenceOnWeeklyStaysAboutMonthly(t *testing.T) {
	c := testChannel("Facebook", ChannelManual, cadence("mention", 28))
	e := wednesdayTrivia()

	lastDone := time.Date(2026, 8, 5, 20, 0, 0, 0, time.UTC) // a Wednesday quiz
	state := map[promoKey]promoRecord{
		{e.ID, c.ID, "mention"}: {State: PromoDone, LastDoneAt: &lastDone},
	}

	got := buildPromoList([]Event{e}, []Channel{c}, state, promoNow)
	if len(got) != 1 {
		t.Fatalf("expected one cadence item, got %v", keysOf(got))
	}
	gap := got[0].DueAt.Sub(lastDone).Hours() / 24
	if gap < 18 || gap > 32 {
		t.Errorf("weekly trivia promoted %.0f days after the last post; should be about a month", gap)
	}
}
