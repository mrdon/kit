package squaresales

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Bucket- and item-level thresholds. The day-level gates live in stats.go.
const (
	// An hour is "dead" only against ITS OWN same-weekday-same-hour
	// baseline. Comparing against the day's average hour would flag every
	// afternoon forever: a Saturday 2pm is supposed to be quieter than a
	// Saturday 8pm, and that is not news.
	deadHourRatio = 0.35
	deadHourZ     = -2.5

	// An hour that is normally near-dead cannot go dead. This also stands
	// in for opening hours: an hour outside trading never accumulates a
	// median above the floor, so it can never be flagged -- and it adapts
	// on its own if the taproom changes its hours, which a hardcoded
	// schedule would not.
	deadHourMinMedianCents = 6000

	busyHourRatio         = 2.0
	busyHourMinDeltaCents = 15000

	// Adjacent dead hours merge into one span, and we report at most two.
	// "2-4pm ran dead" is a fact; three separate bullets about it is the
	// padding this card exists to avoid.
	maxDeadSpans = 2

	// A mover must move real money. Without this, one extra pour of a
	// slow-selling beer reads as a 200% swing.
	moverMinDeltaCents = 4000
	maxMovers          = 2

	// Orders and revenue moving in opposite directions by this much is a
	// different story from either moving alone.
	divergencePct = 0.15

	// Comps are a deliberate lever -- dialled up to build goodwill, dialled
	// back when margin is tight -- so the card reports the RATE every day
	// and only flags a move. Three points of gross is a real change in
	// posture; the dollar floor stops a slow day flagging on two free
	// pints.
	compRateDeltaPoints = 0.03
	compMinFlagCents    = 5000

	// Hard cap on findings. The card is meant to be read in five seconds.
	maxFindings = 3
)

// FindingKind identifies what was noticed, for tests and for severity.
type FindingKind string

const (
	FindingDayHigh    FindingKind = "day_high"
	FindingDayLow     FindingKind = "day_low"
	FindingDeadHours  FindingKind = "dead_hours"
	FindingBusyHour   FindingKind = "busy_hour"
	FindingMoverUp    FindingKind = "mover_up"
	FindingMoverDown  FindingKind = "mover_down"
	FindingDivergence FindingKind = "divergence"
	FindingComps      FindingKind = "comps"
)

// Finding is one thing worth saying. Headline is printed verbatim; the
// numeric fields exist so tests assert on arithmetic rather than prose.
type Finding struct {
	Kind      FindingKind
	Headline  string
	Value     float64
	Baseline  float64
	PctDelta  float64
	RobustZ   float64
	Important bool
}

// Status describes why a day may have nothing to say.
type Status string

const (
	StatusOK       Status = "ok"
	StatusBuilding Status = "baseline_building"
	StatusClosed   Status = "closed"
	StatusNoData   Status = "no_data"
)

// DaySummary is the whole contract between the analysis and the card.
type DaySummary struct {
	Day         DayRollup
	Status      Status
	Baseline    Baseline
	HasBaseline bool
	Findings    []Finding

	// Comp rate is carried separately from Findings because it is
	// reported every day, flagged or not: the point is to watch the trend,
	// not to be told once when it jumps.
	CompRate        float64
	CompBaseline    Baseline
	HasCompBaseline bool

	// Note explains a non-OK status in one sentence.
	Note string
}

// compRate is comps as a fraction of gross sales, 0 when nothing was sold.
func compRate(d DayRollup) float64 {
	if d.GrossCents == 0 {
		return 0
	}
	return float64(d.CompsCents) / float64(d.GrossCents)
}

// Analyze turns one business day plus its history into a summary.
//
// history must contain the SAME-WEEKDAY days preceding target (the caller
// selects them); hours and items must cover the target date and those same
// history dates. Pure: no DB, no clock, no network, so every threshold is
// reachable from a table-driven test.
func Analyze(target DayRollup, history []DayRollup, hours []HourRollup, items []ItemRollup) DaySummary {
	s := DaySummary{Day: target, Status: StatusOK}

	if target.OrderCount == 0 && target.NetCents == 0 {
		s.Status = StatusClosed
		s.Note = "Closed — no sales recorded. Excluded from every comparison."
		return s
	}

	samples := make([]float64, 0, len(history))
	for _, h := range history {
		if h.Open() {
			samples = append(samples, cents(h.NetCents))
		}
	}
	base, ok := newBaseline(samples)
	s.Baseline, s.HasBaseline = base, ok
	if !ok {
		s.Status = StatusBuilding
		s.Note = fmt.Sprintf("Not enough history yet: %d of %d prior %ss recorded.",
			base.N, minBaselineSamples, target.Date.Weekday())
		return s
	}

	s.CompRate = compRate(target)
	compSamples := make([]float64, 0, len(history))
	for _, h := range history {
		if h.Open() {
			compSamples = append(compSamples, compRate(h))
		}
	}
	s.CompBaseline, s.HasCompBaseline = newBaseline(compSamples)

	s.Findings = rankFindings(append(append(append(append(
		analyzeDay(target, base),
		analyzeDivergence(target, history, base)...),
		analyzeHours(target, hours)...),
		analyzeMovers(target, items)...),
		analyzeComps(target, s.CompRate, s.CompBaseline, s.HasCompBaseline)...))
	return s
}

