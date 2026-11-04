package cogs

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// takePlan is what a repack takes, worked out before anything is written:
// which lots it comes from, the batch that makes, and the date it was
// packaged, which the output keeps so shelf life still counts from it.
type takePlan struct {
	beer       *Beer
	format     *Format
	units      float64
	bbl        float64
	draws      []drawn
	batch      *uuid.UUID
	packagedOn string
}

func (a *actx) planTake(t *LineInput) (*takePlan, error) {
	if t == nil || t.BeerID == nil {
		return nil, fmt.Errorf("%w: pick what was taken", ErrInvalid)
	}
	units, f, err := a.units(*t)
	if err != nil {
		return nil, err
	}
	if units <= 0 {
		return nil, fmt.Errorf("%w: how many were taken?", ErrInvalid)
	}
	beer, err := getBeer(a.ctx, a.tx, a.tenantID, *t.BeerID)
	if err != nil {
		return nil, err
	}
	draws, err := planDraw(a.ctx, a.tx, a.tenantID, beer.ID, f.ID, t.BatchID, units)
	if err != nil {
		return nil, err
	}
	p := &takePlan{beer: beer, format: f, units: units, bbl: units * f.BBLPerUnit, draws: draws, packagedOn: a.day()}
	shares := map[uuid.UUID]float64{}
	var order []uuid.UUID
	for _, d := range draws {
		if d.Lot.ID != uuid.Nil && dateKey(d.Lot.PackagedOn) < p.packagedOn {
			p.packagedOn = dateKey(d.Lot.PackagedOn)
		}
		if d.Lot.BatchID != nil {
			if _, ok := shares[*d.Lot.BatchID]; !ok {
				order = append(order, *d.Lot.BatchID)
			}
			shares[*d.Lot.BatchID] += d.Units * f.BBLPerUnit
		}
	}
	return p, a.takeBatch(p, order, shares)
}

// takeBatch is the batch what was taken belongs to: its one batch, or a
// blend of them when the draw spanned several.
func (a *actx) takeBatch(p *takePlan, order []uuid.UUID, shares map[uuid.UUID]float64) error {
	switch len(order) {
	case 0:
		return nil
	case 1:
		p.batch = &order[0]
		return nil
	}
	parents := make([]BatchShare, 0, len(order))
	oldest := ""
	for _, id := range order {
		parents = append(parents, BatchShare{BatchID: id, BBL: round3(shares[id])})
		b, err := getBatch(a.ctx, a.tx, a.tenantID, id)
		if err != nil {
			return err
		}
		if k := dateKey(b.BrewedOn); oldest == "" || k < oldest {
			oldest = k
		}
	}
	blend, err := a.derivedBatch(p.beer.ID, oldest, "brewed", parents, "blend")
	if err != nil {
		return err
	}
	p.batch = &blend.ID
	return nil
}

// outBatch is the batch the made units belong to: the taken batch for the
// same beer, or a new derived batch for a variant.
func (a *actx) outBatch(p *takePlan, outBeer *Beer, outBBL float64, added string) (*uuid.UUID, error) {
	if outBeer.ID == p.beer.ID {
		return p.batch, nil
	}
	brewedOn, source := p.packagedOn, "brewed"
	var parents []BatchShare
	if p.batch != nil {
		parent, err := getBatch(a.ctx, a.tx, a.tenantID, *p.batch)
		if err != nil {
			return nil, err
		}
		brewedOn, source = dateKey(parent.BrewedOn), parent.Source
		parents = []BatchShare{{BatchID: parent.ID, BBL: round3(outBBL)}}
	}
	derived, err := a.derivedBatch(outBeer.ID, brewedOn, source, parents, added)
	if err != nil {
		return nil, err
	}
	return &derived.ID, nil
}

// runRepack turns packaged units into other packaged units: a ½ broken into
// sixtels, a keg canned off, a sixtel pulled from a ½ and made into a
// variant. What goes in comes off the oldest lot first unless a batch is
// picked; the output keeps the input's packaging date. What wasn't used is
// written off, or with KeepRest goes back as a partial unit.
func runRepack(a *actx, in ActionInput) error {
	p, err := a.planTake(in.Take)
	if err != nil {
		return err
	}
	outBeer := p.beer
	if in.BeerID != nil || strings.TrimSpace(in.NewBeerName) != "" {
		if outBeer, err = a.resolveBeer(in.BeerID, in.NewBeerName); err != nil {
			return err
		}
	}
	lines, outBBL, err := a.detailLines(in.Lines, outBeer)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return fmt.Errorf("%w: what was made?", ErrInvalid)
	}
	batch, err := a.outBatch(p, outBeer, outBBL, in.Added)
	if err != nil {
		return err
	}
	var rest *DetailLine
	if rbbl := p.bbl - outBBL; in.KeepRest && rbbl > eps {
		rest = &DetailLine{BeerID: p.beer.ID, BeerName: p.beer.Name, FormatID: p.format.ID, FormatName: p.format.Name,
			Units: round2(rbbl / p.format.BBLPerUnit), BBL: round3(rbbl)}
		outBBL += rbbl
	}
	took := DetailLine{BeerID: p.beer.ID, BeerName: p.beer.Name, FormatID: p.format.ID, FormatName: p.format.Name,
		Units: p.units, BBL: round3(p.bbl)}
	d := EventDetails{Took: &took, Lines: lines, Added: in.Added, Note: in.Note}
	if rest != nil {
		d.Lines = append(append([]DetailLine(nil), lines...), *rest)
	}
	ev, err := a.emit(Event{Kind: EvRepack, BatchID: batch, VolumeBBL: p.bbl, VolumeInBBL: outBBL,
		LossBBL: p.bbl - outBBL, Details: d})
	if err != nil {
		return err
	}
	if err := applyDraw(a.ctx, a.tx, a.tenantID, ev, p.draws, a.day()); err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := addLot(a.ctx, a.tx, a.tenantID, ev, outBeer.ID, batch, l.FormatID, p.packagedOn, l.Units); err != nil {
			return err
		}
	}
	if rest != nil {
		_, err = addLot(a.ctx, a.tx, a.tenantID, ev, p.beer.ID, p.batch, p.format.ID, p.packagedOn, rest.Units)
	}
	return err
}
