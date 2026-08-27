package events

import (
	"encoding/json"
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

// NextOccurrence returns the first occurrence at or after the start of today,
// or nil when every date is behind us.
//
// "Start of today" rather than "now" on purpose: an event running this
// afternoon is still today's event at 4pm, and a list that drops it the moment
// it begins is wrong in the one hour people are most likely to be looking.
func (e *Event) NextOccurrence() *time.Time {
	if e == nil {
		return nil
	}
	loc := e.Loc()
	now := timeNow().In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	// A year is far enough to catch any rule we can store, and bounds an
	// otherwise unbounded weekly series.
	occ := e.Series().Expand(from, from.AddDate(1, 0, 0))
	if len(occ) == 0 {
		return nil
	}
	next := occ[0].Start
	return &next
}

// MarshalJSON adds the fields a caller would otherwise have to derive.
//
// next_occurrence exists because starts_at is the FIRST occurrence, which for
// a repeating event is frequently in the past -- a weekly quiz started in 2024,
// a monthly class whose series began in August. Every surface that lists events
// wants the next one, and the alternative is each of them reimplementing the
// expander in its own language. Kit already owns a tested one, so it answers
// the question here rather than exporting the puzzle.
//
// Computed on marshal rather than stored on the struct so it cannot go stale
// or be forgotten on a read path that builds an Event some other way.
func (e Event) MarshalJSON() ([]byte, error) {
	type alias Event // shed the method, or this recurses
	return json.Marshal(struct {
		alias
		NextOccurrence *time.Time `json:"next_occurrence,omitempty"`
		// The total number of dates an explicit list carries, so a caller can
		// say "4 dates" without receiving the list itself.
		DateCount int `json:"date_count,omitempty"`
	}{
		alias:          alias(e),
		NextOccurrence: e.NextOccurrence(),
		DateCount:      e.DateCount(),
	})
}

// DateCount is how many dates an explicit list holds, counting starts_at. Zero
// for an event with no list -- including a rule-driven series, which has no
// finite count to report.
func (e *Event) DateCount() int {
	if e == nil || len(e.RDates) == 0 {
		return 0
	}
	return len(e.RDates) + 1
}
