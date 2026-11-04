package cogs

import (
	"fmt"

	"github.com/google/uuid"
)

// The actions that take beer out of a vessel in bulk: moves, barrel fills
// and packaging. Each one ends, when the vessel is now empty, by writing
// off what is left; when more came out than the ledger had, the difference
// is recorded as a gain first rather than clamped away.

// writeOffRest empties a vessel after its last draw, writing off what the
// ledger says is still there. Left pins that figure so a later entry dated
// before this one can't change it without the replay noticing.
func (a *actx) writeOffRest(v *Vessel, batchID *uuid.UUID, out float64) error {
	left := round3(v.Volume - out)
	_, err := a.emit(Event{Kind: EvEmpty, VesselID: &v.ID, BatchID: batchID, LossBBL: left,
		Details: EventDetails{Left: &left}})
	return err
}

// gainTo records that a vessel held more than the ledger said: more came
// out of it than was recorded going in. The brewer counted what came out;
// the recorded volume was the estimate.
func (a *actx) gainTo(v *Vessel, batchID *uuid.UUID, total float64) error {
	extra := total - v.Volume
	if extra <= eps {
		return nil
	}
	reading := round3(total)
	if _, err := a.emit(Event{Kind: EvLevel, VesselID: &v.ID, BatchID: batchID, LossBBL: -extra,
		Details: EventDetails{Reading: &reading, Note: "more came out than was recorded"}}); err != nil {
		return err
	}
	v.Volume = total
	return nil
}

// usesAll reports whether taking `out` leaves nothing worth keeping, so the
// vessel is empty whether or not the box was ticked.
func usesAll(v *Vessel, out float64) bool { return out >= v.Volume-eps }

func runTransfer(a *actx, in ActionInput) error {
	from, err := a.vessel(in.VesselID, "tank it came from")
	if err != nil {
		return err
	}
	to, err := a.vessel(in.ToVesselID, "tank it went to")
	if err != nil {
		return err
	}
	if from.ID == to.ID {
		return fmt.Errorf("%w: pick a different tank", ErrInvalid)
	}
	batch, beer, err := a.contents(from)
	if err != nil {
		return err
	}
	out, arrived := in.VolumeBBL, in.VolumeInBBL
	if from.Kind == KindBarrel && out <= 0 {
		out = from.Volume
	}
	if arrived <= 0 {
		arrived = out
	}
	if out <= 0 {
		return fmt.Errorf("%w: how much moved?", ErrInvalid)
	}
	if out > from.Volume+tolerance && !in.Empty {
		return fmt.Errorf("%w: %s only holds %.2f bbl. Tick \"is now empty\" if this emptied it",
			ErrOverdraw, from.Name, from.Volume)
	}
	empty := in.Empty || usesAll(from, out)
	if err := a.checkDestination(to, beer, arrived); err != nil {
		return err
	}
	if err := a.cleanIfDirty(to); err != nil {
		return err
	}
	if err := a.gainTo(from, &batch.ID, out); err != nil {
		return err
	}
	destBefore := to.VesselState
	if _, err := a.emit(Event{Kind: EvTransfer, VesselID: &from.ID, ToVesselID: &to.ID, BatchID: &batch.ID,
		VolumeBBL: out, VolumeInBBL: arrived, LossBBL: out - arrived, Details: EventDetails{Note: in.Note}}); err != nil {
		return err
	}
	if destBefore.BatchID != nil && *destBefore.BatchID != batch.ID {
		if err := a.blendInto(to, destBefore, batch, arrived); err != nil {
			return err
		}
	}
	if empty {
		return a.writeOffRest(from, &batch.ID, out)
	}
	return nil
}

// blendInto records the same beer from another batch going into a vessel
// that already held some: the vessel now holds a blend batch whose parents
// are both, with how much of each, so either can still be traced.
func (a *actx) blendInto(to *Vessel, before VesselState, incoming *Batch, arrived float64) error {
	held, err := getBatch(a.ctx, a.tx, a.tenantID, *before.BatchID)
	if err != nil {
		return err
	}
	brewedOn, source := held.BrewedOn, held.Source
	if incoming.BrewedOn.Before(brewedOn) {
		brewedOn = incoming.BrewedOn
	}
	if incoming.Source != source {
		source = "brewed"
	}
	parents := []BatchShare{{BatchID: held.ID, BBL: round3(before.Volume)}, {BatchID: incoming.ID, BBL: round3(arrived)}}
	blend, err := a.derivedBatch(held.BeerID, dateKey(brewedOn), source, parents, "blend")
	if err != nil {
		return err
	}
	_, err = a.emit(Event{Kind: EvBlend, VesselID: &to.ID, BatchID: &blend.ID, Details: EventDetails{Parents: parents}})
	return err
}

// checkDestination refuses a different beer (blends of different beers
// aren't modelled) and a fill past the vessel's size.
func (a *actx) checkDestination(to *Vessel, beer *Beer, arriving float64) error {
	if !to.Empty() {
		_, toBeer, err := a.contents(to)
		if err != nil {
			return err
		}
		if toBeer.ID != beer.ID {
			return fmt.Errorf("%w: %s has %s in it. Blending different beers isn't tracked; empty it first",
				ErrBlend, to.Name, toBeer.Name)
		}
	}
	if to.Volume+arriving > to.SizeBBL*1.05 {
		return fmt.Errorf("%w: %s only has room for %.1f bbl", ErrOverdraw, to.Name, max(0, to.SizeBBL-to.Volume))
	}
	return nil
}

