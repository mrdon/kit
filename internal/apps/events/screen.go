package events

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// The events screen: a looping slideshow for a TV on the taproom wall.
//
// It is the third view of the same rows, after the website feed and the table
// topper, and it borrows from both rather than inventing a fourth idea of what
// is on. The week slide IS the topper's week -- one band per day, the same
// billing, the same trimmed copy -- so the card on the table and the screen
// above it can never disagree about Wednesday.
//
// The visibility rule is the feed's: only published, public events, decided
// by IsPubliclyVisible. The URL is open, like the menu's, because the screen
// has no credentials to present and everything on it is already on the
// website.

const (
	// screenHorizonDays is how far ahead anything on the screen reaches. The
	// big-events slide is the one that looks furthest, because a featured
	// night is announced well before it is on the week slide.
	screenHorizonDays = 90
	// screenSoonDays bounds the coming-soon list. Past six weeks a date is a
	// save-the-date, and those are what the featured slides are for.
	screenSoonDays = 45
	screenWeekDays = 7

	screenOverviewMax = 6
	screenFeaturedMax = 3
	screenSoonMax     = 7
)

// screenCard is one event as a slide shows it.
type screenCard struct {
	EventID  string
	Title    string
	Summary  string
	Location string
	Offsite  bool
	Featured bool
	Day      string // "SAT"
	Date     string // "Oct 11"
	DayNum   string // "11"
	Month    string // "OCT"
	Long     string // "Saturday, October 11"
	Time     string // "6pm"; empty for all-day
	Relative string // "Tonight", "Tomorrow", "In 15 days"
	PosterID string

	posterID *uuid.UUID
	at       time.Time
	// standing marks a standing offer. Unexported, so it stays out of the
	// version stamp: it only steers selection, and nothing renders it.
	standing bool
}

// screenDay is one row of the week slide: a topper band plus the labels a
// screen needs that a printed card does not.
type screenDay struct {
	Label    string // "Today", "Tomorrow", or "" for a plain weekday
	Day      string // "SAT"
	Date     string // "Sep 27"
	Time     string
	Title    string
	Bullets  []string
	PosterID string

	posterID *uuid.UUID
}

// Screen is everything the slideshow shows. Its JSON form -- posters as ids,
// not bytes -- is what the version stamp hashes, so the fields a slide renders
// must all be exported.
type Screen struct {
	Venue    string
	Site     string
	Today    string // the local date, so the stamp rolls over at midnight
	Overview []screenCard
	Week     []screenDay
	Featured []screenCard
	Soon     []screenCard
}

// Empty reports whether there is nothing at all to show.
func (s *Screen) Empty() bool {
	return len(s.Overview) == 0 && len(s.Week) == 0 && len(s.Featured) == 0 && len(s.Soon) == 0
}

// buildScreen assembles the slideshow for right now.
func (a *App) buildScreen(ctx context.Context, tenant *models.Tenant) (*Screen, error) {
	settings, err := getSettings(ctx, a.pool, tenant.ID)
	if err != nil {
		return nil, err
	}
	loc := settings.Loc()
	now := timeNow().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	horizon := today.AddDate(0, 0, screenHorizonDays)

	events, err := listEvents(ctx, a.pool, tenant.ID, ListFilter{
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		From:       &today,
		To:         &horizon,
		Limit:      500,
	})
	if err != nil {
		return nil, fmt.Errorf("listing screen events: %w", err)
	}
	return composeScreen(events, now, tenant.Name, siteHost(settings.PublicURLTemplate)), nil
}

// composeScreen is the pure half of buildScreen, split out so the selection
// rules can be tested without a database.
func composeScreen(events []Event, now time.Time, venue, site string) *Screen {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	cards := nextCards(events, now, today.AddDate(0, 0, screenHorizonDays))

	s := &Screen{Venue: venue, Site: site, Today: today.Format("2006-01-02")}
	s.Week = screenWeek(events, now)
	weekEnd := today.AddDate(0, 0, screenWeekDays)
	soonEnd := today.AddDate(0, 0, screenSoonDays)
	for _, c := range cards {
		if c.Featured && len(s.Featured) < screenFeaturedMax {
			s.Featured = append(s.Featured, c)
		}
		// Standing offers are what the week slide is for. On the overview
		// and the look-ahead they would crowd out the nights people plan
		// around, which is the same call the chamber tier makes.
		if c.standing {
			continue
		}
		if len(s.Overview) < screenOverviewMax {
			s.Overview = append(s.Overview, c)
		}
		if !c.at.Before(weekEnd) && c.at.Before(soonEnd) && len(s.Soon) < screenSoonMax {
			s.Soon = append(s.Soon, c)
		}
	}
	return s
}

