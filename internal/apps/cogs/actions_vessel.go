package cogs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// gallonsPerBBL is a US beer barrel.
const gallonsPerBBL = 31.0

// tolerance is how far a recorded volume may be off before a draw beyond
// it is refused rather than taken as rounding.
const tolerance = 0.05

// resolveBeer picks the beer an action names: an existing one by id, or a
// typed name. A name nobody has seen is saved straight away so the entry
// isn't blocked, and Don gets a queue item to set it up.
func (a *actx) resolveBeer(id *uuid.UUID, newName string) (*Beer, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		if id == nil {
			return nil, fmt.Errorf("%w: pick a beer", ErrInvalid)
		}
		return getBeer(a.ctx, a.tx, a.tenantID, *id)
	}
	b, err := findBeerByName(a.ctx, a.tx, a.tenantID, newName)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	nb := NewBeerDefaults(newName)
	nb.NeedsSetup = true
	bid, err := saveBeer(a.ctx, a.tx, a.tenantID, nil, nb)
	if err != nil {
		return nil, err
	}
	nb.ID = bid
	if err := addQueue(a.ctx, a.tx, a.tenantID, queueItem{
		Kind: QueueNewBeer, Title: "Set up " + newName,
		Body:   a.rec.Label + " logged a beer that wasn't on the list. Check its name, durations and shelf life.",
		BeerID: &bid, ActionID: &a.actionID, Label: a.rec.Label,
	}); err != nil {
		return nil, err
	}
	return &nb, nil
}

// contents loads the batch and beer in a vessel, refusing an empty one.
func (a *actx) contents(v *Vessel) (*Batch, *Beer, error) {
	if v.BatchID == nil {
		return nil, nil, fmt.Errorf("%w: %s is empty", ErrInvalid, v.Name)
	}
	b, err := getBatch(a.ctx, a.tx, a.tenantID, *v.BatchID)
	if err != nil {
		return nil, nil, err
	}
	beer, err := getBeer(a.ctx, a.tx, a.tenantID, b.BeerID)
	if err != nil {
		return nil, nil, err
	}
	return b, beer, nil
}

// cleanIfDirty logs the clean a dirty vessel implies when it's used: a
// forgotten "Cleaned it" never blocks a fill.
func (a *actx) cleanIfDirty(v *Vessel) error {
	if !v.Dirty || !v.Empty() {
		return nil
	}
	_, err := a.emit(Event{Kind: EvClean, VesselID: &v.ID})
	return err
}

func runBrew(a *actx, in ActionInput) error {
	v, err := a.vessel(in.VesselID, "fermenter")
	if err != nil {
		return err
	}
	if v.Kind != KindFermenter {
		return fmt.Errorf("%w: brew into a fermenter, not %s", ErrInvalid, v.Name)
	}
	beer, err := a.resolveBeer(in.BeerID, in.NewBeerName)
	if err != nil {
		return err
	}
	if in.VolumeBBL <= 0 {
		return fmt.Errorf("%w: how much was knocked out?", ErrInvalid)
	}
	var batchID uuid.UUID
	if v.Empty() {
		if err := a.cleanIfDirty(v); err != nil {
			return err
		}
		b, err := createBatch(a.ctx, a.tx, a.tenantID, beer.ID, a.day(), "brewed", nil, in.Note)
		if err != nil {
			return err
		}
		batchID = b.ID
	} else {
		b, _, err := a.contents(v)
		if err != nil {
			return err
		}
		if b.BeerID != beer.ID || dateKey(b.BrewedOn) != a.day() {
			return fmt.Errorf("%w: %s already has beer in it", ErrInvalid, v.Name)
		}
		batchID = b.ID
	}
	if v.Volume+in.VolumeBBL > v.SizeBBL*1.05 {
		return fmt.Errorf("%w: %s holds %.1f bbl", ErrOverdraw, v.Name, v.SizeBBL)
	}
	d := EventDetails{Note: in.Note}
	if in.PhotoID != nil {
		d.PhotoID = in.PhotoID.String()
	}
	if in.PlanEntryID != nil {
		d.PlanEntryID = in.PlanEntryID.String()
	}
	if _, err := a.emit(Event{Kind: EvBrew, ToVesselID: &v.ID, BatchID: &batchID, VolumeBBL: in.VolumeBBL,
		Details: d}); err != nil {
		return err
	}
	return a.linkBrew(batchID, in)
}

// linkBrew attaches the brew-sheet photo and closes the plan entry.
func (a *actx) linkBrew(batchID uuid.UUID, in ActionInput) error {
	if in.PhotoID != nil {
		if _, err := a.tx.Exec(a.ctx, `UPDATE app_cogs_photos SET batch_id = $3
			WHERE tenant_id = $1 AND id = $2`, a.tenantID, *in.PhotoID, batchID); err != nil {
			return fmt.Errorf("attaching brew sheet: %w", err)
		}
	}
	if in.PlanEntryID != nil {
		if _, err := a.tx.Exec(a.ctx, `UPDATE app_cogs_plan_entries SET batch_id = $3, updated_at = NOW()
			WHERE tenant_id = $1 AND id = $2`, a.tenantID, *in.PlanEntryID, batchID); err != nil {
			return fmt.Errorf("closing plan entry: %w", err)
		}
	}
	return nil
}

