package squaresales

import (
	"strings"
	"testing"
	"time"
)

func day(date string, netDollars float64, orders int) DayRollup {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		panic(err)
	}
	return DayRollup{
		LocationID: "L1", Date: d, Timezone: "America/Denver", Currency: "USD",
		NetCents: int64(netDollars * 100), OrderCount: orders,
	}
}

// The real Saturdays preceding 2026-08-29, with their real order counts.
func saturdayHistory() []DayRollup {
	return []DayRollup{
		day("2026-07-04", 379.12, 22), day("2026-07-11", 794.10, 41),
		day("2026-07-18", 603.89, 37), day("2026-07-25", 673.70, 39),
		day("2026-08-01", 849.98, 51), day("2026-08-08", 2725.09, 148),
		day("2026-08-15", 723.58, 44), day("2026-08-22", 935.38, 53),
	}
}

func hasKind(f []Finding, k FindingKind) bool {
	for _, x := range f {
		if x.Kind == k {
			return true
		}
	}
	return false
}

// A closed day is a fact, not an anomaly. Flagging it would fire every week
// for any business that takes a day off.
func TestClosedDayIsNotAnAnomaly(t *testing.T) {
	s := Analyze(day("2026-08-31", 0, 0), saturdayHistory(), nil, nil)
	if s.Status != StatusClosed {
		t.Fatalf("status = %s, want closed", s.Status)
	}
	if len(s.Findings) != 0 {
		t.Errorf("closed day produced findings: %v", s.Findings)
	}
	if strings.Contains(FormatDaySummary(s), "$") {
		t.Error("closed-day card should not quote a dollar figure")
	}
}

func TestBaselineBuildingSaysSo(t *testing.T) {
	s := Analyze(day("2026-07-11", 794.10, 41), saturdayHistory()[:2], nil, nil)
	if s.Status != StatusBuilding {
		t.Fatalf("status = %s, want baseline_building", s.Status)
	}
	body := FormatDaySummary(s)
	if !strings.Contains(body, "No baseline yet") {
		t.Errorf("cold-start body must disclaim the number:\n%s", body)
	}
}

func TestAug8FlagsHigh(t *testing.T) {
	hist := saturdayHistory()[:5] // the Saturdays before Aug 8
	s := Analyze(day("2026-08-08", 2725.09, 148), hist, nil, nil)
	if !hasKind(s.Findings, FindingDayHigh) {
		t.Fatalf("Aug 8 should flag high, got %+v", s.Findings)
	}
	card := CardTitle(s) + "\n" + FormatDaySummary(s)
	if !strings.Contains(card, "$2,725.09") {
		t.Errorf("card should quote the day's net:\n%s", card)
	}
	if !strings.Contains(CardTitle(s), "above normal") {
		t.Errorf("the verdict belongs in the title, got %q", CardTitle(s))
	}
}

// The ordinary Saturday must stay silent, or the card is noise again.
func TestAug29ProducesNoDayFinding(t *testing.T) {
	s := Analyze(day("2026-08-29", 807.01, 58), saturdayHistory(), nil, nil)
	if hasKind(s.Findings, FindingDayHigh) || hasKind(s.Findings, FindingDayLow) {
		t.Fatalf("ordinary Saturday should not flag: %+v", s.Findings)
	}
	if !strings.Contains(CardTitle(s), "in line for a Saturday") {
		t.Errorf("quiet day title should carry its verdict, got %q", CardTitle(s))
	}
	if n := strings.Count(FormatDaySummary(s), "\n"); n > 0 {
		t.Errorf("an ordinary day should be one line of body, got:\n%s", FormatDaySummary(s))
	}
}