// analyzeDay flags the day itself against its same-weekday baseline.
func analyzeDay(target DayRollup, base Baseline) []Finding {
	v := cents(target.NetCents)
	if !base.fires(v) {
		return nil
	}
	z, pct := base.robustZ(v), base.pctDelta(v)
	kind, word := FindingDayHigh, "above"
	if pct < 0 {
		kind, word = FindingDayLow, "below"
	}
	f := Finding{
		Kind: kind, Value: v, Baseline: base.Median, PctDelta: pct, RobustZ: z,
		// A thin baseline may state the case but never escalate: with five
		// samples we are guessing confidently, which is the worst mode.
		Important: math.Abs(z) >= importantZ && !base.thin(),
	}
	f.Headline = fmt.Sprintf("%s normal: %s, %.0f%% %s a typical %s (%s)",
		title(word), money(target.NetCents), math.Abs(pct)*100, word,
		target.Date.Weekday(), money(int64(base.Median*100)))
	if base.thin() {
		f.Headline += fmt.Sprintf(" — thin baseline, only %d prior %ss", base.N, target.Date.Weekday())
	}
	return []Finding{f}
}

// analyzeDivergence flags orders and revenue moving opposite ways.
//
// More people spending less and fewer people spending more are different
// problems with different responses, and the top-line number hides both.
// This subsumes average-ticket drift: average ticket IS this ratio.
func analyzeDivergence(target DayRollup, history []DayRollup, netBase Baseline) []Finding {
	counts := make([]float64, 0, len(history))
	for _, h := range history {
		if h.Open() {
			counts = append(counts, float64(h.OrderCount))
		}
	}
	countBase, ok := newBaseline(counts)
	if !ok || countBase.Median == 0 {
		return nil
	}
	netPct := netBase.pctDelta(cents(target.NetCents))
	cntPct := countBase.pctDelta(float64(target.OrderCount))
	if math.Abs(netPct) < divergencePct || math.Abs(cntPct) < divergencePct {
		return nil
	}
	if (netPct > 0) == (cntPct > 0) {
		return nil // moving together is just a busy or quiet day
	}
	phrase := "more visits, smaller tabs"
	if netPct > 0 {
		phrase = "fewer visits, bigger tabs"
	}
	return []Finding{{
		Kind: FindingDivergence, PctDelta: netPct,
		Headline: fmt.Sprintf("%s: %d orders (%+.0f%%) but %s (%+.0f%%), average ticket %s",
			title(phrase), target.OrderCount, cntPct*100, money(target.NetCents), netPct*100,
			money(target.AvgTicketCents())),
	}}
}

// analyzeComps flags a deliberate-looking shift in comping.
//
// It compares the RATE, not the dollar amount: comping $120 on a $2,700
// festival Saturday is the same posture as $30 on a $700 Tuesday, and only
// the rate says which way the dial has moved.
func analyzeComps(target DayRollup, rate float64, base Baseline, ok bool) []Finding {
	if !ok || target.CompsCents < compMinFlagCents {
		return nil
	}
	delta := rate - base.Median
	if math.Abs(delta) < compRateDeltaPoints {
		return nil
	}
	word := "up from"
	if delta < 0 {
		word = "down from"
	}
	return []Finding{{
		Kind: FindingComps, PctDelta: delta,
		Value:    float64(target.CompsCents) / 100,
		Baseline: base.Median * float64(target.GrossCents) / 100,
		Headline: fmt.Sprintf("Comps at %.1f%% of gross (%s), %s a usual %.1f%% for a %s",
			rate*100, money(target.CompsCents), word, base.Median*100, target.Date.Weekday()),
	}}
}

// cents converts integer cents to the float dollars the statistics work in.
func cents(c int64) float64 { return float64(c) / 100 }

// money formats integer cents as dollars with thousands separators. A
// taproom's numbers cross $1,000 often enough that "$2725.09" reads as a
// stumble.
func money(c int64) string {
	neg := c < 0
	if neg {
		c = -c
	}
	whole := strconv.FormatInt(c/100, 10)
	var b strings.Builder
	for i, ch := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := fmt.Sprintf("$%s.%02d", b.String(), c%100)
	if neg {
		return "-" + out
	}
	return out
}

func title(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}

// rankFindings puts important findings first, then the biggest DOLLAR
// swings, and caps the list so the card stays short.
//
// Dollars rather than percentages: ranking by percentage let a $160 item
// mover outrank a dead afternoon that cost $383, and the cap then dropped
// the dead afternoon entirely. Percentage is how a deviation is judged;
// money is how it is prioritised.
func rankFindings(f []Finding) []Finding {
	swing := func(x Finding) float64 { return math.Abs(x.Value - x.Baseline) }
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Important != f[j].Important {
			return f[i].Important
		}
		return swing(f[i]) > swing(f[j])
	})
	if len(f) > maxFindings {
		f = f[:maxFindings]
	}
	return f
}

// timeOnly renders an hour as a compact clock label ("3pm").
func timeOnly(hour int) string {
	suffix := "am"
	h := hour
	if h >= 12 {
		suffix = "pm"
	}
	if h > 12 {
		h -= 12
	}
	if h == 0 {
		h = 12
	}
	return fmt.Sprintf("%d%s", h, suffix)
}
