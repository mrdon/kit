package events

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// The promotion list.
//
// This is COMPUTED, never materialised. Every item below is derived on read
// from (event x channel x step) and left-joined against the sparse state in
// app_event_promos. Nothing schedules it, nothing backfills it, and there is
// no reconciler: retime a drip or flip a channel to `subscribed` and the next
// page load simply reflects it.
//
// The one rule that keeps the page usable rather than punitive: a missed drip
// beat EXPIRES and drops off. The complaint that started this design was not
// "I forget things", it was "I will not spend hours a day on this" -- and a
// list that accumulates every reminder you ever missed is a guilt ledger, not
// a tool.

// PromoState is what has happened to one item.
type PromoState string

const (
	// PromoTodo is the absence of a stored row. It is computed, never
	// written: a stored todo would be a second source of truth that could
	// disagree with the template about whether the work still applies.
	PromoTodo PromoState = "todo"
	// PromoDone -- a human did it.
	PromoDone PromoState = "done"
	// PromoIgnored -- deliberately not doing this one. Distinct from done, and
	// scoped to this event x channel x step. The systematic case ("the chamber
	// never wants standing offers") is the channel's MinProminence, not this.
	PromoIgnored PromoState = "ignored"
	// PromoExpired -- a drip beat whose window closed. Computed, never stored.
	PromoExpired PromoState = "expired"
	// PromoAutoDone / PromoAutoFailed -- what an automated channel did. A
	// failure is shown as actionable work rather than buried in a log,
	// because a channel that quietly stopped posting is worse than one that
	// was never automated.
	PromoAutoDone   PromoState = "auto_done"
	PromoAutoFailed PromoState = "auto_failed"
)

// actionable reports whether an item belongs in the working list rather than
// the collapsed "done automatically" group.
func (s PromoState) actionable() bool {
	return s == PromoTodo || s == PromoAutoFailed
}

