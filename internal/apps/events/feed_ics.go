package events

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/google/uuid"
)

// The ICS feeds exist for a different consumer than feed.json.
//
// feed.json is fetched once per site build by software we own. An ICS feed is
// SUBSCRIBED to, by calendars we do not own -- a chamber of commerce listing,
// a regional newspaper's events platform, a regular's phone. That difference
// drives every decision in this file:
//
//   - It is generated here rather than in the site's Hugo templates. RFC 5545
//     wants text escaping, 75-octet line folding and UID/DTSTAMP discipline,
//     and golang-ical (already a dependency, for reading, in the calendar app)
//     does all of it correctly. The content adapter next door compares weekdays
//     by NAME because templates have no int cast for time.Weekday; that is the
//     standard of ergonomics we would be writing a serialiser against.
//
//   - Recurring events carry DTSTART in their own named zone with a TZID, never
//     UTC. A weekly 7pm event written as UTC drifts an hour the moment the
//     clocks change, and every subscriber inherits the drift. Same rule the
//     rest of the app follows.
//
//   - There are three nested tiers rather than one feed. What a subscriber
//     wants differs: the chamber wants the anniversary party, not NFL Sundays.
//     Prominence already draws that line, so no new field is involved.

// Tier is how much of the calendar a given subscriber wants. The tiers are
// nested supersets -- featured ⊂ highlights ⊂ all -- so handing an org a
// narrower feed is a URL change, never a reconfiguration.
type Tier string

const (
	// TierAll is everything publicly visible, standing offers included. This
	// is the feed for a regular subscribing on their phone: "happy hour, every
	// Friday" is a perfectly ordinary calendar entry, and NFL Sundays is a
	// reason to turn up.
	TierAll Tier = "all"
	// TierHighlights is real happenings without the standing offers -- a
	// downtown association or trade guild, who want what is on but would not
	// thank us for a permanent pizza deal.
	TierHighlights Tier = "highlights"
	// TierFeatured is the handful of things a town calendar cares about: the
	// anniversary party, Oktoberfest.
	TierFeatured Tier = "featured"
)

// ValidTier gates the query parameter so an unknown tier is a 400 rather than
// quietly serving everything -- the failure mode that matters here is a
// narrow-tier subscriber silently receiving the full calendar.
func ValidTier(t Tier) bool {
	return t == TierAll || t == TierHighlights || t == TierFeatured
}

// includeInTier decides whether one event belongs in a tier.
//
// Two independent filters, and the offsite one is easy to mistake for a
// prominence rule. An offsite event is a festival we are ATTENDING, not
// hosting. The town calendar and the chamber already carry it from the actual
// organiser, so syndicating it duplicates their own listing. It stays in
// TierAll, which is the "everything we are up to" feed. That holds even when
// the event is featured -- pouring at a big festival is worth leading the
// website with and still not ours to submit elsewhere.
func includeInTier(e *Event, tier Tier) bool {
	switch tier {
	case TierAll:
		// Everything, offsite included: this is the "what we are up to" feed.
		return true
	case TierHighlights:
		return e.Venue != VenueOffsite && e.Prominence != ProminenceBackground
	case TierFeatured:
		return e.Venue != VenueOffsite && e.Prominence == ProminenceFeatured
	}
	return false
}

// BuildICS renders one tier as an RFC 5545 calendar.
//
// It deliberately mirrors BuildFeed's selection -- same window, same
// listEvents filter, same selectFeedEvents gate -- rather than inventing its
// own query, so the two feeds can never disagree about what is public. The
// projection below is explicit about which fields it reads; prep_notes,
// expected_attendance and space_impact are absent here for the same reason
// they are absent from feedItem, and TestBuildICS_NeverLeaksInternalFields
// holds that line.
func (s *Service) BuildICS(ctx context.Context, tenantID uuid.UUID, tier Tier, calName string) (string, error) {
	if !ValidTier(tier) {
		return "", fmt.Errorf("unknown tier: %q", tier)
	}
	settings, err := getSettings(ctx, s.pool, tenantID)
	if err != nil {
		return "", err
	}
	now := timeNow()
	until := now.AddDate(0, feedWindowMonths, 0)
	events, err := listEvents(ctx, s.pool, tenantID, ListFilter{
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		From:       &now,
		To:         &until,
		Limit:      500,
	})
	if err != nil {
		return "", err
	}

	entries := selectFeedEvents(events, until)
	cal := newICSCalendar(calName, tier)
	for _, entry := range entries {
		if !includeInTier(entry.event, tier) {
			continue
		}
		addVEvent(cal, entry.event, settings, now)
	}
	// CRLF explicitly. golang-ical defaults its line ending to the HOST's --
	// os_unix.go sets LF, os_windows.go sets CRLF -- so left alone this feed
	// would be RFC 5545 conformant on a developer's Windows box and not on the
	// Linux host it actually ships from. RFC 5545 §3.1 requires CRLF, and the
	// strict parsers are exactly the ones that matter here: a calendar
	// platform ingesting our feed rejects it, and nobody tells us.
	return cal.Serialize(ics.WithNewLineWindows), nil
}

