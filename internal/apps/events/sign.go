package events

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// The entrance sign: one letter page on the stand by the door, saying what is
// on today to someone who is already inside.
//
// A poster sells a night ahead of time; this greets the people who came. So it
// says "Tonight" rather than a date -- so a sheet printed for one Tuesday can
// go back on the stand the next time the day's lineup repeats -- puts every one of the day's events on one
// page, and leaves the standing offers to a single line at the foot. When a
// poster really is the right thing for the stand, the print route takes a
// poster= instead and prints that, fitted to the page.
//
// Same rows and the same visibility rule as the topper: published, public,
// and here. A festival we pour at elsewhere is not news at our own door.

// signMaxEvents is what one page holds at a size readable from the door.
// More than that and the rest are named in the "Also today" line.
const signMaxEvents = 4

// signEveningHour is when "Today" becomes "Tonight": a day whose events all
// start from this hour on is an evening.
const signEveningHour = 16

// SignEvent is one event as the sign shows it.
type SignEvent struct {
	EventID  uuid.UUID
	Title    string
	Time     string // "3 to 5pm", or "6:30pm" with no end; empty for an all-day event
	Lines    []string
	Poster   *signImage
	posterID *uuid.UUID
	at       time.Time
}

// Sign is everything one entrance sign shows.
type Sign struct {
	Heading string // "Tonight at Gravity Brewing"
	Date    string // "Tuesday, October 27"; for the taproom picker, never printed
	Events  []SignEvent
	Also    []string // standing offers and overflow: "Happy Hour · 3pm"
	Site    string
	Logo    []byte
	Day     time.Time
}

// signDay resolves ?day= to a local midnight: "today" (or empty) or
// "tomorrow", the night-before case.
func signDay(raw string, now time.Time) (time.Time, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "today":
		return dayStart(now), true
	case "tomorrow":
		return dayStart(now).AddDate(0, 0, 1), true
	}
	return time.Time{}, false
}

// listSignEvents reads the day's published, public events.
func (a *App) listSignEvents(ctx context.Context, tenantID uuid.UUID, day time.Time) ([]Event, error) {
	end := day.AddDate(0, 0, 1)
	return listEvents(ctx, a.pool, tenantID, ListFilter{
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		From:       &day,
		To:         &end,
		Limit:      200,
	})
}

// buildSign assembles the sign for the day starting at day, with posters
// loaded.
func (a *App) buildSign(ctx context.Context, tenant *models.Tenant, day time.Time, site string) (Sign, error) {
	events, err := a.listSignEvents(ctx, tenant.ID, day)
	if err != nil {
		return Sign{}, err
	}
	s := composeSign(events, day, tenant.Name)
	s.Site, s.Logo = site, tenant.Icon192
	if store := a.attachments(); store != nil {
		for i := range s.Events {
			if id := s.Events[i].posterID; id != nil {
				s.Events[i].Poster = loadSignImage(ctx, store, tenant.ID, *id, signThumbPx)
			}
		}
	}
	return s, nil
}

// composeSign is the pure half of buildSign: which events, in what order, and
// what each one says.
func composeSign(events []Event, day time.Time, venue string) Sign {
	end := day.AddDate(0, 0, 1)
	type pick struct {
		ev       SignEvent
		billing  topperOccurrence
		standing bool
	}
	var picks []pick
	for i := range events {
		e := &events[i]
		if !e.IsPubliclyVisible() || e.Venue == VenueOffsite {
			continue
		}
		for _, occ := range e.Occurrences(day, end) {
			at := occ.Start.In(day.Location())
			se := SignEvent{EventID: e.ID, Title: strings.TrimSpace(e.Title), posterID: e.HeroAttachmentID, at: at}
			if !e.AllDay {
				se.Time = signTime(e, at, occ.End.In(day.Location()))
			}
			se.Lines = topperBullets(e, bandFacts{Weekday: at.Format("Monday"), Time: topperTime(at), Title: se.Title})
			picks = append(picks, pick{se, topperOccurrence{at: at, prominence: e.Prominence}, e.IsStandingOffer()})
		}
	}
	// Billing decides who gets a block when there are too many; the page
	// then reads in time order, the way the evening runs.
	slices.SortStableFunc(picks, func(a, b pick) int { return compareBilling(a.billing, b.billing) })
	s := Sign{Date: day.Format("Monday, January 2"), Day: day}
	for _, p := range picks {
		if p.standing || len(s.Events) == signMaxEvents {
			s.Also = append(s.Also, supportBullet(topperOccurrence{title: p.ev.Title, timeLabel: p.ev.Time}))
			continue
		}
		s.Events = append(s.Events, p.ev)
	}
	slices.SortStableFunc(s.Events, func(a, b SignEvent) int { return a.at.Compare(b.at) })
	s.Heading = signWhen(s.Events) + " at " + venue
	return s
}

// signTime is the span when the event has a real end -- "3 to 5pm" is the
// whole point of a happy hour -- and just the start otherwise, since End()
// would invent an hour nobody set.
func signTime(e *Event, start, end time.Time) string {
	if e.EndsAt == nil {
		return topperTime(start)
	}
	return timeRange(start, end)
}

// signWhen is "Tonight" when everything on the sign is an evening thing.
func signWhen(evs []SignEvent) string {
	if len(evs) == 0 {
		return "Today"
	}
	for _, e := range evs {
		if e.Time == "" || e.at.Hour() < signEveningHour {
			return "Today"
		}
	}
	return "Tonight"
}
