package menu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Happy hour: a fixed price on a few beers, on a weekly schedule.
//
// It is stored once and read by both sides. The board applies it at render
// time -- the wall switches on at the start time and off at the end with
// nothing pushed to it -- and a Square sync turns the same setting into an
// automatic discount the register applies by itself. See happy_hour_square.go.
//
// The price is a price, not a discount, because that is how a taproom says
// it: "$5 pints". Square only knows amount-off, and the beers do not share a
// regular price, so the sync works out each beer's discount from its own
// price. Saying "$1.50 off" here instead would put a different number on
// every row of the wall.

// dayCodes is the week in iCalendar order, which is what Square's RRULE takes
// and what the console's day picker lays out.
var dayCodes = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// HappyHour is the setting.
type HappyHour struct {
	Enabled bool `json:"enabled"`

	// Days are lowercase three-letter codes, mon..sun.
	Days []string `json:"days"`

	// Start and End are local wall-clock times, "15:00" and "17:00". End is
	// exclusive and must be later the same day: a happy hour that runs past
	// midnight is not a thing this taproom does, and modelling it would double
	// the window logic for nobody.
	Start string `json:"start"`
	End   string `json:"end"`

	// StartsOn is the first day it runs, "2026-10-12". Empty means it is
	// already running. It lets a happy hour be set up and synced to Square
	// ahead of its launch without the wall announcing it early.
	StartsOn string `json:"starts_on,omitempty"`

	// PriceCents is the happy hour price of one pour of Size.
	PriceCents int `json:"price_cents"`

	// Size is the Square variation name the price applies to, which is also
	// the board's pour label: "16oz". Other pours of the same beer ring at
	// their normal price.
	Size string `json:"size"`

	// Beers are names as the board shows them. Square matches an item whose
	// name or kitchen name is the same, ignoring only case and spacing; there
	// is no fuzzy join, for the reason sync_flight_pours.py gives -- a match
	// you can predict by reading two names beats one that is usually right.
	Beers []string `json:"beers"`
}

// DefaultHappyHour is what an unconfigured workspace starts from: a weekday
// afternoon, switched off, so opening the settings page offers a sensible
// form rather than a blank one.
func DefaultHappyHour() HappyHour {
	return HappyHour{
		Days:       []string{"mon", "tue", "wed", "thu", "fri"},
		Start:      "15:00",
		End:        "17:00",
		PriceCents: 500,
		Size:       DefaultPour,
		Beers:      []string{},
	}
}

// Validate checks the setting is one both sides can act on.
func (h *HappyHour) Validate() error {
	if len(h.Days) == 0 {
		return fmt.Errorf("%w: happy hour needs at least one day", ErrPayloadInvalid)
	}
	for _, d := range h.Days {
		if !slices.Contains(dayCodes, d) {
			return fmt.Errorf("%w: unknown day %q (want mon, tue, wed, thu, fri, sat or sun)", ErrPayloadInvalid, d)
		}
	}
	start, err := parseClock(h.Start)
	if err != nil {
		return fmt.Errorf("%w: start: %w", ErrPayloadInvalid, err)
	}
	end, err := parseClock(h.End)
	if err != nil {
		return fmt.Errorf("%w: end: %w", ErrPayloadInvalid, err)
	}
	if end <= start {
		return fmt.Errorf("%w: happy hour must end after it starts on the same day", ErrPayloadInvalid)
	}
	if h.StartsOn != "" {
		if _, err := time.Parse(time.DateOnly, h.StartsOn); err != nil {
			return fmt.Errorf("%w: starts_on must be YYYY-MM-DD", ErrPayloadInvalid)
		}
	}
	if h.PriceCents <= 0 {
		return fmt.Errorf("%w: happy hour price must be more than zero", ErrPayloadInvalid)
	}
	if strings.TrimSpace(h.Size) == "" {
		return fmt.Errorf("%w: happy hour needs a pour size, e.g. 16oz", ErrPayloadInvalid)
	}
	if h.Enabled && len(h.Beers) == 0 {
		return fmt.Errorf("%w: happy hour is on but names no beers", ErrPayloadInvalid)
	}
	return nil
}

