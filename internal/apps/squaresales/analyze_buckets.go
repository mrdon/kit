package squaresales

import (
	"fmt"
	"math"
	"sort"
)

// analyzeHours flags stretches that ran dead, and a standout busy hour.
//
// hours must cover the target date and its same-weekday history. Each hour
// is judged only against the same hour on the same weekday: the question is
// "was 3pm quiet for a Saturday 3pm", never "was 3pm quiet for a Saturday".
func analyzeHours(target DayRollup, hours []HourRollup) []Finding {
	todayBy, historyBy := splitHours(target, hours)
	if len(historyBy) == 0 {
		return nil
	}

	var dead []int
	var busy *Finding
	for hour := range hoursPerDay {
		base, ok := newBaseline(historyBy[hour])
		if !ok || base.Median*100 < deadHourMinMedianCents {
			// Never traded much at this hour, so it cannot "go dead" --
			// this is what keeps closed hours silent without hardcoding
			// opening times, and it follows the taproom if hours change.
			continue
		}
		v := cents(todayBy[hour])
		switch {
		case v <= deadHourRatio*base.Median && base.robustZ(v) <= deadHourZ:
			dead = append(dead, hour)
		case v >= busyHourRatio*base.Median && (v-base.Median)*100 >= busyHourMinDeltaCents:
			if busy == nil || v-base.Median > busy.Value-busy.Baseline {
				busy = &Finding{
					Kind: FindingBusyHour, Value: v, Baseline: base.Median,
					PctDelta: base.pctDelta(v), RobustZ: base.robustZ(v),
					Headline: fmt.Sprintf("Busy hour: %s took %s against a usual %s",
						hourSpan(hour, hour), money(todayBy[hour]), money(int64(base.Median*100))),
				}
			}
		}
	}

	out := deadFindings(dead, todayBy, historyBy)
	if busy != nil {
		out = append(out, *busy)
	}
	return out
}

// splitHours separates the target day's hours from its history, indexing
// both by hour of day.
func splitHours(target DayRollup, hours []HourRollup) (map[int]int64, map[int][]float64) {
	today := make(map[int]int64, hoursPerDay)
	history := make(map[int][]float64, hoursPerDay)
	for _, h := range hours {
		if h.Date.Equal(target.Date) {
			today[h.Hour] = h.NetCents
			continue
		}
		history[h.Hour] = append(history[h.Hour], cents(h.NetCents))
	}
	return today, history
}

// deadFindings merges adjacent dead hours into spans and keeps the biggest.
func deadFindings(dead []int, today map[int]int64, history map[int][]float64) []Finding {
	if len(dead) == 0 {
		return nil
	}
	sort.Ints(dead)
	type span struct{ start, end int }
	spans := []span{{dead[0], dead[0]}}
	for _, h := range dead[1:] {
		if last := &spans[len(spans)-1]; h == last.end+1 {
			last.end = h
			continue
		}
		spans = append(spans, span{h, h})
	}

	out := make([]Finding, 0, len(spans))
	for _, sp := range spans {
		var got, want float64
		for h := sp.start; h <= sp.end; h++ {
			got += cents(today[h])
			want += median(history[h])
		}
		if want == 0 {
			continue
		}
		pct := (got - want) / want
		out = append(out, Finding{
			Kind: FindingDeadHours, Value: got, Baseline: want, PctDelta: pct,
			Headline: fmt.Sprintf("Dead stretch: %s took %s against a usual %s, down %.0f%%",
				hourSpan(sp.start, sp.end), money(int64(got*100)), money(int64(want*100)), math.Abs(pct)*100),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Baseline-out[i].Value > out[j].Baseline-out[j].Value
	})
	if len(out) > maxDeadSpans {
		out = out[:maxDeadSpans]
	}
	return out
}

// hourSpan renders an inclusive hour range as "3-4pm" or "3pm".
func hourSpan(start, end int) string {
	if start == end {
		return fmt.Sprintf("%s-%s", timeOnly(start), timeOnly(start+1))
	}
	return fmt.Sprintf("%s-%s", timeOnly(start), timeOnly(end+1))
}

// analyzeMovers flags items whose sales departed from their own
// same-weekday norm -- "sales were flat but Golden Mosaic doubled" is the
// story the top-line number hides.
func analyzeMovers(target DayRollup, items []ItemRollup) []Finding {
	today, history := splitItems(target, items)
	if len(history) == 0 {
		return nil
	}

	var ups, downs []Finding
	for name, todayCents := range today {
		base, ok := newBaseline(history[name])
		if !ok {
			continue
		}
		v := cents(todayCents)
		delta := (v - base.Median) * 100
		if math.Abs(delta) < moverMinDeltaCents || !base.fires(v) {
			continue
		}
		f := Finding{
			Value: v, Baseline: base.Median, PctDelta: base.pctDelta(v), RobustZ: base.robustZ(v),
		}
		if delta > 0 {
			f.Kind = FindingMoverUp
			f.Headline = fmt.Sprintf("%s took %s, about %.1fx its usual %s",
				name, money(todayCents), v/base.Median, target.Date.Weekday())
			ups = append(ups, f)
		} else {
			f.Kind = FindingMoverDown
			f.Headline = fmt.Sprintf("%s took %s against a usual %s",
				name, money(todayCents), money(int64(base.Median*100)))
			downs = append(downs, f)
		}
	}

	bySwing := func(f []Finding) {
		sort.SliceStable(f, func(i, j int) bool {
			return math.Abs(f[i].Value-f[i].Baseline) > math.Abs(f[j].Value-f[j].Baseline)
		})
	}
	bySwing(ups)
	bySwing(downs)
	return append(trim(ups, maxMovers), trim(downs, 1)...)
}

// splitItems separates the target day's item sales from their history,
// keyed by item name. Items are keyed by name rather than id because the
// name is what the card says out loud, and there is no catalog table to
// resolve an id against.
func splitItems(target DayRollup, items []ItemRollup) (map[string]int64, map[string][]float64) {
	today := make(map[string]int64)
	history := make(map[string][]float64)
	for _, it := range items {
		if it.Date.Equal(target.Date) {
			today[it.Item] += it.NetCents
			continue
		}
		history[it.Item] = append(history[it.Item], cents(it.NetCents))
	}
	return today, history
}

func trim(f []Finding, n int) []Finding {
	if len(f) > n {
		return f[:n]
	}
	return f
}