// An hour is judged against the same hour on the same weekday. A quiet
// afternoon that is ALWAYS quiet must not flag.
func TestDeadHourNeedsAnHourThatNormallyTrades(t *testing.T) {
	target := day("2026-08-29", 807.01, 58)
	hist := saturdayHistory()

	var hours []HourRollup
	// 15:00 normally takes ~$200; today it took $12.
	for _, h := range hist {
		hours = append(hours,
			HourRollup{Date: h.Date, Hour: 15, NetCents: 20000},
			HourRollup{Date: h.Date, Hour: 3, NetCents: 0}, // closed hour, always zero
		)
	}
	hours = append(hours,
		HourRollup{Date: target.Date, Hour: 15, NetCents: 1200},
		HourRollup{Date: target.Date, Hour: 3, NetCents: 0},
	)

	s := Analyze(target, hist, hours, nil)
	if !hasKind(s.Findings, FindingDeadHours) {
		t.Fatalf("3pm collapse should flag: %+v", s.Findings)
	}
	for _, f := range s.Findings {
		if f.Kind == FindingDeadHours && strings.Contains(f.Headline, "3am") {
			t.Error("an hour that never trades must not be reported as dead")
		}
	}
}

// Adjacent dead hours read as one stretch, not three bullets.
func TestAdjacentDeadHoursMerge(t *testing.T) {
	target := day("2026-08-29", 807.01, 58)
	hist := saturdayHistory()
	var hours []HourRollup
	for _, h := range hist {
		for _, hr := range []int{15, 16, 17} {
			hours = append(hours, HourRollup{Date: h.Date, Hour: hr, NetCents: 20000})
		}
	}
	for _, hr := range []int{15, 16, 17} {
		hours = append(hours, HourRollup{Date: target.Date, Hour: hr, NetCents: 500})
	}

	s := Analyze(target, hist, hours, nil)
	var dead int
	for _, f := range s.Findings {
		if f.Kind == FindingDeadHours {
			dead++
			if !strings.Contains(f.Headline, "3pm-6pm") {
				t.Errorf("expected a merged 3pm-6pm span, got %q", f.Headline)
			}
		}
	}
	if dead != 1 {
		t.Errorf("3 adjacent dead hours = %d findings, want 1 merged span", dead)
	}
}

func TestMoverFlagsAnItemThatJumped(t *testing.T) {
	target := day("2026-08-29", 807.01, 58)
	hist := saturdayHistory()
	var items []ItemRollup
	for _, h := range hist {
		items = append(items, ItemRollup{Date: h.Date, Category: "Beer", Item: "Golden Mosaic", NetCents: 6000})
	}
	items = append(items, ItemRollup{Date: target.Date, Category: "Beer", Item: "Golden Mosaic", NetCents: 22000})

	s := Analyze(target, hist, nil, items)
	if !hasKind(s.Findings, FindingMoverUp) {
		t.Fatalf("Golden Mosaic tripling should flag: %+v", s.Findings)
	}
}

// One extra pour is not a story.
func TestMoverIgnoresTinySwings(t *testing.T) {
	target := day("2026-08-29", 807.01, 58)
	hist := saturdayHistory()
	var items []ItemRollup
	for _, h := range hist {
		items = append(items, ItemRollup{Date: h.Date, Category: "Beer", Item: "Coal Kriek", NetCents: 2000})
	}
	items = append(items, ItemRollup{Date: target.Date, Category: "Beer", Item: "Coal Kriek", NetCents: 2900})

	s := Analyze(target, hist, nil, items)
	if hasKind(s.Findings, FindingMoverUp) {
		t.Errorf("a $9 swing should not be a mover: %+v", s.Findings)
	}
}

// Revenue and order count moving opposite ways is a different story from
// either moving alone, and the top line hides it.
func TestDivergenceFlagsFewerBiggerTabs(t *testing.T) {
	hist := []DayRollup{
		day("2026-07-04", 700, 50), day("2026-07-11", 700, 50),
		day("2026-07-18", 700, 50), day("2026-07-25", 700, 50),
		day("2026-08-01", 700, 50), day("2026-08-08", 700, 50),
	}
	s := Analyze(day("2026-08-15", 1000, 30), hist, nil, nil)
	if !hasKind(s.Findings, FindingDivergence) {
		t.Fatalf("revenue up on fewer orders should flag: %+v", s.Findings)
	}
}

func TestFindingsAreCapped(t *testing.T) {
	f := make([]Finding, 10)
	for i := range f {
		f[i] = Finding{PctDelta: float64(i)}
	}
	if got := len(rankFindings(f)); got != maxFindings {
		t.Errorf("rankFindings kept %d, want %d", got, maxFindings)
	}
}

