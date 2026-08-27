package events

import (
	"slices"
	"strings"
	"time"
)

// Explicit repeat dates: the list a person edits when no rule fits.
//
// The stored shape is RFC 5545's -- starts_at is DTSTART, rdates holds the
// extras -- but that is not the shape anyone thinks in. People think of a
// series as one flat list of dates, and the UI presents exactly that. These
// helpers are the join between the two, and they live in one file so the
// invariant they maintain has a single home:
//
//	the combined set is sorted, deduped, and starts_at is its earliest member.
//
// Holding that invariant is what lets every other query stay simple. starts_at
// keeps meaning "the first occurrence", so existing ORDER BY and lower-bound
// clauses need no knowledge of the array, and Series.Expand can treat rdates as
// purely additive.

// maxRDates bounds an explicit date list. Generous enough for a weekly series
// authored by hand for two years, small enough that the array column and the
// RDATE line stay sane. An open-ended series is what a repeat rule is for.
const maxRDates = 200

// parseDates reads a list of human-entered date/times in the event's zone.
// An entry that is blank is skipped rather than rejected, because the console's
// date list can carry an empty row the user has not filled in yet.
func parseDates(raw []string, loc *time.Location) ([]time.Time, error) {
	var out []time.Time
	for _, s := range raw {
		if strings.TrimSpace(s) == "" {
			continue
		}
		t, err := ParseTime(s, loc)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if len(out) > maxRDates {
		return nil, invalid("an event can have at most %d dates; use a repeat rule for an open-ended series", maxRDates)
	}
	return out, nil
}

// applyDates folds a set of extra dates into the event, re-establishing the
// invariant described above.
//
// The subtle part is the end time. EndsAt is stored as an absolute instant, not
// a duration, so when a supplied date is EARLIER than the current start and
// takes over as starts_at, the end has to move by the same delta or the event
// silently changes length -- and since Series.Expand derives every occurrence's
// duration from that pair, a wrong delta would resize the whole series, not
// just its first date.
func applyDates(e *Event, extra []time.Time) {
	all := append([]time.Time{e.StartsAt}, extra...)
	slices.SortFunc(all, func(a, b time.Time) int { return a.Compare(b) })
	all = slices.CompactFunc(all, func(a, b time.Time) bool { return a.Equal(b) })

	if newStart := all[0]; !newStart.Equal(e.StartsAt) {
		if e.EndsAt != nil {
			moved := e.EndsAt.Add(newStart.Sub(e.StartsAt))
			e.EndsAt = &moved
		}
		e.StartsAt = newStart
	}
	e.RDates = all[1:]
}

// AllDates returns the event's explicit dates as one flat list, earliest
// first, with starts_at at its head.
//
// This is the read side of the same join: it is what the console's date editor
// renders and what a caller means by "the dates this happens on". It describes
// only the explicit list -- a rule-driven series is expanded through
// Occurrences instead, because it has no finite list to return.
func (e *Event) AllDates() []time.Time {
	if e == nil {
		return nil
	}
	out := make([]time.Time, 0, len(e.RDates)+1)
	out = append(out, e.StartsAt)
	return append(out, e.RDates...)
}

// validateDates checks the explicit list against the same rules the database
// enforces, plus the ones SQL cannot express.
func validateDates(e *Event) error {
	if len(e.RDates) == 0 {
		return nil
	}
	if e.AllDay {
		return invalid("all-day events cannot have a list of dates; give the event a start and end time")
	}
	if len(e.RDates) > maxRDates {
		return invalid("an event can have at most %d dates; use a repeat rule for an open-ended series", maxRDates)
	}
	// applyDates guarantees both of these, so a breach means a caller reached
	// past it -- worth failing loudly rather than storing a list that Expand
	// and Google would then disagree about.
	for i, d := range e.RDates {
		if !d.After(e.StartsAt) {
			return invalid("repeat dates must all come after the first date")
		}
		if i > 0 && !d.After(e.RDates[i-1]) {
			return invalid("repeat dates must be in order and unique")
		}
	}
	return nil
}
