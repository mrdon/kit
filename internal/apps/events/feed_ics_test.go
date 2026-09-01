package events

import (
	"strings"
	"testing"
	"time"
)

// ics builds one tier and returns it as raw text. Assertions are made against
// the serialised output rather than an intermediate struct on purpose: what a
// subscriber's calendar parses is the text, and escaping and line folding only
// exist there.
func (sf *syncFixture) ics(t *testing.T, tier Tier) string {
	t.Helper()
	body, err := sf.svc.BuildICS(sf.ctx, sf.tenant.ID, tier, "Test Brewing")
	if err != nil {
		t.Fatalf("BuildICS(%s): %v", tier, err)
	}
	return body
}

// unfold reverses RFC 5545's 75-octet line folding so a test can look for a
// whole property value. Real parsers do this first too; without it every
// assertion on a long line is a false negative.
func unfold(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n ", ""), "\r\n\t", "")
}

func (sf *syncFixture) publishedPublic(t *testing.T, p CreateParams) *Event {
	t.Helper()
	if p.Visibility == "" {
		p.Visibility = VisibilityPublic
	}
	return sf.publish(t, sf.create(t, p))
}

// The tier matrix, including the case most likely to be got wrong: offsite is
// an independent filter, so a FEATURED offsite event is still absent from the
// two narrow feeds.
func TestBuildICS_TierFiltering(t *testing.T) {
	sf := newSyncFixture(t)

	background := ProminenceBackground
	featured := ProminenceFeatured

	sf.publishedPublic(t, CreateParams{Title: "Happy Hour", Prominence: &background})
	sf.publishedPublic(t, CreateParams{Title: "Bike Night"})
	sf.publishedPublic(t, CreateParams{Title: "Oktoberfest", Prominence: &featured})
	sf.publishedPublic(t, CreateParams{
		Title:      "GABF Pour",
		Venue:      VenueOffsite,
		Prominence: &featured,
	})

	cases := []struct {
		tier Tier
		want []string
		gone []string
	}{
		{
			tier: TierAll,
			want: []string{"Happy Hour", "Bike Night", "Oktoberfest", "GABF Pour"},
		},
		{
			tier: TierHighlights,
			want: []string{"Bike Night", "Oktoberfest"},
			// Happy Hour is a standing offer; GABF Pour is someone else's
			// event even though it is featured.
			gone: []string{"Happy Hour", "GABF Pour"},
		},
		{
			tier: TierFeatured,
			want: []string{"Oktoberfest"},
			gone: []string{"Happy Hour", "Bike Night", "GABF Pour"},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.tier), func(t *testing.T) {
			got := unfold(sf.ics(t, tc.tier))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("tier %s: missing %q", tc.tier, w)
				}
			}
			for _, g := range tc.gone {
				if strings.Contains(got, g) {
					t.Errorf("tier %s: leaked %q", tc.tier, g)
				}
			}
		})
	}
}

// The same gate the JSON feed has. This is the predicate that decides what
// leaves the building, so both serialisers are tested against it separately
// rather than one being trusted to imply the other.
func TestBuildICS_ExcludesEverythingButPublishedPublic(t *testing.T) {
	sf := newSyncFixture(t)

	sf.publishedPublic(t, CreateParams{Title: "Trivia Night"})
	sf.publish(t, sf.create(t, CreateParams{Title: "Sarahs 40th", Visibility: VisibilityPrivate}))
	sf.create(t, CreateParams{Title: "Unfinished Draft", Visibility: VisibilityPublic})

	got := unfold(sf.ics(t, TierAll))
	if !strings.Contains(got, "Trivia Night") {
		t.Error("published public event missing from the feed")
	}
	for _, absent := range []string{"Sarahs 40th", "Unfinished Draft"} {
		if strings.Contains(got, absent) {
			t.Errorf("%q reached the ICS feed", absent)
		}
	}
}

// prep_notes is the internal bartender brief. It reaches the Google Calendar
// entry staff read and must never reach a public feed -- and unlike the JSON
// feed, an ICS is subscribed to by parties outside the business entirely.
func TestBuildICS_NeverLeaksInternalFields(t *testing.T) {
	sf := newSyncFixture(t)

	attendance := 250
	sf.publishedPublic(t, CreateParams{
		Title:              "Oktoberfest",
		PrepNotes:          "SECRETBRIEF pull the good glassware, Dave opens",
		ExpectedAttendance: &attendance,
		SpaceImpact:        SpaceImpactPartial,
	})

	for _, tier := range []Tier{TierAll, TierHighlights, TierFeatured} {
		got := unfold(sf.ics(t, tier))
		if strings.Contains(got, "SECRETBRIEF") {
			t.Errorf("tier %s: prep_notes leaked into the ICS feed", tier)
		}
	}
}

