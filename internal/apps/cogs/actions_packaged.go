package cogs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrForbidden is an action this caller may not take (a day-one load from
// the iPad).
var ErrForbidden = errors.New("not allowed")

// Out-not-sold reasons.
const (
	ReasonDumped   = "dumped"
	ReasonEvent    = "event"
	ReasonCameBack = "came_back"
	ReasonOther    = "other"
)

// packagedLines resolves lines that each name their own beer.
func (a *actx) packagedLines(in []LineInput, fallbackBeer *uuid.UUID) ([]DetailLine, error) {
	var out []DetailLine
	for _, l := range in {
		beerID := l.BeerID
		if beerID == nil {
			beerID = fallbackBeer
		}
		if beerID == nil {
			return nil, fmt.Errorf("%w: pick the beer", ErrInvalid)
		}
		beer, err := getBeer(a.ctx, a.tx, a.tenantID, *beerID)
		if err != nil {
			return nil, err
		}
		u, f, err := a.units(l)
		if err != nil {
			return nil, err
		}
		if u <= 0 {
			continue
		}
		out = append(out, DetailLine{BeerID: beer.ID, BeerName: beer.Name, FormatID: f.ID, FormatName: f.Name,
			Units: u, BBL: round3(u * f.BBLPerUnit)})
	}
	return out, nil
}

func runOutNotSold(a *actx, in ActionInput) error {
	switch in.Reason {
	case ReasonDumped, ReasonEvent, ReasonCameBack, ReasonOther:
	default:
		return fmt.Errorf("%w: pick a reason", ErrInvalid)
	}
	lines, err := a.packagedLines(in.Lines, in.BeerID)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return fmt.Errorf("%w: how many?", ErrInvalid)
	}
	total := 0.0
	for _, l := range lines {
		total += l.BBL
	}
	ev, err := a.emit(Event{Kind: EvOutNotSold, VolumeBBL: total,
		Details: EventDetails{Reason: in.Reason, Lines: lines, Note: in.Note}})
	if err != nil {
		return err
	}
	for _, l := range lines {
		if in.Reason == ReasonCameBack {
			err = addToNewest(a.ctx, a.tx, a.tenantID, ev, l.BeerID, l.FormatID, l.Units, a.day())
		} else {
			err = drawFIFO(a.ctx, a.tx, a.tenantID, ev, l.BeerID, l.FormatID, nil, l.Units, a.day())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runLoadVessel is the console's day-one load of what is already in a
// vessel. It makes the batch it names.
func runLoadVessel(a *actx, in ActionInput) error {
	if !a.caller.IsAdmin {
		return fmt.Errorf("%w: the day-one load is done from the console", ErrForbidden)
	}
	v, err := a.vessel(in.ToVesselID, "vessel")
	if err != nil {
		return err
	}
	if !v.Empty() {
		return fmt.Errorf("%w: %s already has beer in it", ErrInvalid, v.Name)
	}
	beer, err := a.resolveBeer(in.BeerID, in.NewBeerName)
	if err != nil {
		return err
	}
	if in.VolumeBBL <= 0 {
		return fmt.Errorf("%w: how much is in it?", ErrInvalid)
	}
	brewedOn := strings.TrimSpace(in.BrewedOn)
	if brewedOn == "" {
		brewedOn = a.day()
	}
	source := in.Source
	if source != "acquired" {
		source = "brewed"
	}
	b, err := createBatch(a.ctx, a.tx, a.tenantID, beer.ID, brewedOn, source, nil, in.Note)
	if err != nil {
		return err
	}
	_, err = a.emit(Event{Kind: EvLoadVessel, ToVesselID: &v.ID, BatchID: &b.ID, VolumeBBL: in.VolumeBBL,
		Details: EventDetails{Stage: in.Stage, OnTap: in.OnTap && v.Kind == KindServing, Note: in.Note}})
	return err
}

// runLoadPackaged is the console's day-one load of packaged stock, with
// the batch where it's known.
func runLoadPackaged(a *actx, in ActionInput) error {
	if !a.caller.IsAdmin {
		return fmt.Errorf("%w: the day-one load is done from the console", ErrForbidden)
	}
	beerID := in.BeerID
	if strings.TrimSpace(in.NewBeerName) != "" {
		beer, err := a.resolveBeer(nil, in.NewBeerName)
		if err != nil {
			return err
		}
		beerID = &beer.ID
	}
	lines, err := a.packagedLines(in.Lines, beerID)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return fmt.Errorf("%w: nothing to load", ErrInvalid)
	}
	if in.BatchID != nil {
		// A batch from another workspace, or of another beer, is not found.
		b, err := getBatch(a.ctx, a.tx, a.tenantID, *in.BatchID)
		if err != nil {
			return err
		}
		for _, l := range lines {
			if l.BeerID != b.BeerID {
				return fmt.Errorf("%w: that batch is a different beer", ErrInvalid)
			}
		}
	}
	on := strings.TrimSpace(in.BrewedOn)
	if on == "" {
		on = a.day()
	}
	ev, err := a.emit(Event{Kind: EvLoadPackaged, BatchID: in.BatchID, Details: EventDetails{Lines: lines, Note: in.Note}})
	if err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := addLot(a.ctx, a.tx, a.tenantID, ev, l.BeerID, in.BatchID, l.FormatID, on, l.Units); err != nil {
			return err
		}
	}
	return nil
}