// PromoItem is one row of the page.
type PromoItem struct {
	EventID    uuid.UUID `json:"event_id"`
	EventTitle string    `json:"event_title"`
	EventSlug  string    `json:"event_slug"`
	EventStart time.Time `json:"event_start"`

	ChannelID   uuid.UUID `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	SubmitURL   string    `json:"submit_url,omitempty"`

	StepKey   string   `json:"step_key"`
	StepLabel string   `json:"step_label"`
	StepKind  StepKind `json:"step_kind"`

	State PromoState `json:"state"`
	URL   string     `json:"url,omitempty"`
	Note  string     `json:"note,omitempty"`

	// DueAt is when this item wants doing -- for a drip or one-shot that is
	// the event date minus lead time; for a cadence it is the next qualifying
	// occurrence minus lead time.
	DueAt time.Time `json:"due_at"`
	// Overdue is DueAt in the past while still actionable. Ordering leans on
	// it rather than on the event date; see PromoList.
	Overdue bool `json:"overdue"`

	// LastDoneAt / LastURL are carried by cadence items so the row can say
	// "last posted 3 Aug" with a link. That answers "is this due?" and "what
	// did I say last time?" in one glance, which is the cheap guard against
	// posting near-identical copy every month.
	LastDoneAt *time.Time `json:"last_done_at,omitempty"`
	LastURL    string     `json:"last_url,omitempty"`

	// Manual is false for work an automated connector will do unattended.
	Manual bool `json:"manual"`
}

// promoKey identifies one computed item against its stored state.
type promoKey struct {
	eventID   uuid.UUID
	channelID uuid.UUID
	stepKey   string
}

// promoRecord is the stored half: the latest state for one key, plus the most
// recent completion, which is what anchors a cadence.
type promoRecord struct {
	State      PromoState
	URL        string
	Note       string
	UpdatedAt  time.Time
	LastDoneAt *time.Time
	LastURL    string
}

// buildPromoList is the whole computation, kept pure so it can be tested
// without a database. `now` is injected for the same reason.
func buildPromoList(events []Event, channels []Channel, state map[promoKey]promoRecord, now time.Time) []PromoItem {
	out := make([]PromoItem, 0, len(events)*2)
	for i := range events {
		e := &events[i]
		for ci := range channels {
			c := &channels[ci]
			if !c.appliesTo(e) {
				continue
			}
			for _, s := range c.Steps {
				if !c.stepApplies(s, e) {
					continue
				}
				item, ok := buildPromoItem(e, c, s, state[promoKey{e.ID, c.ID, s.Key}], now)
				if !ok {
					continue
				}
				out = append(out, item)
			}
		}
	}
	sortPromoItems(out)
	return out
}

// buildPromoItem resolves one (event, channel, step) against its stored state.
// Returns false when the item should not appear at all.
func buildPromoItem(e *Event, c *Channel, s Step, rec promoRecord, now time.Time) (PromoItem, bool) {
	item := PromoItem{
		EventID:     e.ID,
		EventTitle:  e.Title,
		EventSlug:   e.Slug,
		EventStart:  e.StartsAt,
		ChannelID:   c.ID,
		ChannelName: c.Name,
		SubmitURL:   c.SubmitURL,
		StepKey:     s.Key,
		StepLabel:   s.Label,
		StepKind:    s.Kind,
		State:       rec.State,
		URL:         rec.URL,
		Note:        rec.Note,
		LastDoneAt:  rec.LastDoneAt,
		LastURL:     rec.LastURL,
		Manual:      c.isManualWork(s),
	}
	if item.State == "" {
		item.State = PromoTodo
	}

	// An ignored item is settled; it neither expires nor comes back.
	if item.State == PromoIgnored {
		return PromoItem{}, false
	}

	due, ok := stepDue(e, c, s, rec, now)
	if !ok {
		return PromoItem{}, false
	}
	item.DueAt = due

	if item.State == PromoTodo && stepExpired(e, s, due, now) {
		// Lapse quietly. Not red, not a nag -- and not listed at all, which
		// is the difference between a working list and a backlog of guilt.
		return PromoItem{}, false
	}

	// A cadence RE-ARMS, which is what separates it from the other kinds. Its
	// stored `done` row is the anchor for the next cycle, not a final state --
	// so once the interval has elapsed the item becomes outstanding again
	// rather than staying done forever. Treating the stored state as final
	// here would mean posting about trivia once and never being asked again.
	if s.Kind == StepCadence && !item.State.actionable() && !due.After(now) {
		item.State = PromoTodo
	}

	item.Overdue = item.State.actionable() && due.Before(now)
	return item, true
}

// stepDue is when an item wants doing.
//
// One-shot and drip both count backwards from the event, which is what makes
// priority meaningful: a chamber wanting two weeks' notice is urgent for an
// event three weeks out, while a Facebook post for the same event is not.
// Ordering by event date would get that exactly backwards.
//
// A cadence is anchored to the series' own dates instead: the first occurrence
// at least IntervalDays after the last post. Skipping a cycle means owing one
// rather than two, because there is only ever one computed instance
// outstanding.
func stepDue(e *Event, c *Channel, s Step, rec promoRecord, now time.Time) (time.Time, bool) {
	switch s.Kind {
	case StepCadence:
		// Aligned to the series' OWN occurrences, not a floating interval.
		//
		// This is what makes one setting behave sensibly across very
		// different rhythms. IntervalDays is a floor -- "don't post about
		// this more often than every N days" -- and the actual due date is
		// the first occurrence at or after that floor. So with a 21-day
		// floor, weekly trivia gets promoted every third or fourth week,
		// while a D&D night that runs every 26 days gets promoted before
		// every single one. Rarer things get proportionally more attention
		// without anyone classifying them.
		//
		// A floating interval got both of those wrong in the same way: it
		// under-promoted the rare series, and it landed the post on whatever
		// day the arithmetic produced, which for a weekly quiz is routinely a
		// day with no quiz anywhere near it.
		earliest := now
		if rec.LastDoneAt != nil {
			// The grace period is what makes "roughly monthly" work for BOTH a
			// weekly quiz and a roughly-monthly game night.
			//
			// Without it, a strict floor skips a whole cycle whenever the
			// series' own gap is a little under the interval: a game night
			// every 26 days against a 28-day floor never qualifies on its own
			// date, so the next eligible night is 52 days out and every other
			// game goes unpromoted. Letting a night that falls slightly early
			// count instead gives every game a post, while a weekly quiz --
			// which has a candidate every 7 days -- still lands about a month
			// apart. One setting, and the rarer thing gets proportionally more
			// attention without anyone configuring that.
			grace := s.IntervalDays / 4
			earliest = rec.LastDoneAt.AddDate(0, 0, s.IntervalDays-grace)
		}
		if occ, ok := nextOccurrenceOnOrAfter(e, earliest); ok {
			// Post ahead of the night itself, by however much notice this
			// channel wants.
			return occ.AddDate(0, 0, -c.LeadTimeDays), true
		}
		// The series has run out of dates -- a rule with an UNTIL, or an
		// exhausted date list. Nothing left to promote.
		return time.Time{}, false

	case StepDrip:
		return e.StartsAt.AddDate(0, 0, -s.OffsetDays), true

	case StepOneshot:
		if e.Repeats() {
			// A series has no meaningful single deadline -- its "first
			// occurrence" may be years past. Due now; it stays until done.
			return now, true
		}
		if e.StartsAt.Before(now) {
			// The event has been and gone; submitting it now helps nobody.
			return time.Time{}, false
		}
		return e.StartsAt.AddDate(0, 0, -c.LeadTimeDays), true
	}
	return time.Time{}, false
}

// nextOccurrenceOnOrAfter finds the first date a series lands on at or after
// `from`. The window is bounded rather than open-ended because this runs for
// every (event, channel, step) on every page load; a year and a bit covers
// anything short of an annual series, and a series with no date inside it has
// effectively finished.
func nextOccurrenceOnOrAfter(e *Event, from time.Time) (time.Time, bool) {
	occ := e.Occurrences(from, from.AddDate(0, 0, cadenceLookaheadDays))
	if len(occ) == 0 {
		return time.Time{}, false
	}
	return occ[0].Start, true
}

// cadenceLookaheadDays bounds the occurrence search above.
const cadenceLookaheadDays = 400

// stepExpired reports whether a drip beat's window has closed.
//
// Only drip expires. A one-shot stays outstanding until the event passes
// (handled in stepDue), and a cadence re-arms rather than lapsing.
func stepExpired(e *Event, s Step, due, now time.Time) bool {
	if s.Kind != StepDrip {
		return false
	}
	// Past the event itself, every beat is moot.
	if now.After(e.StartsAt) {
		return true
	}
	if s.ExpiresAfterDays <= 0 {
		return false
	}
	return now.After(due.AddDate(0, 0, s.ExpiresAfterDays))
}

// sortPromoItems puts the page in the order it should be worked.
//
// Overdue first, then by due date. Prominence breaks ties so that when two
// things are due the same day the anniversary party outranks the bike night.
// Note this is emphatically NOT sorted by event date -- see stepDue.
func sortPromoItems(items []PromoItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Overdue != b.Overdue {
			return a.Overdue
		}
		if !a.DueAt.Equal(b.DueAt) {
			return a.DueAt.Before(b.DueAt)
		}
		if a.EventTitle != b.EventTitle {
			return a.EventTitle < b.EventTitle
		}
		return a.StepKey < b.StepKey
	})
}

// PromoSummary is what the reminder card carries: a count, not a grid. The
// swipe stack is good at "7 things outstanding, tap to open"; it is a 2-inch
// phone and a bad place to work a checklist.
type PromoSummary struct {
	Outstanding int `json:"outstanding"`
	Overdue     int `json:"overdue"`
}

func summarisePromo(items []PromoItem) PromoSummary {
	var s PromoSummary
	for _, it := range items {
		if !it.State.actionable() {
			continue
		}
		s.Outstanding++
		if it.Overdue {
			s.Overdue++
		}
	}
	return s
}