// Normalize sorts the days into week order, trims names and drops duplicates,
// so two settings that mean the same thing hash the same and the console does
// not report Square out of date over a reordering.
func (h *HappyHour) Normalize() {
	days := make([]string, 0, len(h.Days))
	for _, d := range dayCodes {
		if slices.Contains(h.Days, d) || slices.Contains(h.Days, strings.ToUpper(d)) {
			days = append(days, d)
		}
	}
	// Anything unrecognised is kept so Validate can name it.
	for _, d := range h.Days {
		if !slices.Contains(dayCodes, strings.ToLower(d)) {
			days = append(days, d)
		}
	}
	h.Days = days
	h.Start = strings.TrimSpace(h.Start)
	h.End = strings.TrimSpace(h.End)
	h.StartsOn = strings.TrimSpace(h.StartsOn)
	h.Size = strings.TrimSpace(h.Size)
	seen := map[string]bool{}
	beers := make([]string, 0, len(h.Beers))
	for _, b := range h.Beers {
		b = strings.Join(strings.Fields(b), " ")
		if b == "" || seen[nameKey(b)] {
			continue
		}
		seen[nameKey(b)] = true
		beers = append(beers, b)
	}
	h.Beers = beers
}

// Hash fingerprints the parts of the setting Square is built from.
func (h HappyHour) Hash() string {
	raw, _ := json.Marshal(h) //nolint:errchkjson // plain struct, cannot fail
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

// Includes reports whether a beer is one of the happy hour beers.
func (h HappyHour) Includes(name string) bool {
	key := nameKey(name)
	for _, b := range h.Beers {
		if nameKey(b) == key {
			return true
		}
	}
	return false
}

// Price is the happy hour price as the board writes prices: "5", "5.50".
func (h HappyHour) Price() string { return formatCents(h.PriceCents) }

// Summary is the one-line description used by the tools and the console.
func (h HappyHour) Summary() string {
	sched := "schedule off"
	if h.Enabled {
		sched = "on schedule"
	}
	s := fmt.Sprintf("Happy hour (%s): $%s %s pours of %s, %s %s–%s",
		sched, h.Price(), h.Size, joinWords(h.Beers), describeDays(h.Days),
		clockLabel(h.Start), clockLabel(h.End))
	if h.StartsOn != "" {
		s += ", starting " + h.StartsOn
	}
	return s
}

// applyHappyHour marks the taps on happy hour and returns the banner text,
// for a happy hour that is on. until is when it ends, "" when nothing will
// end it but somebody pressing End now.
//
// Only a tap poured at the happy hour size is marked. A beer on the list that
// the board offers in a 10oz pour is not $5, and the wall must not say so.
func applyHappyHour(b *Board, h *HappyHour, until string) string {
	marked := 0
	for i := range b.Taps {
		t := &b.Taps[i]
		size := t.Size
		if size == "" {
			size = DefaultPour
		}
		if !h.Includes(t.Name) || !strings.EqualFold(size, h.Size) {
			continue
		}
		t.HappyPrice = h.Price()
		marked++
	}
	switch {
	case marked == 0:
		return ""
	case until == "":
		return "Happy hour"
	}
	return "Happy hour · till " + until
}

// parseClock reads "15:00" into minutes after midnight.
func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a time like 15:00", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// clockLabel writes "15:00" as "3pm" and "15:30" as "3:30pm".
func clockLabel(s string) string {
	m, err := parseClock(s)
	if err != nil {
		return s
	}
	h, min := m/60, m%60
	suffix := "am"
	if h >= 12 {
		suffix = "pm"
	}
	h %= 12
	if h == 0 {
		h = 12
	}
	if min == 0 {
		return strconv.Itoa(h) + suffix
	}
	return fmt.Sprintf("%d:%02d%s", h, min, suffix)
}

// describeDays writes the common runs the way a person would.
func describeDays(days []string) string {
	switch strings.Join(days, ",") {
	case "mon,tue,wed,thu,fri":
		return "weekdays"
	case "sat,sun":
		return "weekends"
	case strings.Join(dayCodes, ","):
		return "every day"
	}
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = strings.ToUpper(d[:1]) + d[1:]
	}
	return strings.Join(names, ", ")
}

// formatCents writes 500 as "5" and 550 as "5.50", matching how the board
// already writes prices.
func formatCents(c int) string {
	if c%100 == 0 {
		return strconv.Itoa(c / 100)
	}
	return fmt.Sprintf("%d.%02d", c/100, c%100)
}

// nameKey is the join key for a beer name: case and spacing only.
func nameKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
