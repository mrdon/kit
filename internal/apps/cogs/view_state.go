package cogs

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// State is everything the brewer's screens show, in one read: the tiles,
// this week, packaged stock and the schedule. One request keeps a polled
// tablet simple, and the numbers involved are small.
type State struct {
	Today     string        `json:"today"`
	Vessels   []BoardVessel `json:"vessels"`
	ThisWeek  []WeekItem    `json:"this_week"`
	Packaged  []PackagedRow `json:"packaged"`
	Schedule  []Lane        `json:"schedule"`
	Plan      []PlanEntry   `json:"plan"`
	Beers     []*Beer       `json:"beers"`
	Formats   []*Format     `json:"formats"`
	Settings  Settings      `json:"settings"`
	LastCount *time.Time    `json:"last_count"`
}

// stateData is the raw material the read model is built from.
type stateData struct {
	vessels  []*Vessel
	beers    map[uuid.UUID]*Beer
	batches  map[uuid.UUID]*Batch
	plan     []PlanEntry
	lots     []Lot
	settings Settings
	n        *names
}

func (s *Service) loadStateData(ctx context.Context, tenantID uuid.UUID, today string) (*stateData, error) {
	n, err := newNames(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	d := &stateData{beers: n.beers, n: n}
	if d.vessels, err = listVessels(ctx, s.pool, tenantID, false); err != nil {
		return nil, err
	}
	if d.plan, err = listPlan(ctx, s.pool, tenantID, today); err != nil {
		return nil, err
	}
	if d.lots, err = listLots(ctx, s.pool, tenantID, nil, nil); err != nil {
		return nil, err
	}
	if d.settings, err = loadSettings(ctx, s.pool, tenantID); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, v := range d.vessels {
		if v.BatchID != nil {
			ids = append(ids, *v.BatchID)
		}
	}
	for _, l := range d.lots {
		if l.BatchID != nil {
			ids = append(ids, *l.BatchID)
		}
	}
	if d.batches, err = listBatches(ctx, s.pool, tenantID, ids); err != nil {
		return nil, err
	}
	n.batches = d.batches
	return d, nil
}

// State builds the read model for the caller's clock.
func (s *Service) State(ctx context.Context, caller *services.Caller) (*State, error) {
	if s == nil || s.pool == nil {
		return nil, ErrNotConfigured
	}
	loc := s.location(ctx, caller)
	now := time.Now().In(loc)
	today := dateOnly(now)
	d, err := s.loadStateData(ctx, caller.TenantID, today)
	if err != nil {
		return nil, err
	}
	// Every list goes out as [], never null: an empty brewery is the first
	// thing anyone sees, and the screens read .length on all of them.
	st := &State{Today: today, Plan: d.plan, Settings: d.settings, Beers: []*Beer{}}
	st.Vessels = d.tiles(now, loc)
	if st.LastCount, err = s.lastCount(ctx, caller.TenantID); err != nil {
		return nil, err
	}
	formats, err := listFormats(ctx, s.pool, caller.TenantID, false)
	if err != nil {
		return nil, err
	}
	st.Formats = formats
	st.Packaged = d.packaged(formats, now, loc)
	st.Schedule = d.schedule(st.Vessels, now, loc)
	st.ThisWeek = d.thisWeek(st, now, loc)
	for _, b := range d.beers {
		st.Beers = append(st.Beers, b)
	}
	sort.Slice(st.Beers, func(i, j int) bool { return st.Beers[i].Name < st.Beers[j].Name })
	return st, nil
}

func (d *stateData) tiles(now time.Time, loc *time.Location) []BoardVessel {
	nextPlan := map[uuid.UUID]*PlanEntry{}
	for i := range d.plan {
		p := &d.plan[i]
		if p.BatchID != nil {
			continue
		}
		if _, ok := nextPlan[p.VesselID]; !ok {
			nextPlan[p.VesselID] = p
		}
	}
	onTapOf := map[uuid.UUID]string{}
	for _, v := range d.vessels {
		if v.OnTap && v.BatchID != nil {
			if b := d.batches[*v.BatchID]; b != nil {
				onTapOf[b.BeerID] = v.Name
			}
		}
	}
	out := make([]BoardVessel, 0, len(d.vessels))
	for _, v := range d.vessels {
		in := tileInputs{v: v, onTapOf: onTapOf, now: now, loc: loc, plan: nextPlan[v.ID]}
		if in.plan != nil {
			if b := d.beers[in.plan.BeerID]; b != nil {
				in.planFor = b.Name
			}
		}
		if v.BatchID != nil {
			in.batch = d.batches[*v.BatchID]
			if in.batch != nil {
				in.beer = d.beers[in.batch.BeerID]
				in.code = d.n.batchCode(in.batch)
			}
		}
		out = append(out, buildTile(in))
	}
	return out
}

func (s *Service) lastCount(ctx context.Context, tenantID uuid.UUID) (*time.Time, error) {
	var at *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT MAX(e.occurred_at) FROM app_cogs_events e
		WHERE e.tenant_id = $1 AND e.kind = 'count' AND `+standingSQL, tenantID).Scan(&at); err != nil {
		return nil, fmt.Errorf("loading last count: %w", err)
	}
	return at, nil
}

// PackagedRow is one beer in one format, as the system calculates it.
type PackagedRow struct {
	BeerID     uuid.UUID `json:"beer_id"`
	Beer       string    `json:"beer"`
	FormatID   uuid.UUID `json:"format_id"`
	Format     string    `json:"format"`
	FormatKind string    `json:"format_kind"`
	Units      float64   `json:"units"`
	BBL        float64   `json:"bbl"`
	OldestOn   string    `json:"oldest_on,omitempty"`
	AgeWeeks   float64   `json:"age_weeks"`
	PastShelf  bool      `json:"past_shelf"`
	Lots       []LotRow  `json:"lots"`
}

// LotRow is a lot inside a packaged row, for the console's drill-down.
type LotRow struct {
	BatchCode  string  `json:"batch_code"`
	PackagedOn string  `json:"packaged_on"`
	Units      float64 `json:"units"`
}

func (d *stateData) packaged(formats []*Format, now time.Time, loc *time.Location) []PackagedRow {
	fm := map[uuid.UUID]*Format{}
	pos := map[uuid.UUID]int{}
	for i, f := range formats {
		fm[f.ID] = f
		pos[f.ID] = i
	}
	rows := map[lotKey]*PackagedRow{}
	var order []lotKey
	for _, l := range d.lots {
		if l.Units > -0.001 && l.Units < 0.001 {
			continue
		}
		f, beer := fm[l.FormatID], d.beers[l.BeerID]
		if f == nil || beer == nil {
			continue
		}
		k := lotKey{l.BeerID, l.FormatID}
		r, ok := rows[k]
		if !ok {
			r = &PackagedRow{BeerID: beer.ID, Beer: beer.Name, FormatID: f.ID, Format: f.Name, FormatKind: f.Kind}
			rows[k] = r
			order = append(order, k)
		}
		r.Units += l.Units
		r.BBL += l.Units * f.BBLPerUnit
		r.Lots = append(r.Lots, LotRow{BatchCode: d.n.batchCode(d.batches[ptrID(l.BatchID)]),
			PackagedOn: dateKey(l.PackagedOn), Units: l.Units})
		if l.Units > 0 && r.OldestOn == "" {
			r.OldestOn = dateKey(l.PackagedOn)
			r.AgeWeeks = float64(daysBetween(localDate(l.PackagedOn, loc), now, loc)) / 7
			shelf := beer.PkgShelfWeeks
			if f.Kind == FormatKeg {
				shelf = beer.KegShelfWeeks
			}
			r.PastShelf = shelf > 0 && r.AgeWeeks > shelf
		}
	}
	out := make([]PackagedRow, 0, len(order))
	for _, k := range order {
		r := rows[k]
		r.Units, r.BBL = round2(r.Units), round3(r.BBL)
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Beer != out[j].Beer {
			return out[i].Beer < out[j].Beer
		}
		return pos[out[i].FormatID] < pos[out[j].FormatID]
	})
	return out
}

func ptrID(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}
