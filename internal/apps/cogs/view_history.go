package cogs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// names resolves ids to what the brewer calls them, loading each table
// once per description.
type names struct {
	ctx      context.Context
	q        querier
	tenantID uuid.UUID
	vessels  map[uuid.UUID]*Vessel
	beers    map[uuid.UUID]*Beer
	batches  map[uuid.UUID]*Batch
	formats  map[uuid.UUID]*Format
}

func newNames(ctx context.Context, q querier, tenantID uuid.UUID) (*names, error) {
	n := &names{ctx: ctx, q: q, tenantID: tenantID, vessels: map[uuid.UUID]*Vessel{},
		beers: map[uuid.UUID]*Beer{}, batches: map[uuid.UUID]*Batch{}, formats: map[uuid.UUID]*Format{}}
	vs, err := listVessels(ctx, q, tenantID, true)
	if err != nil {
		return nil, err
	}
	for _, v := range vs {
		n.vessels[v.ID] = v
	}
	bs, err := listBeers(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		n.beers[b.ID] = b
	}
	fs, err := listFormats(ctx, q, tenantID, true)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		n.formats[f.ID] = f
	}
	return n, nil
}

func (n *names) vessel(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	if v, ok := n.vessels[*id]; ok {
		return v.Name
	}
	return "a vessel"
}

func (n *names) batch(id *uuid.UUID) *Batch {
	if id == nil {
		return nil
	}
	if b, ok := n.batches[*id]; ok {
		return b
	}
	b, err := getBatch(n.ctx, n.q, n.tenantID, *id)
	if err != nil {
		return nil
	}
	n.batches[*id] = b
	return b
}

func (n *names) beerOf(batchID *uuid.UUID) string {
	b := n.batch(batchID)
	if b == nil {
		return "beer"
	}
	if beer, ok := n.beers[b.BeerID]; ok {
		return beer.Name
	}
	return "beer"
}

// batchCode is "LL-07".
func (n *names) batchCode(b *Batch) string {
	if b == nil {
		return ""
	}
	code := "B"
	if beer, ok := n.beers[b.BeerID]; ok && beer.Code != "" {
		code = beer.Code
	}
	return fmt.Sprintf("%s-%02d", code, b.Number)
}

// describeAction is the one line an action reads as, everywhere: the
// confirmation banner, the tank history, the console and the agent.
func describeAction(ctx context.Context, q querier, tenantID uuid.UUID, events []Event) (string, error) {
	n, err := newNames(ctx, q, tenantID)
	if err != nil {
		return "", err
	}
	return n.describe(events), nil
}

func (n *names) describe(events []Event) string {
	var parts []string
	var transfers []Event
	for _, ev := range events {
		if ev.Kind == EvTransfer {
			transfers = append(transfers, ev)
			continue
		}
		if s := n.describeEvent(ev, events); s != "" {
			parts = append(parts, s)
		}
	}
	if len(transfers) > 0 {
		parts = append([]string{n.describeTransfers(transfers)}, parts...)
	}
	return strings.Join(parts, ". ")
}

func (n *names) describeTransfers(ts []Event) string {
	first := ts[0]
	beer := n.beerOf(first.BatchID)
	if len(ts) == 1 {
		s := fmt.Sprintf("Moved %s bbl of %s from %s to %s", bbl(first.VolumeBBL), beer, n.vessel(first.VesselID),
			n.vessel(first.ToVesselID))
		if first.LossBBL > tolerance {
			s += fmt.Sprintf(" (%s bbl written off)", bbl(first.LossBBL))
		}
		return s
	}
	var dests []string
	total := 0.0
	for _, t := range ts {
		dests = append(dests, n.vessel(t.ToVesselID))
		total += t.VolumeBBL
	}
	return fmt.Sprintf("Moved %s bbl of %s from %s to %s", bbl(total), beer, n.vessel(first.VesselID),
		strings.Join(dests, ", "))
}

