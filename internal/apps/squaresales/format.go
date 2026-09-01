package squaresales

import (
	"fmt"
	"strings"
)

// CardTitle names the card, and carries the two things worth seeing at a
// glance: the day's takings and whether that was good or bad.
//
// The title renders at 24px where the body renders at 16px, and markdown
// headings inside the body are SMALLER still (0.9em, uppercase — they are
// section labels here, not display type). So the headline number belongs in
// the title; putting it in the body would shrink it.
//
// The date stays so saved cards remain distinguishable.
func CardTitle(s DaySummary) string {
	return fmt.Sprintf("%s — %s", s.Day.Date.Format("Mon Jan 2"), verdict(s))
}

// verdict is the headline clause: the money and its judgement in one
// phrase. Never the figure alone — a bare number is what made the old
// recap useless.
func verdict(s DaySummary) string {
	switch s.Status {
	case StatusNoData:
		return "no sales data"
	case StatusClosed:
		return "closed"
	case StatusBuilding:
		return fmt.Sprintf("%s, no baseline yet", money(s.Day.NetCents))
	case StatusOK:
	}
	for _, f := range s.Findings {
		if f.Kind == FindingDayHigh || f.Kind == FindingDayLow {
			word := "above"
			if f.Kind == FindingDayLow {
				word = "below"
			}
			return fmt.Sprintf("%s, %.0f%% %s normal", money(s.Day.NetCents), absPct(f.PctDelta), word)
		}
	}
	return fmt.Sprintf("%s, in line for a %s", money(s.Day.NetCents), s.Day.Date.Weekday())
}

func absPct(p float64) float64 {
	if p < 0 {
		return -p * 100
	}
	return p * 100
}

// FormatDaySummary renders the card body: the supporting detail, kept
// deliberately thin because the headline already landed in the title.
//
// Three invariants are enforced here, in Go, rather than asked of a writer:
//
//  1. No dollar figure appears without its comparison, or without an
//     explicit sentence saying there is no comparison.
//  2. At most maxFindings bullets, each one line.
//  3. On an ordinary day the body is a single line of metrics — the card
//     should be readable without reading, not a wall of text.
func FormatDaySummary(s DaySummary) string {
	switch s.Status {
	case StatusNoData:
		return "The sales sync has not run for this date, or Square returned nothing."
	case StatusClosed:
		return "No sales recorded. Excluded from every comparison."
	case StatusBuilding:
		return fmt.Sprintf("%s\n\n%d orders · %s avg. No baseline yet, so this figure means nothing on its own.",
			s.Note, s.Day.OrderCount, money(s.Day.AvgTicketCents()))
	case StatusOK:
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d orders · %s avg · typical %s %s",
		s.Day.OrderCount, money(s.Day.AvgTicketCents()),
		s.Day.Date.Weekday(), money(int64(s.Baseline.Median*100)))

	// The day-level finding is already the title; repeating it here is the
	// padding this card exists to avoid.
	//
	// Bullets are written as ONE tight block: a blank line between list
	// items makes markdown render a loose list, which adds a paragraph gap
	// under every bullet and turns three short lines into a wall.
	first := true
	for _, f := range s.Findings {
		if f.Kind == FindingDayHigh || f.Kind == FindingDayLow {
			continue
		}
		if first {
			b.WriteString("\n")
			first = false
		}
		fmt.Fprintf(&b, "\n- %s", f.Headline)
	}

	writeCompLine(&b, s)
	return strings.TrimSpace(b.String())
}

// writeCompLine reports comping every day it happened, flagged or not.
//
// Comps are a lever the owner sets on purpose, so the useful thing is the
// running level rather than a one-off alert. It carries its own comparison
// for the same reason every other figure does: 3.9% means nothing without
// knowing a Monday usually runs 1.7%.
func writeCompLine(b *strings.Builder, s DaySummary) {
	if s.Day.CompsCents == 0 {
		return
	}
	fmt.Fprintf(b, "\n\nComps %s — %.1f%% of gross", money(s.Day.CompsCents), s.CompRate*100)
	if s.HasCompBaseline {
		fmt.Fprintf(b, ", usual %.1f%%", s.CompBaseline.Median*100)
	}
	b.WriteString(".")
}

// Severity maps the summary onto a briefing severity.
//
// info on an ordinary day is deliberate: the card still posts every day
// (the taproom trades seven days a week, so there is always a real number),
// but an ordinary day must not climb the stack above things that need
// attention. Only info-severity cards get an automatic shelf life, which is
// also why the card task sets an explicit TTL.
func Severity(s DaySummary) string {
	if s.Status != StatusOK || len(s.Findings) == 0 {
		return "info"
	}
	for _, f := range s.Findings {
		if f.Important {
			return "important"
		}
	}
	return "notable"
}
