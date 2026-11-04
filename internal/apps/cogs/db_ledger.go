package cogs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// standingSQL is true for a row (aliased e) whose action has not been
// reversed. Every read of the ledger's effect goes through it.
const standingSQL = `NOT EXISTS (SELECT 1 FROM app_cogs_events r
	WHERE r.tenant_id = e.tenant_id AND r.reverses_action_id = e.action_id)`

const eventCols = `e.id, e.seq, e.action_id, e.kind, e.occurred_at, e.batch_id, e.vessel_id, e.to_vessel_id,
	e.volume_bbl::float8, e.volume_in_bbl::float8, e.loss_bbl::float8, e.details, e.recorded_by_kind,
	e.recorded_by_id, e.recorded_label, e.reverses_action_id, e.created_at`

func scanEvents(rows pgx.Rows) ([]Event, error) {
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var ev Event
		var details []byte
		if err := rows.Scan(&ev.ID, &ev.Seq, &ev.ActionID, &ev.Kind, &ev.OccurredAt, &ev.BatchID,
			&ev.VesselID, &ev.ToVesselID, &ev.VolumeBBL, &ev.VolumeInBBL, &ev.LossBBL, &details,
			&ev.RecordedByKind, &ev.RecordedByID, &ev.RecordedLabel, &ev.ReversesActionID,
			&ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &ev.Details); err != nil {
				return nil, fmt.Errorf("decoding event details: %w", err)
			}
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// recorder is who an action is attributed to.
type recorder struct {
	Kind  string
	ID    *uuid.UUID
	Label string
}

// insertEvent appends one row to the ledger.
func insertEvent(ctx context.Context, q querier, tenantID uuid.UUID, rec recorder, ev *Event) error {
	details, err := json.Marshal(ev.Details)
	if err != nil {
		return fmt.Errorf("encoding event details: %w", err)
	}
	err = q.QueryRow(ctx, `INSERT INTO app_cogs_events (tenant_id, action_id, kind, occurred_at, batch_id,
		vessel_id, to_vessel_id, volume_bbl, volume_in_bbl, loss_bbl, details, recorded_by_kind,
		recorded_by_id, recorded_label, reverses_action_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id, seq, created_at`,
		tenantID, ev.ActionID, ev.Kind, ev.OccurredAt, ev.BatchID, ev.VesselID, ev.ToVesselID,
		round3(ev.VolumeBBL), round3(ev.VolumeInBBL), round3(ev.LossBBL), details, rec.Kind, rec.ID,
		rec.Label, ev.ReversesActionID).Scan(&ev.ID, &ev.Seq, &ev.CreatedAt)
	if err != nil {
		return fmt.Errorf("inserting %s event: %w", ev.Kind, err)
	}
	ev.RecordedByKind, ev.RecordedByID, ev.RecordedLabel = rec.Kind, rec.ID, rec.Label
	return nil
}

// vesselEvents loads every standing event touching a vessel.
func vesselEvents(ctx context.Context, q querier, tenantID, vesselID uuid.UUID) ([]Event, error) {
	rows, err := q.Query(ctx, `SELECT `+eventCols+` FROM app_cogs_events e
		WHERE e.tenant_id = $1 AND (e.vessel_id = $2 OR e.to_vessel_id = $2)
		AND e.kind <> 'reversal' AND `+standingSQL+`
		ORDER BY e.occurred_at, e.seq`, tenantID, vesselID)
	if err != nil {
		return nil, fmt.Errorf("loading vessel events: %w", err)
	}
	return scanEvents(rows)
}

// refreshVessel replays a vessel's ledger into its cached state. A replay
// that doesn't add up fails, so the transaction that caused it rolls back.
func refreshVessel(ctx context.Context, q querier, tenantID uuid.UUID, v *Vessel) error {
	events, err := vesselEvents(ctx, q, tenantID, v.ID)
	if err != nil {
		return err
	}
	s, err := Replay(v.ID, v.Kind, events)
	if err != nil {
		return fmt.Errorf("%s: %w", v.Name, err)
	}
	if s.Volume < 0 {
		s.Volume = 0
	}
	_, err = q.Exec(ctx, `UPDATE app_cogs_vessels SET batch_id=$3, volume_bbl=$4, stage=$5, stage_at=$6,
		filled_at=$7, dirty=$8, on_tap=$9 WHERE tenant_id=$1 AND id=$2`,
		tenantID, v.ID, s.BatchID, round3(s.Volume), s.Stage, s.StageAt, s.FilledAt, s.Dirty, s.OnTap)
	if err != nil {
		return fmt.Errorf("updating vessel state: %w", err)
	}
	v.VesselState = s
	return nil
}

// vesselStateAt is a vessel's state as of a moment, for an entry dated
// then: a level read yesterday is compared with yesterday's volume, not
// today's.
func vesselStateAt(ctx context.Context, q querier, tenantID uuid.UUID, v *Vessel, at time.Time) (VesselState, error) {
	events, err := vesselEvents(ctx, q, tenantID, v.ID)
	if err != nil {
		return VesselState{}, err
	}
	s, err := ReplayAt(v.ID, v.Kind, events, at)
	if err != nil {
		return VesselState{}, fmt.Errorf("%s: %w", v.Name, err)
	}
	return s, nil
}

// lockTenant serializes brewing writes for one tenant. Volumes are small
// and writes are a few a day, so one lock is simpler than row locks over
// lot balances that are sums.
func lockTenant(ctx context.Context, q querier, tenantID uuid.UUID) error {
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('app_cogs:' || $1::text))`,
		tenantID); err != nil {
		return fmt.Errorf("locking brewing ledger: %w", err)
	}
	return nil
}

const batchCols = `id, beer_id, number, brewed_on, source, parent_batch_id, note`

func getBatch(ctx context.Context, q querier, tenantID, id uuid.UUID) (*Batch, error) {
	var b Batch
	err := q.QueryRow(ctx, `SELECT `+batchCols+` FROM app_cogs_batches WHERE tenant_id=$1 AND id=$2`,
		tenantID, id).Scan(&b.ID, &b.BeerID, &b.Number, &b.BrewedOn, &b.Source, &b.ParentBatchID, &b.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("batch: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("loading batch: %w", err)
	}
	return &b, nil
}

// listBatches loads batches by id, for the read model.
func listBatches(ctx context.Context, q querier, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*Batch, error) {
	out := map[uuid.UUID]*Batch{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+batchCols+` FROM app_cogs_batches
		WHERE tenant_id=$1 AND id = ANY($2)`, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("listing batches: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b Batch
		if err := rows.Scan(&b.ID, &b.BeerID, &b.Number, &b.BrewedOn, &b.Source, &b.ParentBatchID,
			&b.Note); err != nil {
			return nil, fmt.Errorf("scanning batch: %w", err)
		}
		out[b.ID] = &b
	}
	return out, rows.Err()
}

// createBatch numbers the batch after the beer's highest so far.
func createBatch(ctx context.Context, q querier, tenantID, beerID uuid.UUID, brewedOn string,
	source string, parent *uuid.UUID, note string,
) (*Batch, error) {
	on, err := time.Parse("2006-01-02", brewedOn)
	if err != nil {
		return nil, fmt.Errorf("%w: brew date %q", ErrInvalid, brewedOn)
	}
	b := Batch{BeerID: beerID, BrewedOn: on, Source: source, ParentBatchID: parent, Note: note}
	err = q.QueryRow(ctx, `INSERT INTO app_cogs_batches (tenant_id, beer_id, number, brewed_on, source,
		parent_batch_id, note)
		VALUES ($1, $2, COALESCE((SELECT MAX(number) FROM app_cogs_batches WHERE tenant_id=$1 AND beer_id=$2), 0) + 1,
		$3::date, $4, $5, $6) RETURNING id, number`,
		tenantID, beerID, brewedOn, source, parent, note).Scan(&b.ID, &b.Number)
	if err != nil {
		return nil, fmt.Errorf("creating batch: %w", err)
	}
	return &b, nil
}

// Lot is a packaged lot with its standing balance.
type Lot struct {
	ID         uuid.UUID  `json:"id"`
	BeerID     uuid.UUID  `json:"beer_id"`
	BatchID    *uuid.UUID `json:"batch_id"`
	FormatID   uuid.UUID  `json:"format_id"`
	PackagedOn time.Time  `json:"packaged_on"`
	Units      float64    `json:"units"`
}

// listLots returns lots with their standing balances, oldest first,
// optionally narrowed to one beer and format.
func listLots(ctx context.Context, q querier, tenantID uuid.UUID, beerID, formatID *uuid.UUID) ([]Lot, error) {
	rows, err := q.Query(ctx, `SELECT l.id, l.beer_id, l.batch_id, l.format_id, l.packaged_on,
		COALESCE((SELECT SUM(m.units) FROM app_cogs_lot_moves m WHERE m.tenant_id = l.tenant_id
			AND m.lot_id = l.id AND NOT EXISTS (SELECT 1 FROM app_cogs_events r
				WHERE r.tenant_id = m.tenant_id AND r.reverses_action_id = m.action_id)), 0)::float8
		FROM app_cogs_lots l
		WHERE l.tenant_id = $1 AND ($2::uuid IS NULL OR l.beer_id = $2) AND ($3::uuid IS NULL OR l.format_id = $3)
		ORDER BY l.packaged_on, l.created_at`, tenantID, beerID, formatID)
	if err != nil {
		return nil, fmt.Errorf("listing lots: %w", err)
	}
	defer rows.Close()
	var out []Lot
	for rows.Next() {
		var l Lot
		if err := rows.Scan(&l.ID, &l.BeerID, &l.BatchID, &l.FormatID, &l.PackagedOn, &l.Units); err != nil {
			return nil, fmt.Errorf("scanning lot: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// addLot creates a lot and its opening move.
func addLot(ctx context.Context, q querier, tenantID uuid.UUID, ev *Event, beerID uuid.UUID,
	batchID *uuid.UUID, formatID uuid.UUID, on string, units float64,
) (uuid.UUID, error) {
	var id uuid.UUID
	if err := q.QueryRow(ctx, `INSERT INTO app_cogs_lots (tenant_id, beer_id, batch_id, format_id,
		packaged_on, event_id) VALUES ($1,$2,$3,$4,$5::date,$6) RETURNING id`,
		tenantID, beerID, batchID, formatID, on, ev.ID).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("creating lot: %w", err)
	}
	return id, addMove(ctx, q, tenantID, ev, id, units)
}

func addMove(ctx context.Context, q querier, tenantID uuid.UUID, ev *Event, lotID uuid.UUID, units float64) error {
	if _, err := q.Exec(ctx, `INSERT INTO app_cogs_lot_moves (tenant_id, lot_id, event_id, action_id, units)
		VALUES ($1,$2,$3,$4,$5)`, tenantID, lotID, ev.ID, ev.ActionID, round2(units)); err != nil {
		return fmt.Errorf("recording lot move: %w", err)
	}
	return nil
}

// drawn is a draw from one lot.
type drawn struct {
	Lot   Lot
	Units float64
}

// planDraw works out which lots a draw of a beer and format comes from,
// oldest first (one batch's lots only, when batchID is set), without
// writing anything. A shortfall is not refused: the calculated inventory is
// an estimate and the brewer is looking at the keg. It comes off the newest
// lot, leaving it negative until a count corrects it, or off a new lot with
// no batch (a drawn entry with a zero lot id) when there are none.
func planDraw(ctx context.Context, q querier, tenantID, beerID, formatID uuid.UUID, batchID *uuid.UUID,
	units float64,
) ([]drawn, error) {
	lots, err := listLots(ctx, q, tenantID, &beerID, &formatID)
	if err != nil {
		return nil, err
	}
	var out []drawn
	remaining := units
	var last *Lot
	for i := range lots {
		l := lots[i]
		if batchID != nil && (l.BatchID == nil || *l.BatchID != *batchID) {
			continue
		}
		last = &lots[i]
		if remaining <= 0.001 || l.Units <= 0.001 {
			continue
		}
		take := min(l.Units, remaining)
		out = append(out, drawn{Lot: l, Units: take})
		remaining -= take
	}
	switch {
	case remaining <= 0.001:
	case last == nil:
		out = append(out, drawn{Lot: Lot{BeerID: beerID, BatchID: batchID, FormatID: formatID}, Units: remaining})
	default:
		out = append(out, drawn{Lot: *last, Units: remaining})
	}
	return out, nil
}

// applyDraw writes a planned draw against an event.
func applyDraw(ctx context.Context, q querier, tenantID uuid.UUID, ev *Event, draws []drawn, on string) error {
	for _, d := range draws {
		if d.Lot.ID == uuid.Nil {
			if _, err := addLot(ctx, q, tenantID, ev, d.Lot.BeerID, d.Lot.BatchID, d.Lot.FormatID, on, -d.Units); err != nil {
				return err
			}
			continue
		}
		if err := addMove(ctx, q, tenantID, ev, d.Lot.ID, -d.Units); err != nil {
			return err
		}
	}
	return nil
}

// drawFIFO plans and writes a draw in one go.
func drawFIFO(ctx context.Context, q querier, tenantID uuid.UUID, ev *Event, beerID, formatID uuid.UUID,
	batchID *uuid.UUID, units float64, on string,
) error {
	draws, err := planDraw(ctx, q, tenantID, beerID, formatID, batchID, units)
	if err != nil {
		return err
	}
	return applyDraw(ctx, q, tenantID, ev, draws, on)
}

// addToNewest puts units back on the newest lot of a beer and format (a
// keg that came back full, a count that found more), or on a new lot with
// no batch when there is none.
func addToNewest(ctx context.Context, q querier, tenantID uuid.UUID, ev *Event, beerID, formatID uuid.UUID,
	units float64, on string,
) error {
	lots, err := listLots(ctx, q, tenantID, &beerID, &formatID)
	if err != nil {
		return err
	}
	if len(lots) == 0 {
		_, err := addLot(ctx, q, tenantID, ev, beerID, nil, formatID, on, units)
		return err
	}
	return addMove(ctx, q, tenantID, ev, lots[len(lots)-1].ID, units)
}

func round3(v float64) float64 { return float64(int64(v*1000+sign(v)*0.5)) / 1000 }
func round2(v float64) float64 { return float64(int64(v*100+sign(v)*0.5)) / 100 }

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
