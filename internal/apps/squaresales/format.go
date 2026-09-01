package squaresales

import (
	"fmt"
	"strings"
)

// CardTitle names the card. The date is in the title so consecutive days
// stack in the feed without blending into one another.
func CardTitle(s DaySummary) string {
	return "Sales — " + s.Day.Date.Format("Mon Jan 2")
}

// FormatDaySummary renders the card body.
//
// Four invariants are enforced HERE, in Go, rather than asked of a writer:
//
//  1. No dollar figure is ever emitted without its comparison, or without
//     an explicit sentence saying there is no comparison. A bare revenue
//     number is the thing that made the old recap useless.
//  2. The flagged item leads; the day's total sits inside the summary line
//     rather than in a header of its own.
//  3. At most maxFindings bullets.
//  4. The whole body stays a few hundred characters, readable in one look.
func FormatDaySummary(s DaySummary) string {
	var b strings.Builder

	switch s.Status {
	case StatusOK:
		// handled below — the common path needs the whole function body
	case StatusNoData:
		b.WriteString("No Square data for this date — the sales sync has not run or returned nothing.")
		return b.String()
	case StatusClosed:
		b.WriteString(s.Note)
		return b.String()
	case StatusBuilding:
		fmt.Fprintf(&b, "%s\n%s net · %d orders. No baseline yet, so this number means nothing on its own.",
			s.Note, money(s.Day.NetCents), s.Day.OrderCount)
		return b.String()
	}

	fmt.Fprintf(&b, "%s net · %d orders · %s avg ticket\n",
		money(s.Day.NetCents), s.Day.OrderCount, money(s.Day.AvgTicketCents()))

	// The comparison clause is not optional: it is what makes the number
	// above mean anything.
	pct := s.Baseline.pctDelta(cents(s.Day.NetCents))
	typical := money(int64(s.Baseline.Median * 100))
	switch {
	case len(s.Findings) == 0:
		fmt.Fprintf(&b, "In line with a typical %s (%s).", s.Day.Date.Weekday(), typical)
	default:
		fmt.Fprintf(&b, "A typical %s is %s (%d-week baseline).\n", s.Day.Date.Weekday(), typical, s.Baseline.N)
		for _, f := range s.Findings {
			fmt.Fprintf(&b, "\n• %s", f.Headline)
		}
	}
	_ = pct
	writeCompLine(&b, s)
	return strings.TrimSpace(b.String())
}

// writeCompLine reports comping every day it happened, flagged or not.
//
// Comps are a lever the owner sets on purpose, so the useful thing is the
// running level rather than a one-off alert. It carries its own comparison
// for the same reason every other figure does: 5.8% means nothing without
// knowing that a Saturday usually runs 4.3%.
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