func (n *names) describeEvent(ev Event, all []Event) string {
	v, beer := n.vessel(ev.VesselID), n.beerOf(ev.BatchID)
	switch ev.Kind {
	case EvBrew:
		return fmt.Sprintf("Brewed %s into %s, %s bbl", beer, n.vessel(ev.ToVesselID), bbl(ev.VolumeBBL))
	case EvClean:
		if len(all) > 1 {
			return "Cleaned " + v + " first"
		}
		return "Cleaned " + v
	case EvDryHop:
		return fmt.Sprintf("Dry hopped %s in %s", beer, v)
	case EvPackage:
		return fmt.Sprintf("Packaged %s of %s from %s (%s bbl)", n.lines(ev.Details.Lines, false), beer, v,
			bbl(ev.VolumeBBL))
	case EvEmpty:
		return v + " is now empty" + writtenOff(ev.LossBBL)
	case EvLoss:
		return fmt.Sprintf("Spilled %s gal of %s in %s", num(ev.Details.Gallons), beer, v)
	case EvDump:
		return fmt.Sprintf("Dumped %s (%s bbl of %s)", v, bbl(ev.LossBBL), beer)
	case EvLevel:
		if ev.LossBBL < 0 && ev.Details.Note != "" {
			return fmt.Sprintf("%s held %s bbl more than recorded", v, bbl(-ev.LossBBL))
		}
		if ev.Details.Reading == nil {
			return "Read the level in " + v
		}
		return fmt.Sprintf("Read %s at %s bbl", v, bbl(*ev.Details.Reading)) + writtenOff(ev.LossBBL)
	case EvKick, EvBlend:
		return n.describeTankChange(ev, v)
	case EvOnTap:
		return fmt.Sprintf("%s on tap from %s", beer, v)
	case EvVariant:
		s := fmt.Sprintf("%s became %s", v, beer)
		if ev.Details.Added != "" {
			s += " (" + ev.Details.Added + ")"
		}
		return s
	case EvRepack:
		return n.describeRepack(ev)
	case EvOutNotSold:
		if ev.Details.Reason == ReasonCameBack {
			return n.lines(ev.Details.Lines, true) + " came back full"
		}
		return fmt.Sprintf("%s out, not sold (%s)", n.lines(ev.Details.Lines, true), reasonLabel(ev.Details.Reason))
	case EvCount:
		return "Counted the packaged beer"
	case EvLoadVessel:
		return fmt.Sprintf("Loaded %s with %s, %s bbl", n.vessel(ev.ToVesselID), beer, bbl(ev.VolumeBBL))
	case EvLoadPackaged:
		return "Loaded " + n.lines(ev.Details.Lines, true)
	}
	return ev.Kind
}

// describeTankChange is a kick or a blend.
func (n *names) describeTankChange(ev Event, v string) string {
	if ev.Kind == EvBlend {
		var codes []string
		for _, p := range ev.Details.Parents {
			id := p.BatchID
			codes = append(codes, n.batchCode(n.batch(&id)))
		}
		return fmt.Sprintf("%s now holds a blend of %s", v, strings.Join(codes, " and "))
	}
	if ev.Details.Unaccounted > tolerance {
		return fmt.Sprintf("%s kicked (%s bbl on the books not accounted for)", v, bbl(ev.Details.Unaccounted))
	}
	return v + " kicked"
}

func (n *names) describeRepack(ev Event) string {
	took := ""
	if t := ev.Details.Took; t != nil {
		took = n.line(*t, true)
	}
	s := fmt.Sprintf("Repacked %s into %s", took, n.lines(ev.Details.Lines, true))
	if ev.Details.Added != "" {
		s += " (" + ev.Details.Added + ")"
	}
	return s + writtenOff(ev.LossBBL)
}

func writtenOff(loss float64) string {
	switch {
	case loss > tolerance:
		return fmt.Sprintf(", %s bbl written off", bbl(loss))
	case loss < -tolerance:
		return fmt.Sprintf(", %s bbl more than expected", bbl(-loss))
	}
	return ""
}

func reasonLabel(r string) string {
	switch r {
	case ReasonDumped:
		return "dumped"
	case ReasonEvent:
		return "event or gift"
	default:
		return "other"
	}
}

func (n *names) lines(ls []DetailLine, withBeer bool) string {
	var parts []string
	for _, l := range ls {
		parts = append(parts, n.line(l, withBeer))
	}
	return strings.Join(parts, ", ")
}

func (n *names) line(l DetailLine, withBeer bool) string {
	f, ok := n.formats[l.FormatID]
	name := l.FormatName
	s := num(l.Units) + " × " + name
	if ok {
		s = fmtUnits(l.Units, f)
	}
	if withBeer {
		s += " " + l.BeerName
	}
	return s
}

// fmtUnits reads a quantity the way it's counted: "14 × ½ bbl",
// "10 cases + 3 × 16oz can".
func fmtUnits(units float64, f *Format) string {
	if f.UnitsPerCase > 0 && units >= float64(f.UnitsPerCase) {
		cases := int(units) / f.UnitsPerCase
		loose := units - float64(cases*f.UnitsPerCase)
		s := fmt.Sprintf("%d %s of %s", cases, plural(cases, "case", "cases"), f.Name)
		if loose > 0.001 {
			s += " + " + num(loose)
		}
		return s
	}
	return num(units) + " × " + f.Name
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// bbl prints a volume to one decimal, two when it's small.
func bbl(v float64) string {
	if v < 1 && v > -1 {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