// newICSCalendar sets the calendar-level properties subscribers actually act
// on. X-WR-CALNAME is what Apple and Google display in the sidebar; without it
// a subscription shows up as a URL. REFRESH-INTERVAL asks clients to re-poll
// daily, which matches how often the site rebuilds at worst.
func newICSCalendar(name string, tier Tier) *ics.Calendar {
	cal := ics.NewCalendar()
	cal.SetProductId("-//Kit//Events " + string(tier) + "//EN")
	cal.SetVersion("2.0")
	cal.SetCalscale("GREGORIAN")
	cal.SetMethod(ics.MethodPublish)
	if name != "" {
		cal.SetXWRCalName(name)
		cal.SetName(name)
	}
	cal.SetRefreshInterval("PT24H")
	cal.SetXPublishedTTL("PT24H")
	return cal
}

// icsUID is the subscriber-visible identity of an event, and it must be stable
// forever: change it and every calendar that already holds this event grows a
// duplicate rather than updating the original. The event's own UUID is already
// permanent, so it is the whole key -- the suffix only makes the value a valid
// RFC 5545 globally-unique identifier.
//
// Note it is NOT tier-scoped. The same event promoted from normal to featured
// appears in a narrower feed under the same UID, so a subscriber to both does
// not see it twice.
func icsUID(e *Event) string {
	return e.ID.String() + "@kit.events"
}

// addVEvent projects one event into the calendar.
func addVEvent(cal *ics.Calendar, e *Event, s Settings, stamp time.Time) {
	ev := cal.AddEvent(icsUID(e))
	ev.SetDtStampTime(stamp.UTC())
	ev.SetSummary(e.Title)

	// DTSTART/DTEND in the event's own zone with a TZID, so a weekly 7pm stays
	// 7pm through a DST change. An all-day event is a DATE value instead --
	// midnight-to-midnight in a zone renders as the wrong day for anyone
	// reading it from another one.
	loc := e.Loc()
	if e.AllDay {
		ev.SetAllDayStartAt(e.StartsAt.In(loc))
		ev.SetAllDayEndAt(e.End().In(loc).AddDate(0, 0, 1))
	} else {
		tz := ics.WithTZID(loc.String())
		ev.SetProperty(ics.ComponentPropertyDtStart, e.StartsAt.In(loc).Format(icsLocalLayout), tz)
		ev.SetProperty(ics.ComponentPropertyDtEnd, e.End().In(loc).Format(icsLocalLayout), tz)
	}

	// Recurrence rides through as the rule itself rather than as materialised
	// instances: one VEVENT the subscriber's own client expands. RDATEs are the
	// dates no rule expresses, so they are additive to it, not a substitute.
	if e.RRule != "" {
		ev.SetProperty(ics.ComponentPropertyRrule, e.RRule)
	}
	if len(e.RDates) > 0 {
		ev.SetProperty(ics.ComponentPropertyRdate, icsRDates(e, loc), ics.WithTZID(loc.String()))
	}

	if body := icsDescription(e, s); body != "" {
		ev.SetDescription(body)
	}
	if e.Location != "" {
		ev.SetLocation(e.Location)
	}
	if url := s.CanonicalURL(e.Slug); url != "" {
		ev.SetProperty(ics.ComponentPropertyUrl, url)
	}
	// Everything here is a public happening, never a busy-block on someone's
	// working day, so it should not make a subscriber look unavailable.
	ev.SetTimeTransparency(ics.TransparencyTransparent)
}

// icsLocalLayout is RFC 5545's local (floating-with-TZID) date-time form. The
// library's SetStartAt writes UTC, which is wrong for anything recurring, so
// the timed path formats its own value.
const icsLocalLayout = "20060102T150405"

// icsRDates renders the extra dates as one comma-separated value, sorted so
// the output is stable between runs -- an unstable feed re-downloads as
// "changed" on every poll for no reason.
func icsRDates(e *Event, loc *time.Location) string {
	out := make([]string, 0, len(e.RDates))
	for _, d := range e.RDates {
		out = append(out, d.In(loc).Format(icsLocalLayout))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// icsDescription is the body a subscriber reads inside their calendar app.
//
// Summary then description then the link, skipping whatever is empty. The link
// matters more here than anywhere else: a calendar entry is often the only
// surface someone sees weeks before the event, and it is the one place they
// cannot click through to a website unless we put the URL in.
func icsDescription(e *Event, s Settings) string {
	parts := make([]string, 0, 3)
	if e.Summary != "" {
		parts = append(parts, e.Summary)
	}
	if e.Description != "" && e.Description != e.Summary {
		parts = append(parts, e.Description)
	}
	if url := s.CanonicalURL(e.Slug); url != "" {
		parts = append(parts, url)
	}
	return strings.Join(parts, "\n\n")
}