func TestImportantFindingsSortFirst(t *testing.T) {
	got := rankFindings([]Finding{
		{Kind: FindingMoverUp, PctDelta: 9},
		{Kind: FindingDayLow, PctDelta: 0.5, Important: true},
	})
	if got[0].Kind != FindingDayLow {
		t.Errorf("important finding should lead, got %s", got[0].Kind)
	}
}

// The invariant that matters most: a revenue figure never appears without
// its comparison, in any status. This is what separates the new card from
// the recap it replaces.
func TestNoBareRevenueFigureInAnyStatus(t *testing.T) {
	hist := saturdayHistory()
	cases := map[string]DaySummary{
		"ok-quiet":   Analyze(day("2026-08-29", 807.01, 58), hist, nil, nil),
		"ok-flagged": Analyze(day("2026-08-08", 2725.09, 148), hist[:5], nil, nil),
		"building":   Analyze(day("2026-07-11", 794.10, 41), hist[:2], nil, nil),
		"closed":     Analyze(day("2026-08-31", 0, 0), hist, nil, nil),
		"no-data":    {Status: StatusNoData, Day: day("2026-08-31", 0, 0)},
	}
	for name, s := range cases {
		body := CardTitle(s) + "\n" + FormatDaySummary(s)
		if !strings.Contains(body, "$") {
			continue // no figure quoted at all is trivially fine
		}
		hasComparison := strings.Contains(body, "typical") ||
			strings.Contains(body, "baseline") ||
			strings.Contains(body, "usual") ||
			strings.Contains(body, "normal") ||
			strings.Contains(body, "in line") ||
			strings.Contains(body, "against")
		if !hasComparison {
			t.Errorf("%s: quotes a dollar figure with no comparison:\n%s", name, body)
		}
	}
}

func TestSeverity(t *testing.T) {
	hist := saturdayHistory()
	if got := Severity(Analyze(day("2026-08-29", 807.01, 58), hist, nil, nil)); got != "info" {
		t.Errorf("ordinary day severity = %s, want info", got)
	}
	if got := Severity(Analyze(day("2026-08-31", 0, 0), hist, nil, nil)); got != "info" {
		t.Errorf("closed day severity = %s, want info", got)
	}
	flagged := Analyze(day("2026-08-08", 2725.09, 148), hist[:5], nil, nil)
	if got := Severity(flagged); got == "info" {
		t.Errorf("a flagged day should not be info")
	}
}

func TestSameWeekdayDates(t *testing.T) {
	d, _ := time.Parse(time.DateOnly, "2026-08-29") // a Saturday
	got := sameWeekdayDates(d, 3)
	want := []string{"2026-08-08", "2026-08-15", "2026-08-22"}
	if len(got) != len(want) {
		t.Fatalf("got %d dates, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Format(time.DateOnly) != w {
			t.Errorf("date %d = %s, want %s", i, got[i].Format(time.DateOnly), w)
		}
		if got[i].Weekday() != time.Saturday {
			t.Errorf("date %d is a %s, want Saturday", i, got[i].Weekday())
		}
	}
}

func TestMoneyFormatting(t *testing.T) {
	cases := map[int64]string{
		0: "$0.00", 5: "$0.05", 807_01: "$807.01",
		2_725_09: "$2,725.09", 1_234_567_89: "$1,234,567.89", -2862: "-$28.62",
	}
	for cents, want := range cases {
		if got := money(cents); got != want {
			t.Errorf("money(%d) = %q, want %q", cents, got, want)
		}
	}
}

// A dead stretch that cost real money must survive the findings cap even
// when a flashier percentage swing is present.
func TestDollarImpactOutranksPercentage(t *testing.T) {
	got := rankFindings([]Finding{
		{Kind: FindingMoverUp, Value: 220, Baseline: 60, PctDelta: 2.67},
		{Kind: FindingDeadHours, Value: 27, Baseline: 410, PctDelta: -0.93},
	})
	if got[0].Kind != FindingDeadHours {
		t.Errorf("a $383 dead stretch should outrank a $160 mover, got %s first", got[0].Kind)
	}
}