func runBarrelFill(a *actx, in ActionInput) error {
	from, err := a.vessel(in.VesselID, "tank it came from")
	if err != nil {
		return err
	}
	batch, beer, err := a.contents(from)
	if err != nil {
		return err
	}
	var barrels []*Vessel
	seen := map[uuid.UUID]bool{}
	total := 0.0
	for i := range in.ToVessels {
		if seen[in.ToVessels[i]] {
			continue
		}
		seen[in.ToVessels[i]] = true
		to, err := a.vessel(&in.ToVessels[i], "barrel")
		if err != nil {
			return err
		}
		if to.Kind != KindBarrel || !to.Empty() {
			return fmt.Errorf("%w: %s isn't an empty barrel", ErrInvalid, to.Name)
		}
		barrels = append(barrels, to)
		total += to.SizeBBL
	}
	if len(barrels) == 0 {
		return fmt.Errorf("%w: pick the barrels", ErrInvalid)
	}
	if total > from.Volume+tolerance && !in.Empty {
		return fmt.Errorf("%w: %d barrels take %.1f bbl and %s holds %.2f", ErrOverdraw, len(barrels),
			total, from.Name, from.Volume)
	}
	if err := a.gainTo(from, &batch.ID, total); err != nil {
		return err
	}
	for _, to := range barrels {
		if err := a.checkDestination(to, beer, to.SizeBBL); err != nil {
			return err
		}
		if err := a.cleanIfDirty(to); err != nil {
			return err
		}
		if _, err := a.emit(Event{Kind: EvTransfer, VesselID: &from.ID, ToVesselID: &to.ID, BatchID: &batch.ID,
			VolumeBBL: to.SizeBBL, VolumeInBBL: to.SizeBBL}); err != nil {
			return err
		}
	}
	if in.Empty || usesAll(from, total) {
		return a.writeOffRest(from, &batch.ID, total)
	}
	return nil
}

func runPackage(a *actx, in ActionInput) error {
	v, err := a.vessel(in.VesselID, "tank")
	if err != nil {
		return err
	}
	batch, beer, err := a.contents(v)
	if err != nil {
		return err
	}
	lines, bbl, err := a.detailLines(in.Lines, beer)
	if err != nil {
		return err
	}
	if bbl <= 0 {
		return fmt.Errorf("%w: nothing was counted", ErrInvalid)
	}
	if v.Kind == KindServing {
		for _, l := range lines {
			if f := a.formats[l.FormatID]; f != nil && f.Kind != FormatKeg {
				return fmt.Errorf("%w: only kegs are filled from a serving tank", ErrInvalid)
			}
		}
	}
	if bbl > v.Volume+tolerance && !in.Empty {
		return fmt.Errorf("%w: that's %.2f bbl and %s holds %.2f. Tick \"is now empty\" if this emptied it",
			ErrOverdraw, bbl, v.Name, v.Volume)
	}
	empty := in.Empty || usesAll(v, bbl)
	if err := a.gainTo(v, &batch.ID, bbl); err != nil {
		return err
	}
	ev, err := a.emit(Event{Kind: EvPackage, VesselID: &v.ID, BatchID: &batch.ID, VolumeBBL: bbl,
		Details: EventDetails{Lines: lines, LineLossUnits: in.LineLossUnits, Note: in.Note}})
	if err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := addLot(a.ctx, a.tx, a.tenantID, ev, beer.ID, &batch.ID, l.FormatID, a.day(), l.Units); err != nil {
			return err
		}
	}
	if empty {
		return a.writeOffRest(v, &batch.ID, bbl)
	}
	return nil
}

// detailLines resolves form lines into recorded lines of one beer,
// dropping zeros.
func (a *actx) detailLines(in []LineInput, beer *Beer) ([]DetailLine, float64, error) {
	var out []DetailLine
	total := 0.0
	for _, l := range in {
		u, f, err := a.units(l)
		if err != nil {
			return nil, 0, err
		}
		if u <= 0 {
			continue
		}
		bbl := u * f.BBLPerUnit
		out = append(out, DetailLine{BeerID: beer.ID, BeerName: beer.Name, FormatID: f.ID, FormatName: f.Name,
			Units: u, BBL: round3(bbl)})
		total += bbl
	}
	return out, total, nil
}

// derivedBatch makes a batch from other batches (a blend, a variant, a
// repack that drew from several) and records its parents for traceability.
func (a *actx) derivedBatch(beerID uuid.UUID, brewedOn, source string, parents []BatchShare, note string) (*Batch, error) {
	var first *uuid.UUID
	if len(parents) > 0 {
		first = &parents[0].BatchID
	}
	b, err := createBatch(a.ctx, a.tx, a.tenantID, beerID, brewedOn, source, first, note)
	if err != nil {
		return nil, err
	}
	for _, p := range parents {
		if _, err := a.tx.Exec(a.ctx, `INSERT INTO app_cogs_batch_parents (tenant_id, batch_id, parent_batch_id, bbl)
			VALUES ($1, $2, $3, $4)`, a.tenantID, b.ID, p.BatchID, p.BBL); err != nil {
			return nil, fmt.Errorf("recording batch parents: %w", err)
		}
	}
	return b, nil
}
