package events

import (
	"context"
	"fmt"
	"html/template"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The menu board's event panels: the fourth view of the same rows, after the
// feed, the topper and the screen.
//
// The board used to carry these as hand-written panels, refreshed by a nightly
// agent job. Stored text does not expire, so a poster stayed on the wall days
// after its night and a renamed series kept its old name. Deriving them here,
// each time the page loads, is what makes a dated panel safe to have at all.

const (
	// boardHorizonDays bounds the one-off posters: this week's nights. Further
	// out is the events screen's job, and a poster up for a month stops
	// being noticed.
	boardHorizonDays = 7
	// boardUpcomingMax caps the posters so the rail's rotation does not drag:
	// every panel costs the others a turn.
	boardUpcomingMax = 3
	// boardWeeklyMax is measured, not a preference: a sixth line with a
	// normal-length note overflows the agenda panel, which hides overflow
	// rather than wrapping, so the entry simply is not there.
	boardWeeklyMax = 5
	// boardLabelDays is how far ahead a label lookup reaches. A Give Back
	// night is booked weeks out, and its cause should show from the night
	// after the last one.
	boardLabelDays = 45
	// boardNoteMax keeps an agenda note to one line on the wall.
	boardNoteMax = 48
)

// BoardRequest says which events the board's stored panels point at.
type BoardRequest struct {
	// Labels are the labels a panel takes its image from. An event carrying
	// one is that panel's, so it gets no poster of its own as well.
	Labels []string
	// Claimed are events a hand-written panel is pinned to. They get no
	// automatic poster, and Live reports whether each is still to come.
	Claimed []uuid.UUID
}

// BoardEvent is one event as the board shows it.
type BoardEvent struct {
	EventID  uuid.UUID
	Title    string
	When     string // "Tonight", "Tomorrow", "Saturday"
	PosterID *uuid.UUID
}

// BoardWeekly is one line of the weekly agenda.
type BoardWeekly struct {
	When  string // "Every Mon", "Every Mon & Tue"
	Time  string // "5 to 9pm"
	Title string
	Note  string
}

// BoardFeed is everything the board's event panels need right now.
type BoardFeed struct {
	Today    string // the local date, so a stamp built from this rolls at midnight
	Upcoming []BoardEvent
	Weekly   []BoardWeekly
	ByLabel  map[string]BoardEvent
	Live     map[uuid.UUID]bool
}

// BoardFeed reads the board's events for a tenant at this moment.
func (a *App) BoardFeed(ctx context.Context, tenantID uuid.UUID, req BoardRequest) (*BoardFeed, error) {
	settings, err := getSettings(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	now := timeNow().In(settings.Loc())
	today := dayStart(now)
	horizon := today.AddDate(0, 0, boardLabelDays)
	events, err := listEvents(ctx, a.pool, tenantID, ListFilter{
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		From:       &today,
		To:         &horizon,
		Limit:      500,
	})
	if err != nil {
		return nil, fmt.Errorf("listing board events: %w", err)
	}
	return composeBoardFeed(events, now, req), nil
}

// BoardPoster loads one event poster as a data URI sized for a wall, or ""
// when it cannot be had. A missing poster costs its panel, never the page.
func (a *App) BoardPoster(ctx context.Context, tenantID, posterID uuid.UUID) template.URL {
	store := a.attachments()
	if store == nil {
		return ""
	}
	return loadScreenPoster(ctx, store, tenantID, posterID)
}

// composeBoardFeed is the pure half of BoardFeed, split out so the selection
// rules can be tested without a database.
func composeBoardFeed(events []Event, now time.Time, req BoardRequest) *BoardFeed {
	f := &BoardFeed{
		Today:   dayStart(now).Format(time.DateOnly),
		ByLabel: map[string]BoardEvent{},
		Live:    map[uuid.UUID]bool{},
	}
	claimed := map[uuid.UUID]bool{}
	for _, id := range req.Claimed {
		claimed[id] = true
	}
	for i := range events {
		e := &events[i]
		if !e.IsPubliclyVisible() {
			continue
		}
		if claimed[e.ID] {
			f.Live[e.ID] = nextOccurrence(e, now, dayStart(now).AddDate(0, 0, boardLabelDays)) != nil
		}
	}
	for _, label := range req.Labels {
		if be, ok := nextWithLabel(events, label, now); ok {
			f.ByLabel[label] = be
		}
	}
	f.Upcoming = boardUpcoming(events, now, req.Labels, claimed)
	f.Weekly = boardWeekly(events, now)
	return f
}

// boardUpcoming is this week's one-off nights at the brewery that have a
// poster, soonest first. An event with a list of set dates counts once per
// date, the way the screen's look-ahead treats it: each Battles & Brews night
// is its own night to plan around.
func boardUpcoming(events []Event, now time.Time, labels []string, claimed map[uuid.UUID]bool) []BoardEvent {
	end := dayStart(now).AddDate(0, 0, boardHorizonDays)
	type dated struct {
		ev BoardEvent
		at time.Time
	}
	var out []dated
	for i := range events {
		e := &events[i]
		if !onTheWall(e) || e.RRule != "" || e.HeroAttachmentID == nil || claimed[e.ID] ||
			slices.ContainsFunc(labels, e.HasLabel) {
			continue
		}
		for _, occ := range e.Occurrences(dayStart(now), end) {
			if occ.End.Before(now) {
				continue
			}
			at := occ.Start.In(now.Location())
			out = append(out, dated{boardEvent(e, at, now), at})
		}
	}
	slices.SortStableFunc(out, func(a, b dated) int { return a.at.Compare(b.at) })
	evs := make([]BoardEvent, 0, min(len(out), boardUpcomingMax))
	for _, d := range out[:min(len(out), boardUpcomingMax)] {
		evs = append(evs, d.ev)
	}
	return evs
}

// onTheWall is the board's floor: public, not a standing offer, and here. The
// board hangs in our own taproom, so a festival we are pouring at elsewhere is
// not news to anyone reading it.
func onTheWall(e *Event) bool {
	return e.IsPubliclyVisible() && !e.IsStandingOffer() && e.Venue != VenueOffsite
}

// nextWithLabel is the soonest event carrying a label that is still to come.
func nextWithLabel(events []Event, label string, now time.Time) (BoardEvent, bool) {
	var best BoardEvent
	var bestAt time.Time
	horizon := dayStart(now).AddDate(0, 0, boardLabelDays)
	for i := range events {
		e := &events[i]
		if !e.IsPubliclyVisible() || !e.HasLabel(label) {
			continue
		}
		occ := nextOccurrence(e, now, horizon)
		if occ == nil || (!bestAt.IsZero() && !occ.Start.Before(bestAt)) {
			continue
		}
		bestAt = occ.Start
		best = boardEvent(e, occ.Start.In(now.Location()), now)
	}
	return best, !bestAt.IsZero()
}

// nextOccurrence is the first occurrence that has not finished yet.
func nextOccurrence(e *Event, now, horizon time.Time) *Occurrence {
	for _, occ := range e.Occurrences(dayStart(now), horizon) {
		if !occ.End.Before(now) {
			return &occ
		}
	}
	return nil
}

func boardEvent(e *Event, at, now time.Time) BoardEvent {
	return BoardEvent{
		EventID:  e.ID,
		Title:    strings.TrimSpace(e.Title),
		When:     relativeDay(at, now, e.AllDay),
		PosterID: e.HeroAttachmentID,
	}
}