func dayWithComps(date string, netDollars float64, orders int, grossDollars, compsDollars float64) DayRollup {
	d := day(date, netDollars, orders)
	d.GrossCents = int64(grossDollars * 100)
	d.CompsCents = int64(compsDollars * 100)
	return d
}

// Comps are a lever the owner sets on purpose, so the card reports the rate
// every day whether or not it moved.
func TestCompRateAlwaysReported(t *testing.T) {
	hist := make([]DayRollup, 0, 8)
	for _, d := range saturdayHistory() {
		hist = append(hist, dayWithComps(d.Date.Format(time.DateOnly),
			float64(d.NetCents)/100, d.OrderCount, float64(d.NetCents)/100*1.05, float64(d.NetCents)/100*0.04))
	}
	s := Analyze(dayWithComps("2026-08-29", 807.01, 58, 847.36, 35.0), hist, nil, nil)
	body := FormatDaySummary(s)
	if !strings.Contains(body, "Comps $35.00") {
		t.Errorf("comp line missing:\n%s", body)
	}
	if !strings.Contains(body, "usual") {
		t.Errorf("comp line must carry its comparison:\n%s", body)
	}
}

// A real change in posture flags; a normal level does not.
func TestCompRateShiftFlags(t *testing.T) {
	hist := make([]DayRollup, 0, 8)
	for _, d := range saturdayHistory() {
		g := float64(d.NetCents) / 100 * 1.05
		hist = append(hist, dayWithComps(d.Date.Format(time.DateOnly),
			float64(d.NetCents)/100, d.OrderCount, g, g*0.04))
	}
	// 4% is the norm; 12% of an $850 day is a deliberate dial-up.
	jumped := Analyze(dayWithComps("2026-08-29", 807.01, 58, 850.0, 102.0), hist, nil, nil)
	if !hasKind(jumped.Findings, FindingComps) {
		t.Errorf("a jump from 4%% to 12%% should flag: %+v", jumped.Findings)
	}
	steady := Analyze(dayWithComps("2026-08-29", 807.01, 58, 850.0, 38.0), hist, nil, nil)
	if hasKind(steady.Findings, FindingComps) {
		t.Errorf("a normal comp rate should not flag: %+v", steady.Findings)
	}
}

// Two free pints on a slow day is not a strategy shift.
func TestCompFlagIgnoresTrivialAmounts(t *testing.T) {
	hist := make([]DayRollup, 0, 8)
	for _, d := range saturdayHistory() {
		g := float64(d.NetCents) / 100 * 1.05
		hist = append(hist, dayWithComps(d.Date.Format(time.DateOnly),
			float64(d.NetCents)/100, d.OrderCount, g, g*0.04))
	}
	s := Analyze(dayWithComps("2026-08-29", 200.0, 12, 210.0, 24.0), hist, nil, nil)
	if hasKind(s.Findings, FindingComps) {
		t.Errorf("$24 of comps should not flag a strategy shift: %+v", s.Findings)
	}
}

func TestResolveCardDate(t *testing.T) {
	got, err := resolveCardDate("2026-08-29")
	if err != nil || got.Format(time.DateOnly) != "2026-08-29" {
		t.Fatalf("got %v, %v", got, err)
	}
	if def, err := resolveCardDate("  "); err != nil || !def.Equal(Yesterday()) {
		t.Errorf("blank should default to yesterday, got %v %v", def, err)
	}
	if _, err := resolveCardDate("29/08/2026"); err == nil {
		t.Error("expected an error for a non-ISO date")
	}
}

// A day is claimed exactly once. Both the scheduled and the manual path
// must stamp it, or a manual post just before the morning run duplicates.
func TestCardDateIsClaimedOnce(t *testing.T) {
	// markCardPosted only clears a NULL stamp, so re-stamping is a no-op
	// and the two paths can both call it safely. Asserted here as a
	// contract note; the SQL carries "AND card_posted_at IS NULL".
	const guard = "card_posted_at IS NULL"
	src, err := readSource("models.go")
	if err != nil {
		t.Skip("source unavailable")
	}
	if !strings.Contains(src, guard) {
		t.Errorf("markCardPosted must only claim an unclaimed date (%q missing)", guard)
	}
}
