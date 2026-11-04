package cogs

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

type lotKey struct{ beer, format uuid.UUID }

// balances sums the standing lots per beer and format.
func (a *actx) balances() (map[lotKey]float64, error) {
	lots, err := listLots(a.ctx, a.tx, a.tenantID, nil, nil)
	if err != nil {
		return nil, err
	}
	out := map[lotKey]float64{}
	for _, l := range lots {
		out[lotKey{l.BeerID, l.FormatID}] += l.Units
	}
	return out, nil
}

// countRow is one line of a count, resolved before anything is written.
type countRow struct {
	in       LineInput
	beer     *Beer
	format   *Format
	counted  float64
	expected float64
}

// runCount records a blind count and resets the lots to it. Every row the
// system expects has to be in it (the form won't save until each is
// touched); the variance goes to the console queue. A count is of now:
// lots have no dates on their moves, so a count dated in the past would
// wipe out whatever was packaged since.
func runCount(a *actx, in ActionInput) error {
	if d := strings.TrimSpace(in.Date); d != "" && d != dateOnly(time.Now().In(a.loc)) {
		return fmt.Errorf("%w: a count is of what's there now, so it can't be backdated", ErrInvalid)
	}
	rows, err := a.countRows(in.Lines)
	if err != nil {
		return err
	}
	var variances []string
	short := 0.0
	for _, r := range rows {
		diff := r.counted - r.expected
		if math.Abs(diff) > 0.001 {
			variances = append(variances, fmt.Sprintf("%s %s: counted %s, expected %s", r.beer.Name, r.format.Name,
				fmtUnits(r.counted, r.format), fmtUnits(r.expected, r.format)))
		}
		short -= diff * r.format.BBLPerUnit
	}
	ev, err := a.emit(Event{Kind: EvCount, LossBBL: short, Details: EventDetails{Note: in.Note}})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := a.applyCountRow(ev, r); err != nil {
			return err
		}
	}
	if len(variances) == 0 {
		return nil
	}
	return addQueue(a.ctx, a.tx, a.tenantID, queueItem{
		Kind: QueueCountVariance, Title: fmt.Sprintf("Count on %s didn't match", a.day()),
		Body: strings.Join(variances, "\n"), ActionID: &a.actionID, Label: a.rec.Label,
	})
}

// countRows resolves the lines, refusing a duplicate row and naming any
// expected row that's missing.
func (a *actx) countRows(lines []LineInput) ([]countRow, error) {
	expected, err := a.balances()
	if err != nil {
		return nil, err
	}
	seen := map[lotKey]bool{}
	rows := make([]countRow, 0, len(lines))
	for _, l := range lines {
		if l.BeerID == nil {
			return nil, fmt.Errorf("%w: every count row needs a beer", ErrInvalid)
		}
		k := lotKey{*l.BeerID, l.FormatID}
		if seen[k] {
			return nil, fmt.Errorf("%w: a beer and format is counted twice", ErrInvalid)
		}
		seen[k] = true
		counted, f, err := a.units(l)
		if err != nil {
			return nil, err
		}
		beer, err := getBeer(a.ctx, a.tx, a.tenantID, *l.BeerID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, countRow{in: l, beer: beer, format: f, counted: counted, expected: expected[k]})
	}
	var missing []string
	for k, units := range expected {
		if math.Abs(units) > 0.001 && !seen[k] {
			missing = append(missing, a.rowName(k))
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: these need a count, even if it's none: %s", ErrInvalid, strings.Join(missing, ", "))
	}
	return rows, nil
}

func (a *actx) rowName(k lotKey) string {
	name := "a beer"
	if b, err := getBeer(a.ctx, a.tx, a.tenantID, k.beer); err == nil {
		name = b.Name
	}
	if f, ok := a.formats[k.format]; ok {
		name += " " + f.Name
	}
	return name
}

func (a *actx) applyCountRow(ev *Event, r countRow) error {
	l := r.in
	entry := fmt.Sprintf(`{"full":%g,"partial":%g,"cases":%g,"loose":%g,"none":%t}`,
		l.Full, l.Partial, l.Cases, l.Loose, l.None)
	if _, err := a.tx.Exec(a.ctx, `INSERT INTO app_cogs_count_lines (tenant_id, event_id, beer_id, format_id,
		counted_units, expected_units, entry) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		a.tenantID, ev.ID, r.beer.ID, r.format.ID, round2(r.counted), round2(r.expected), entry); err != nil {
		return fmt.Errorf("recording count line: %w", err)
	}
	diff := r.counted - r.expected
	switch {
	case diff < -0.001:
		return drawFIFO(a.ctx, a.tx, a.tenantID, ev, r.beer.ID, r.format.ID, nil, -diff, a.day())
	case diff > 0.001:
		return addToNewest(a.ctx, a.tx, a.tenantID, ev, r.beer.ID, r.format.ID, diff, a.day())
	}
	return nil
}
