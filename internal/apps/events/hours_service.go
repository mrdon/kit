package events

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SetSpecialHours saves one date's hours and squares the day's events with
// them: skipping repeating events on a closed day, and reporting anything a
// person has to decide. Idempotent, so running it again after adding an event
// applies the closure to that one too.
func (s *Service) SetSpecialHours(ctx context.Context, tenantID uuid.UUID, h SpecialHours) (*HoursResult, error) {
	if err := normaliseHours(&h); err != nil {
		return nil, err
	}
	if err := upsertSpecialHours(ctx, s.pool, tenantID, h); err != nil {
		return nil, err
	}
	res := &HoursResult{Hours: h}
	settings, err := getSettings(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	loc, err := ResolveTimezone(settings.Timezone)
	if err != nil {
		return nil, err
	}
	dayStart := time.Date(h.Day.Year(), h.Day.Month(), h.Day.Day(), 0, 0, 0, 0, loc)
	events, err := s.eventsOnDay(ctx, tenantID, dayStart)
	if err != nil {
		return nil, err
	}
	for i := range events {
		if err := s.squareWithHours(ctx, &events[i], h, dayStart, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// RemoveSpecialHours deletes one date's hours. Skips a closure added stay put:
// they now look exactly like skips a person added, and quietly bringing trivia
// back onto a day is the more surprising mistake of the two.
func (s *Service) RemoveSpecialHours(ctx context.Context, tenantID uuid.UUID, day time.Time) (bool, error) {
	return deleteSpecialHours(ctx, s.pool, tenantID, day)
}

// ListSpecialHours returns today's and later dates in the tenant's zone.
func (s *Service) ListSpecialHours(ctx context.Context, tenantID uuid.UUID) ([]SpecialHours, error) {
	settings, err := getSettings(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	loc, err := ResolveTimezone(settings.Timezone)
	if err != nil {
		return nil, err
	}
	return listSpecialHours(ctx, s.pool, tenantID, timeNow().In(loc).Format(skipDayLayout))
}

// eventsOnDay returns the onsite, non-cancelled events with an occurrence
// starting on the given local day.
func (s *Service) eventsOnDay(ctx context.Context, tenantID uuid.UUID, dayStart time.Time) ([]Event, error) {
	dayEnd := dayStart.AddDate(0, 0, 1)
	all, err := listEvents(ctx, s.pool, tenantID, ListFilter{
		From: &dayStart, To: &dayEnd, ExcludeCancelled: true, Limit: 500,
	})
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, e := range all {
		if e.Venue == VenueOffsite || len(e.Occurrences(dayStart, dayEnd)) == 0 {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// squareWithHours applies one day's hours to one event on it.
func (s *Service) squareWithHours(ctx context.Context, e *Event, h SpecialHours, dayStart time.Time, res *HoursResult) error {
	day := h.DayString()
	if h.Closed {
		if !e.Repeats() {
			res.Conflicts = append(res.Conflicts, e.Title+" is a one-off on a closed day; cancel it or move it")
			return nil
		}
		skips := upcomingSkipStrings(e)
		skips = append(skips, day)
		if _, err := s.Update(ctx, e.TenantID, e.ID, UpdateParams{SkipDates: &skips}); err != nil {
			return fmt.Errorf("skipping %s on %s: %w", e.Title, day, err)
		}
		res.Skipped = append(res.Skipped, e.Title)
		return nil
	}
	occ := e.Occurrences(dayStart, dayStart.AddDate(0, 0, 1))[0]
	opens, _ := time.Parse(clockLayout, h.Opens)
	closes, _ := time.Parse(clockLayout, h.Closes)
	open := dayStart.Add(time.Duration(opens.Hour())*time.Hour + time.Duration(opens.Minute())*time.Minute)
	shut := dayStart.Add(time.Duration(closes.Hour())*time.Hour + time.Duration(closes.Minute())*time.Minute)
	if occ.Start.Before(open) || occ.End.After(shut) {
		res.Conflicts = append(res.Conflicts, fmt.Sprintf("%s runs %s–%s, outside %s–%s",
			e.Title, occ.Start.Format("3:04pm"), occ.End.Format("3:04pm"),
			open.Format("3:04pm"), shut.Format("3:04pm")))
	}
	return nil
}

// upcomingSkipStrings is the event's skip list from today on, as dates --
// the form update's SkipDates takes back.
func upcomingSkipStrings(e *Event) []string {
	loc := e.Loc()
	today := timeNow().In(loc).Format(skipDayLayout)
	var out []string
	for _, d := range e.ExDates {
		if day := d.In(loc).Format(skipDayLayout); day >= today {
			out = append(out, day)
		}
	}
	return out
}
