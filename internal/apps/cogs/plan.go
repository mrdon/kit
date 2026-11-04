package cogs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/services"
)

// Queue kinds: loose ends for the console. None of them blocks the brewer.
const (
	QueueNewBeer       = "new_beer"
	QueuePlanFlag      = "plan_flag"
	QueueCountVariance = "count_variance"
)

type queueItem struct {
	Kind        string
	Title       string
	Body        string
	BeerID      *uuid.UUID
	PlanEntryID *uuid.UUID
	ActionID    *uuid.UUID
	Label       string
}

func addQueue(ctx context.Context, q querier, tenantID uuid.UUID, it queueItem) error {
	if _, err := q.Exec(ctx, `INSERT INTO app_cogs_queue (tenant_id, kind, title, body, beer_id,
		plan_entry_id, action_id, created_label) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		tenantID, it.Kind, it.Title, it.Body, it.BeerID, it.PlanEntryID, it.ActionID, it.Label); err != nil {
		return fmt.Errorf("adding queue item: %w", err)
	}
	return nil
}

// QueueItem is an open loose end as the console shows it.
type QueueItem struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	BeerID      *uuid.UUID `json:"beer_id"`
	PlanEntryID *uuid.UUID `json:"plan_entry_id"`
	ActionID    *uuid.UUID `json:"action_id"`
	Label       string     `json:"created_label"`
	CreatedAt   time.Time  `json:"created_at"`
}

// OpenQueue lists unresolved items, oldest first.
func (s *Service) OpenQueue(ctx context.Context, tenantID uuid.UUID) ([]QueueItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, title, body, beer_id, plan_entry_id, action_id,
		created_label, created_at FROM app_cogs_queue
		WHERE tenant_id = $1 AND resolved_at IS NULL ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing brewing queue: %w", err)
	}
	defer rows.Close()
	out := []QueueItem{}
	for rows.Next() {
		var it QueueItem
		if err := rows.Scan(&it.ID, &it.Kind, &it.Title, &it.Body, &it.BeerID, &it.PlanEntryID, &it.ActionID,
			&it.Label, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning queue item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ResolveQueue closes an item.
func (s *Service) ResolveQueue(ctx context.Context, tenantID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE app_cogs_queue SET resolved_at = NOW()
		WHERE tenant_id = $1 AND id = $2 AND resolved_at IS NULL`, tenantID, id)
	if err != nil {
		return fmt.Errorf("resolving queue item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("queue item: %w", ErrNotFound)
	}
	return nil
}

// PlanEntry is a planned brew.
type PlanEntry struct {
	ID        uuid.UUID  `json:"id"`
	BeerID    uuid.UUID  `json:"beer_id"`
	VesselID  uuid.UUID  `json:"vessel_id"`
	BrewOn    string     `json:"brew_on"`
	VolumeBBL float64    `json:"volume_bbl"`
	Why       string     `json:"why"`
	BatchID   *uuid.UUID `json:"batch_id"`
}

// PlanInput is the editable part of a plan entry.
type PlanInput struct {
	BeerID    uuid.UUID `json:"beer_id"`
	VesselID  uuid.UUID `json:"vessel_id"`
	BrewOn    string    `json:"brew_on"`
	VolumeBBL float64   `json:"volume_bbl"`
	Why       string    `json:"why"`
}

// listPlan returns the plan entries not cancelled, from a date on, plus
// any earlier one never brewed: a missed date stays on the plan (and in
// This week, as due) until it's brewed, moved or cancelled. A done entry is
// done only while the brew that closed it still stands.
func listPlan(ctx context.Context, q querier, tenantID uuid.UUID, from string) ([]PlanEntry, error) {
	rows, err := q.Query(ctx, `SELECT p.id, p.beer_id, p.vessel_id, to_char(p.brew_on, 'YYYY-MM-DD'),
		p.volume_bbl::float8, p.why,
		CASE WHEN EXISTS (SELECT 1 FROM app_cogs_events e WHERE e.tenant_id = p.tenant_id
			AND e.batch_id = p.batch_id AND e.kind = 'brew' AND `+standingSQL+`) THEN p.batch_id END
		FROM app_cogs_plan_entries p
		WHERE p.tenant_id = $1 AND p.cancelled_at IS NULL AND (p.brew_on >= $2::date OR NOT EXISTS (
			SELECT 1 FROM app_cogs_events e WHERE e.tenant_id = p.tenant_id AND e.batch_id = p.batch_id
			AND e.kind = 'brew' AND `+standingSQL+`))
		ORDER BY p.brew_on`, tenantID, from)
	if err != nil {
		return nil, fmt.Errorf("listing plan: %w", err)
	}
	defer rows.Close()
	out := []PlanEntry{}
	for rows.Next() {
		var p PlanEntry
		if err := rows.Scan(&p.ID, &p.BeerID, &p.VesselID, &p.BrewOn, &p.VolumeBBL, &p.Why, &p.BatchID); err != nil {
			return nil, fmt.Errorf("scanning plan entry: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Plan lists entries from a date on.
func (s *Service) Plan(ctx context.Context, tenantID uuid.UUID, from string) ([]PlanEntry, error) {
	return listPlan(ctx, s.pool, tenantID, from)
}

// SavePlan creates (id nil) or edits a plan entry.
func (s *Service) SavePlan(ctx context.Context, tenantID uuid.UUID, id *uuid.UUID, in PlanInput) (uuid.UUID, error) {
	if _, err := time.Parse("2006-01-02", in.BrewOn); err != nil {
		return uuid.Nil, fmt.Errorf("%w: brew date %q", ErrInvalid, in.BrewOn)
	}
	if in.VolumeBBL <= 0 {
		return uuid.Nil, fmt.Errorf("%w: a planned brew needs a size", ErrInvalid)
	}
	if _, err := getBeer(ctx, s.pool, tenantID, in.BeerID); err != nil {
		return uuid.Nil, err
	}
	if _, err := getVessel(ctx, s.pool, tenantID, in.VesselID); err != nil {
		return uuid.Nil, err
	}
	var out uuid.UUID
	var err error
	if id == nil {
		err = s.pool.QueryRow(ctx, `INSERT INTO app_cogs_plan_entries (tenant_id, beer_id, vessel_id, brew_on,
			volume_bbl, why) VALUES ($1,$2,$3,$4::date,$5,$6) RETURNING id`,
			tenantID, in.BeerID, in.VesselID, in.BrewOn, in.VolumeBBL, in.Why).Scan(&out)
	} else {
		err = s.pool.QueryRow(ctx, `UPDATE app_cogs_plan_entries SET beer_id=$3, vessel_id=$4, brew_on=$5::date,
			volume_bbl=$6, why=$7, updated_at=NOW() WHERE tenant_id=$1 AND id=$2 RETURNING id`,
			tenantID, *id, in.BeerID, in.VesselID, in.BrewOn, in.VolumeBBL, in.Why).Scan(&out)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("plan entry: %w", ErrNotFound)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("saving plan entry: %w", err)
	}
	return out, nil
}

// CancelPlan drops a planned brew.
func (s *Service) CancelPlan(ctx context.Context, tenantID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE app_cogs_plan_entries SET cancelled_at = NOW(), updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2 AND cancelled_at IS NULL`, tenantID, id)
	if err != nil {
		return fmt.Errorf("cancelling plan entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("plan entry: %w", ErrNotFound)
	}
	return nil
}

// FlagPlan is the brewer's "this date won't work": a note to the console,
// never a change to the plan.
func (s *Service) FlagPlan(ctx context.Context, caller *services.Caller, id uuid.UUID, note string) error {
	var beer, vessel, on string
	err := s.pool.QueryRow(ctx, `SELECT b.name, v.name, to_char(p.brew_on, 'Dy Mon FMDD')
		FROM app_cogs_plan_entries p
		JOIN app_cogs_beers b ON b.tenant_id = p.tenant_id AND b.id = p.beer_id
		JOIN app_cogs_vessels v ON v.tenant_id = p.tenant_id AND v.id = p.vessel_id
		WHERE p.tenant_id = $1 AND p.id = $2`, caller.TenantID, id).Scan(&beer, &vessel, &on)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("plan entry: %w", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("loading plan entry: %w", err)
	}
	rec, err := s.recorderFor(ctx, caller)
	if err != nil {
		return err
	}
	body := strings.TrimSpace(note)
	if body == "" {
		body = "No reason given."
	}
	return addQueue(ctx, s.pool, caller.TenantID, queueItem{
		Kind: QueuePlanFlag, Title: fmt.Sprintf("%s says %s on %s (%s) won't work", rec.Label, beer, on, vessel),
		Body: body, PlanEntryID: &id, Label: rec.Label,
	})
}
