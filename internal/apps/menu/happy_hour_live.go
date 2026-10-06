package menu

import (
	"slices"
	"time"
)

// Whether happy hour is on right now.
//
// There are two ways it changes. The schedule is a series of moments -- each
// scheduled day's start turns it on, each end turns it off -- and Start now /
// End now are moments too, set by hand. Whichever happened most recently
// wins. So End now at 3:30 ends today's happy hour early and it comes back at
// tomorrow's start; Start now at 7pm runs it until the next scheduled end, or
// until somebody presses End now.
//
// Nothing has to fire at 3pm for this to work. The state is worked out from
// the clock whenever anyone asks -- the board on every poll, the Square sync
// every minute -- so a server that was down at 3:00 still shows the right
// thing at 3:05.

// HappyLive is the last Start now / End now: what it set, and when.
type HappyLive struct {
	On bool      `json:"on"`
	At time.Time `json:"at"`
}

// hhMoment is one point where the schedule switches happy hour.
type hhMoment struct {
	On bool
	At time.Time
}

// dayWindow is the scheduled start and end on the local date of d, if the
// schedule runs that day.
func (h HappyHour) dayWindow(d time.Time) (time.Time, time.Time, bool) {
	if !h.Enabled || !slices.Contains(h.Days, dayCodes[(int(d.Weekday())+6)%7]) {
		return time.Time{}, time.Time{}, false
	}
	if h.StartsOn != "" && d.Format(time.DateOnly) < h.StartsOn {
		return time.Time{}, time.Time{}, false
	}
	start, err1 := parseClock(h.Start)
	end, err2 := parseClock(h.End)
	if err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, false
	}
	midnight := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
	return midnight.Add(time.Duration(start) * time.Minute), midnight.Add(time.Duration(end) * time.Minute), true
}

// lastMoment is the most recent scheduled switch at or before now. A week
// back is far enough: any schedule with a day in it switches inside a week.
func (h HappyHour) lastMoment(now time.Time) (hhMoment, bool) {
	for i := range 8 {
		start, end, ok := h.dayWindow(now.AddDate(0, 0, -i))
		if !ok {
			continue
		}
		if !end.After(now) {
			return hhMoment{On: false, At: end}, true
		}
		if !start.After(now) {
			return hhMoment{On: true, At: start}, true
		}
	}
	return hhMoment{}, false
}

// nextEnd is the next scheduled end after now.
func (h HappyHour) nextEnd(now time.Time) (time.Time, bool) {
	for i := range 8 {
		_, end, ok := h.dayWindow(now.AddDate(0, 0, i))
		if ok && end.After(now) {
			return end, true
		}
	}
	return time.Time{}, false
}

// OnAt reports whether happy hour is on at now, read in loc.
func (h HappyHour) OnAt(live *HappyLive, now time.Time, loc *time.Location) bool {
	if len(h.Beers) == 0 {
		return false
	}
	now = now.In(loc)
	m, scheduled := h.lastMoment(now)
	pressed := live != nil && !live.At.IsZero() && !live.At.After(now)
	if pressed && (!scheduled || live.At.After(m.At)) {
		return live.On
	}
	return scheduled && m.On
}

// Until is when a happy hour that is on now will end, written for the wall:
// "5pm" today, "Tue 5pm" on another day, "" when only End now will end it.
func (h HappyHour) Until(now time.Time, loc *time.Location) string {
	now = now.In(loc)
	end, ok := h.nextEnd(now)
	if !ok {
		return ""
	}
	label := clockLabel(end.Format("15:04"))
	if end.Format(time.DateOnly) != now.Format(time.DateOnly) {
		label = end.Format("Mon") + " " + label
	}
	return label
}

// happyStamp is the happy hour's share of the board's version stamp: whether
// it is on right now, and which setting. Without it the wall would only flip
// when a beer changed, which is to say not at 3pm.
func happyStamp(state *HappyHourState, now time.Time, loc *time.Location) string {
	if state == nil {
		return ""
	}
	on := "0"
	if state.Config.OnAt(state.Live, now, loc) {
		on = "1"
	}
	return "hh" + on + state.Config.Hash()[:6]
}