// nextCards is one card per event, at the next time it actually happens,
// soonest first.
//
// One card per event, not per occurrence: weekly trivia is one thing that is
// on, and eight copies of it would push everything else off the overview. An
// occurrence that has already finished today does not count, so a morning
// yoga class stops being "Today" once it is over.
func nextCards(events []Event, now, horizon time.Time) []screenCard {
	var out []screenCard
	for i := range events {
		e := &events[i]
		if !e.IsPubliclyVisible() {
			continue
		}
		for _, occ := range e.Occurrences(dayStart(now), horizon) {
			if occ.End.Before(now) {
				continue
			}
			out = append(out, newScreenCard(e, occ.Start.In(now.Location()), now))
			break
		}
	}
	slices.SortStableFunc(out, func(a, b screenCard) int { return a.at.Compare(b.at) })
	return out
}

func newScreenCard(e *Event, at, now time.Time) screenCard {
	c := screenCard{
		EventID:  e.ID.String(),
		Title:    strings.TrimSpace(e.Title),
		Summary:  screenSummary(e),
		Location: strings.TrimSpace(e.Location),
		Offsite:  e.Venue == VenueOffsite,
		Featured: e.IsFeatured(),
		Day:      strings.ToUpper(at.Format("Mon")),
		Date:     at.Format("Jan 2"),
		DayNum:   at.Format("2"),
		Month:    strings.ToUpper(at.Format("Jan")),
		Long:     at.Format("Monday, January 2"),
		Relative: relativeDay(at, now, e.AllDay),
		posterID: e.HeroAttachmentID,
		at:       at,
		standing: e.IsStandingOffer(),
	}
	if !e.AllDay {
		c.Time = topperTime(at)
	}
	if e.HeroAttachmentID != nil {
		c.PosterID = e.HeroAttachmentID.String()
	}
	return c
}

// screenWeek is the topper's week, starting today rather than on Sunday: a
// screen on a Friday should be showing the weekend and what follows it, not
// a Monday that has already happened.
func screenWeek(events []Event, now time.Time) []screenDay {
	today := dayStart(now)
	rows := topperRowsSince(events, today, today.AddDate(0, 0, screenWeekDays), today.Location(), now)
	out := make([]screenDay, 0, len(rows))
	for _, r := range rows {
		d := screenDay{
			Day:      r.Day,
			Date:     r.at.Format("Jan 2"),
			Time:     r.Time,
			Title:    r.Title,
			Bullets:  r.Bullets,
			posterID: r.posterID,
		}
		if r.posterID != nil {
			d.PosterID = r.posterID.String()
		}
		// Compared as dates, not as a duration: across a DST change a day is
		// 23 or 25 hours long, and dividing would miscount the one after it.
		switch r.at.Format(time.DateOnly) {
		case today.Format(time.DateOnly):
			d.Label = "Today"
		case today.AddDate(0, 0, 1).Format(time.DateOnly):
			d.Label = "Tomorrow"
		}
		out = append(out, d)
	}
	return out
}

// screenSummary is the one line under a title. The summary is written for
// exactly this; failing that, the first real line of the description.
func screenSummary(e *Event) string {
	for _, line := range topperSource(e) {
		line = strings.TrimSpace(stripBulletMarker(line))
		if keepsALine(line) {
			return truncateWords(line, 140)
		}
	}
	return ""
}

// relativeDay says when, the way someone standing at the bar thinks about it.
func relativeDay(at, now time.Time, allDay bool) string {
	days := int(dayStart(at).Sub(dayStart(now)).Hours()/24 + 0.5)
	switch {
	case days <= 0 && !allDay && at.Hour() >= 17:
		return "Tonight"
	case days <= 0:
		return "Today"
	case days == 1:
		return "Tomorrow"
	case days < screenWeekDays:
		return at.Format("Monday")
	default:
		return fmt.Sprintf("In %d days", days)
	}
}

// dayStart is local midnight on t's own date.
func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
