package cogs

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// WeekItem is one line of the iPad's "This week" panel. Action names the
// form it opens, pre-filled from VesselID or PlanEntryID.
type WeekItem struct {
	Kind        string     `json:"kind"`
	Text        string     `json:"text"`
	When        string     `json:"when,omitempty"`
	Action      string     `json:"action,omitempty"`
	VesselID    *uuid.UUID `json:"vessel_id,omitempty"`
	PlanEntryID *uuid.UUID `json:"plan_entry_id,omitempty"`
	Due         bool       `json:"due"`
}

func (d *stateData) thisWeek(st *State, now time.Time, loc *time.Location) []WeekItem {
	horizon := dateOnly(now.AddDate(0, 0, 7))
	var out []WeekItem
	for i := range d.plan {
		p := &d.plan[i]
		if p.BatchID != nil || p.BrewOn > horizon {
			continue
		}
		beer := d.beers[p.BeerID]
		if beer == nil {
			continue
		}
		vid, pid := p.VesselID, p.ID
		out = append(out, WeekItem{Kind: "brew", Action: EvBrew, VesselID: &vid, PlanEntryID: &pid,
			Text: fmt.Sprintf("Brew %s into %s", beer.Name, d.n.vessel(&vid)), When: shortDay(p.BrewOn, loc),
			Due: p.BrewOn <= st.Today})
	}
	for _, t := range st.Vessels {
		id := t.ID
		switch {
		case t.Badge == BadgeDirty:
			out = append(out, WeekItem{Kind: "clean", Action: EvClean, VesselID: &id, Text: "Clean " + t.Name})
		case t.Badge == BadgeDue:
			out = append(out, WeekItem{Kind: "due", Action: t.NextAction, VesselID: &id, Due: true,
				Text: fmt.Sprintf("%s in %s: %s", t.Beer, t.Name, lowerFirst(t.Next))})
		case t.ReadyOn != "" && t.ReadyOn <= horizon && t.Kind != KindServing:
			out = append(out, WeekItem{Kind: "soon", VesselID: &id, When: shortDay(t.ReadyOn, loc),
				Text: fmt.Sprintf("%s in %s: %s", t.Beer, t.Name, lowerFirst(t.Next))})
		}
	}
	if countDue(st, now, d.settings) {
		out = append(out, WeekItem{Kind: "count", Action: EvCount, Text: "Count the packaged beer", Due: true})
	}
	return out
}

func countDue(st *State, now time.Time, s Settings) bool {
	if len(st.Packaged) == 0 {
		return false
	}
	return st.LastCount == nil || now.Sub(*st.LastCount) > time.Duration(s.CountIntervalDays)*24*time.Hour
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'A' && r[0] <= 'Z' {
		r[0] += 'a' - 'A'
	}
	return string(r)
}

// scheduleDays is how far the Schedule tab looks ahead.
const scheduleDays = 28

// Lane is one vessel's next four weeks.
type Lane struct {
	VesselID uuid.UUID `json:"vessel_id"`
	Name     string    `json:"name"`
	Bars     []Bar     `json:"bars"`
}

// Bar is a stretch of a lane, in whole days from today (End exclusive).
type Bar struct {
	Kind        string     `json:"kind"` // in_tank | planned | clean | free
	Label       string     `json:"label"`
	Start       int        `json:"start"`
	End         int        `json:"end"`
	PlanEntryID *uuid.UUID `json:"plan_entry_id,omitempty"`
	Why         string     `json:"why,omitempty"`
	Clash       bool       `json:"clash,omitempty"`
}

func (d *stateData) schedule(tiles []BoardVessel, now time.Time, loc *time.Location) []Lane {
	today := dayOf(now, loc)
	var out []Lane
	for _, t := range tiles {
		if t.Kind != KindFermenter && t.Kind != KindBrite {
			continue
		}
		lane := Lane{VesselID: t.ID, Name: t.Name}
		busyUntil := 0
		switch {
		case t.BatchID != nil:
			end := 1
			if t.ReadyOn != "" {
				if r, err := time.ParseInLocation("2006-01-02", t.ReadyOn, loc); err == nil {
					end = max(1, daysBetween(today, r, loc))
				}
			}
			lane.Bars = append(lane.Bars, Bar{Kind: "in_tank", Start: 0, End: min(end, scheduleDays),
				Label: fmt.Sprintf("%s, %s", t.Beer, lowerFirst(t.StageLabel))})
			lane.Bars = append(lane.Bars, Bar{Kind: "clean", Label: "Clean", Start: end, End: end + 1})
			busyUntil = end + 1
		case t.Dirty:
			lane.Bars = append(lane.Bars, Bar{Kind: "clean", Label: "Clean", Start: 0, End: 1})
			busyUntil = 1
		}
		lane.Bars = append(lane.Bars, d.plannedBars(t.ID, today, loc, busyUntil)...)
		lane.Bars = fillFree(lane.Bars)
		out = append(out, lane)
	}
	return out
}

func (d *stateData) plannedBars(vesselID uuid.UUID, today time.Time, loc *time.Location, busyUntil int) []Bar {
	var out []Bar
	for i := range d.plan {
		p := &d.plan[i]
		if p.VesselID != vesselID || p.BatchID != nil {
			continue
		}
		beer := d.beers[p.BeerID]
		on, err := time.ParseInLocation("2006-01-02", p.BrewOn, loc)
		if beer == nil || err != nil {
			continue
		}
		start := daysBetween(today, on, loc)
		if start >= scheduleDays {
			continue
		}
		length := beer.FermentDays + beer.ConditionDays
		if beer.DryHop {
			length += beer.DryHopDays
		}
		pid := p.ID
		out = append(out, Bar{Kind: "planned", Label: "Brew " + beer.Name, Start: max(0, start),
			End: start + max(1, length), PlanEntryID: &pid, Why: p.Why, Clash: start < busyUntil})
	}
	return out
}

// fillFree clips bars to the window and fills the gaps with free time:
// "FV-7 is free all month" is a planning signal, so it's drawn.
func fillFree(bars []Bar) []Bar {
	var out []Bar
	cursor := 0
	for _, b := range bars {
		b.End = min(b.End, scheduleDays)
		if b.Start >= scheduleDays || b.End <= b.Start {
			continue
		}
		if b.Start > cursor {
			out = append(out, Bar{Kind: "free", Label: "Free", Start: cursor, End: b.Start})
		}
		out = append(out, b)
		cursor = max(cursor, b.End)
	}
	if cursor < scheduleDays {
		out = append(out, Bar{Kind: "free", Label: "Free", Start: cursor, End: scheduleDays})
	}
	return out
}