// runSimple covers the actions that only change a vessel's stage or status.
func runSimple(kind string) actionRunner {
	return func(a *actx, in ActionInput) error {
		v, err := a.vessel(in.VesselID, "tank")
		if err != nil {
			return err
		}
		ev := Event{Kind: kind, VesselID: &v.ID, BatchID: v.BatchID}
		switch kind {
		case EvClean:
			if !v.Empty() {
				return fmt.Errorf("%w: %s still has beer in it", ErrInvalid, v.Name)
			}
		case EvDryHop:
			if v.BatchID == nil {
				return fmt.Errorf("%w: %s is empty", ErrInvalid, v.Name)
			}
		case EvOnTap:
			if v.BatchID == nil || v.Kind != KindServing {
				return fmt.Errorf("%w: only a serving tank with beer in it goes on tap", ErrInvalid)
			}
		}
		_, err = a.emit(ev)
		return err
	}
}

func runLoss(a *actx, in ActionInput) error {
	v, err := a.vessel(in.VesselID, "tank")
	if err != nil {
		return err
	}
	batch, _, err := a.contents(v)
	if err != nil {
		return err
	}
	if in.All {
		left := round3(v.Volume)
		_, err := a.emit(Event{Kind: EvDump, VesselID: &v.ID, BatchID: &batch.ID, LossBBL: left,
			Details: EventDetails{Left: &left, Note: in.Note}})
		return err
	}
	if in.Gallons <= 0 {
		return fmt.Errorf("%w: how many gallons?", ErrInvalid)
	}
	loss := in.Gallons / gallonsPerBBL
	if loss > v.Volume+tolerance {
		return fmt.Errorf("%w: %s only holds %.1f gallons. Tick \"dumped the whole thing\" instead",
			ErrOverdraw, v.Name, v.Volume*gallonsPerBBL)
	}
	_, err = a.emit(Event{Kind: EvLoss, VesselID: &v.ID, BatchID: &batch.ID, LossBBL: loss,
		Details: EventDetails{Gallons: in.Gallons, Note: in.Note}})
	return err
}

func runLevel(a *actx, in ActionInput) error {
	v, err := a.vessel(in.VesselID, "tank")
	if err != nil {
		return err
	}
	batch, _, err := a.contents(v)
	if err != nil {
		return err
	}
	if in.Reading < 0 || in.Reading > v.SizeBBL*1.05 {
		return fmt.Errorf("%w: a reading between 0 and %.1f bbl", ErrInvalid, v.SizeBBL)
	}
	reading := in.Reading
	_, err = a.emit(Event{Kind: EvLevel, VesselID: &v.ID, BatchID: &batch.ID, LossBBL: v.Volume - reading,
		Details: EventDetails{Reading: &reading, Note: in.Note}})
	return err
}

// runKick empties a serving tank that ran dry. What the ledger still had in
// it is kept as unaccounted, not written off: with no sales depleting the
// tank yet, nobody knows how much of it was poured. Another serving tank
// holding the same beer goes on tap in the same action.
func runKick(a *actx, in ActionInput) error {
	v, err := a.vessel(in.VesselID, "serving tank")
	if err != nil {
		return err
	}
	batch, beer, err := a.contents(v)
	if err != nil {
		return err
	}
	left := round3(v.Volume)
	if _, err := a.emit(Event{Kind: EvKick, VesselID: &v.ID, BatchID: &batch.ID,
		Details: EventDetails{Left: &left, Unaccounted: left}}); err != nil {
		return err
	}
	vessels, err := listVessels(a.ctx, a.tx, a.tenantID, false)
	if err != nil {
		return err
	}
	for _, listed := range vessels {
		if listed.ID == v.ID || listed.Kind != KindServing {
			continue
		}
		sib, err := a.vessel(&listed.ID, "serving tank")
		if err != nil {
			return err
		}
		if sib.OnTap || sib.BatchID == nil {
			continue
		}
		b, err := getBatch(a.ctx, a.tx, a.tenantID, *sib.BatchID)
		if err != nil {
			return err
		}
		if b.BeerID != beer.ID {
			continue
		}
		_, err = a.emit(Event{Kind: EvOnTap, VesselID: &sib.ID, BatchID: sib.BatchID})
		return err
	}
	return nil
}

// runVariant turns a vessel's beer into a variant in place (fruiting half
// a batch): a derived batch of the variant beer, linked to its parent.
func runVariant(a *actx, in ActionInput) error {
	v, err := a.vessel(in.VesselID, "tank")
	if err != nil {
		return err
	}
	parent, parentBeer, err := a.contents(v)
	if err != nil {
		return err
	}
	beer, err := a.resolveBeer(in.BeerID, in.NewBeerName)
	if err != nil {
		return err
	}
	if beer.ID == parentBeer.ID {
		return fmt.Errorf("%w: pick the variant it became", ErrInvalid)
	}
	derived, err := a.derivedBatch(beer.ID, dateKey(parent.BrewedOn), parent.Source,
		[]BatchShare{{BatchID: parent.ID, BBL: round3(v.Volume)}}, in.Added)
	if err != nil {
		return err
	}
	_, err = a.emit(Event{Kind: EvVariant, VesselID: &v.ID, BatchID: &derived.ID,
		Details: EventDetails{Added: in.Added, Note: in.Note}})
	return err
}