// A weekly series must ride through as a rule, not as materialised instances,
// and its DTSTART must carry a TZID. Written as UTC, a 7pm Tuesday drifts to
// 6pm for every subscriber the moment the clocks change.
func TestBuildICS_RecurringCarriesRuleAndZone(t *testing.T) {
	sf := newSyncFixture(t)

	sf.publishedPublic(t, CreateParams{
		Title:    "Trivia Night",
		StartsAt: "2026-09-15 19:00",
		RRule:    "FREQ=WEEKLY;BYDAY=TU",
		Timezone: "America/Denver",
	})

	got := unfold(sf.ics(t, TierHighlights))

	if !strings.Contains(got, "RRULE:FREQ=WEEKLY;BYDAY=TU") {
		t.Errorf("recurrence rule missing:\n%s", got)
	}
	if !strings.Contains(got, "DTSTART;TZID=America/Denver:20260915T190000") {
		t.Errorf("DTSTART is not local-with-TZID:\n%s", got)
	}
	// One VEVENT for the series, not one per Tuesday.
	if n := strings.Count(got, "BEGIN:VEVENT"); n != 1 {
		t.Errorf("expected 1 VEVENT for a weekly series, got %d", n)
	}
}

// Extra dates a rule cannot express are additive to it, so they belong in
// RDATE alongside RRULE rather than replacing it.
func TestBuildICS_RepeatDatesBecomeRdate(t *testing.T) {
	sf := newSyncFixture(t)

	sf.publishedPublic(t, CreateParams{
		Title:       "Live Music",
		StartsAt:    "2026-09-15 19:00",
		RepeatDates: []string{"2026-09-22 19:00", "2026-10-06 19:00"},
		Timezone:    "America/Denver",
	})

	got := unfold(sf.ics(t, TierHighlights))
	if !strings.Contains(got, "RDATE;TZID=America/Denver:") {
		t.Errorf("RDATE missing or unzoned:\n%s", got)
	}
	if !strings.Contains(got, "20260922T190000") || !strings.Contains(got, "20261006T190000") {
		t.Errorf("RDATE does not carry both extra dates:\n%s", got)
	}
}

// An all-day event is a DATE value, not a midnight timestamp: rendered in a
// zone it shows up on the wrong day for anyone reading from another one.
func TestBuildICS_AllDayUsesDateValues(t *testing.T) {
	sf := newSyncFixture(t)

	sf.publishedPublic(t, CreateParams{
		Title:    "Anniversary Weekend",
		StartsAt: "2026-09-19 00:00",
		AllDay:   true,
	})

	got := unfold(sf.ics(t, TierHighlights))
	if !strings.Contains(got, "DTSTART;VALUE=DATE:20260919") {
		t.Errorf("all-day DTSTART is not a DATE value:\n%s", got)
	}
}

// The escaping cases. RFC 5545 gives comma, semicolon and backslash meaning
// inside a TEXT value and requires newlines as a literal \n, so an unescaped
// title is not a cosmetic problem -- it terminates the property early and the
// entry is dropped or mangled by the subscriber's parser.
func TestBuildICS_EscapesTextValues(t *testing.T) {
	sf := newSyncFixture(t)

	sf.publishedPublic(t, CreateParams{
		Title:       "Bikes, Brats; and Beer",
		Description: "Line one\nLine two",
	})

	raw := sf.ics(t, TierHighlights)
	got := unfold(raw)

	if !strings.Contains(got, `SUMMARY:Bikes\, Brats\; and Beer`) {
		t.Errorf("comma/semicolon not escaped in SUMMARY:\n%s", got)
	}
	if strings.Contains(got, "DESCRIPTION:Line one\r\nLine two") {
		t.Errorf("raw newline survived into DESCRIPTION, which truncates it:\n%s", got)
	}
	if !strings.Contains(got, `Line one\nLine two`) {
		t.Errorf("newline not escaped as \\n in DESCRIPTION:\n%s", got)
	}
}

// A subscription is re-fetched forever, so identity has to be stable across
// runs AND across tiers. A UID that changed would grow a duplicate in every
// subscriber's calendar instead of updating the entry they already hold.
func TestBuildICS_UIDIsStableAcrossRunsAndTiers(t *testing.T) {
	sf := newSyncFixture(t)

	featured := ProminenceFeatured
	e := sf.publishedPublic(t, CreateParams{Title: "Oktoberfest", Prominence: &featured})

	want := "UID:" + e.ID.String() + "@kit.events"
	for _, tier := range []Tier{TierAll, TierHighlights, TierFeatured} {
		if got := unfold(sf.ics(t, tier)); !strings.Contains(got, want) {
			t.Errorf("tier %s: expected stable UID %q:\n%s", tier, want, got)
		}
	}
	// Same content twice running must be byte-identical apart from DTSTAMP,
	// or every poll looks like a change.
	first := stripStamps(sf.ics(t, TierAll))
	second := stripStamps(sf.ics(t, TierAll))
	if first != second {
		t.Error("two builds of the same data differ; subscribers would re-sync on every poll")
	}
}

func stripStamps(s string) string {
	out := make([]string, 0, 64)
	for line := range strings.SplitSeq(s, "\r\n") {
		if strings.HasPrefix(line, "DTSTAMP") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\r\n")
}

// The envelope subscribers act on: a name so the subscription is not listed as
// a bare URL, and a refresh hint so clients re-poll about as often as the site
// rebuilds.
func TestBuildICS_CalendarEnvelope(t *testing.T) {
	sf := newSyncFixture(t)
	sf.publishedPublic(t, CreateParams{Title: "Bike Night"})

	got := unfold(sf.ics(t, TierAll))
	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"X-WR-CALNAME:Test Brewing",
		"REFRESH-INTERVAL;VALUE=DURATION:PT24H",
		"END:VCALENDAR",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("envelope missing %q:\n%s", want, got)
		}
	}
}

// RFC 5545 §3.1 requires CRLF. golang-ical defaults to the HOST's line ending,
// so without an explicit option this passes on Windows and ships broken from
// Linux -- and the parsers strict enough to care are third-party calendar
// platforms that reject the feed without telling us.
func TestBuildICS_UsesCRLFLineEndings(t *testing.T) {
	sf := newSyncFixture(t)
	sf.publishedPublic(t, CreateParams{Title: "Bike Night"})

	raw := sf.ics(t, TierAll)
	if !strings.Contains(raw, "\r\n") {
		t.Fatal("no CRLF found; the feed is using bare LF and is not RFC 5545 conformant")
	}
	if bare := strings.Count(raw, "\n") - strings.Count(raw, "\r\n"); bare != 0 {
		t.Errorf("found %d bare LF line endings; every line must terminate CRLF", bare)
	}
}

func TestBuildICS_RejectsUnknownTier(t *testing.T) {
	sf := newSyncFixture(t)
	if _, err := sf.svc.BuildICS(sf.ctx, sf.tenant.ID, Tier("everything"), "Test"); err == nil {
		t.Error("expected an unknown tier to be refused, not silently served")
	}
}

// Guards the nesting property the whole tier design rests on: handing an org a
// narrower feed must only ever remove events, never swap them. If this fails,
// "here is a smaller version of the same feed" stops being true.
func TestBuildICS_TiersAreNestedSupersets(t *testing.T) {
	sf := newSyncFixture(t)

	background := ProminenceBackground
	featured := ProminenceFeatured
	sf.publishedPublic(t, CreateParams{Title: "Happy Hour", Prominence: &background})
	sf.publishedPublic(t, CreateParams{Title: "Bike Night"})
	sf.publishedPublic(t, CreateParams{Title: "Oktoberfest", Prominence: &featured})

	all := uids(sf.ics(t, TierAll))
	highlights := uids(sf.ics(t, TierHighlights))
	featuredUIDs := uids(sf.ics(t, TierFeatured))

	for uid := range featuredUIDs {
		if _, ok := highlights[uid]; !ok {
			t.Errorf("featured UID %s absent from highlights; tiers are not nested", uid)
		}
	}
	for uid := range highlights {
		if _, ok := all[uid]; !ok {
			t.Errorf("highlights UID %s absent from all; tiers are not nested", uid)
		}
	}
}

func uids(body string) map[string]struct{} {
	out := map[string]struct{}{}
	for line := range strings.SplitSeq(unfold(body), "\r\n") {
		if v, ok := strings.CutPrefix(line, "UID:"); ok {
			out[v] = struct{}{}
		}
	}
	return out
}

// Timezone handling is the reason DTSTART is written local-with-TZID, so pin
// the behaviour that motivated it: the wall-clock time must not move across a
// DST boundary.
func TestBuildICS_WallClockSurvivesDST(t *testing.T) {
	sf := newSyncFixture(t)

	// Denver leaves DST on 1 November 2026; a series starting in October runs
	// through it.
	prev := timeNow
	timeNow = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { timeNow = prev })

	sf.publishedPublic(t, CreateParams{
		Title:    "Trivia Night",
		StartsAt: "2026-10-06 19:00",
		RRule:    "FREQ=WEEKLY;BYDAY=TU",
		Timezone: "America/Denver",
	})

	got := unfold(sf.ics(t, TierHighlights))
	// Local time plus a zone, so the client re-resolves 19:00 on each side of
	// the boundary. A UTC DTSTART would pin an offset and shift in November.
	if !strings.Contains(got, "DTSTART;TZID=America/Denver:20261006T190000") {
		t.Errorf("DTSTART must be local-with-TZID to survive DST:\n%s", got)
	}
	if strings.Contains(got, "DTSTART:") {
		t.Errorf("a bare UTC DTSTART would drift across the November change:\n%s", got)
	}
}
