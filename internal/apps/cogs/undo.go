package cogs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/services"
)

// actionEvents loads every event of one action, reversed or not.
func actionEvents(ctx context.Context, q querier, tenantID, actionID uuid.UUID) ([]Event, error) {
	rows, err := q.Query(ctx, `SELECT `+eventCols+` FROM app_cogs_events e
		WHERE e.tenant_id = $1 AND e.action_id = $2 ORDER BY e.seq`, tenantID, actionID)
	if err != nil {
		return nil, fmt.Errorf("loading action: %w", err)
	}
	return scanEvents(rows)
}

func isReversed(ctx context.Context, q querier, tenantID, actionID uuid.UUID) (bool, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM app_cogs_events
		WHERE tenant_id = $1 AND reverses_action_id = $2`, tenantID, actionID).Scan(&n); err != nil {
		return false, fmt.Errorf("checking reversal: %w", err)
	}
	return n > 0, nil
}

func actionVessels(events []Event) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, ev := range events {
		for _, p := range []*uuid.UUID{ev.VesselID, ev.ToVesselID} {
			if p != nil && !seen[*p] {
				seen[*p] = true
				out = append(out, *p)
			}
		}
	}
	return out
}

// isNewest reports whether nothing standing was recorded after the action
// on the same vessels or the same packaged lots. Only the newest entry is
// undone from the iPad: undoing an older one can strand what was built on
// it (kegs already repacked, a tank already moved on), which is a console job.
func isNewest(ctx context.Context, q querier, tenantID uuid.UUID, events []Event) (bool, error) {
	var maxSeq int64
	for _, ev := range events {
		maxSeq = max(maxSeq, ev.Seq)
	}
	actionID := events[0].ActionID
	var later int
	if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM app_cogs_events e
		WHERE e.tenant_id = $1 AND e.seq > $2 AND e.kind <> 'reversal' AND e.action_id <> $4
		AND (e.vessel_id = ANY($3) OR e.to_vessel_id = ANY($3)) AND `+standingSQL,
		tenantID, maxSeq, actionVessels(events), actionID).Scan(&later); err != nil {
		return false, fmt.Errorf("checking newer entries: %w", err)
	}
	if later > 0 {
		return false, nil
	}
	if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM app_cogs_lot_moves e
		JOIN app_cogs_events ev ON ev.tenant_id = e.tenant_id AND ev.id = e.event_id
		WHERE e.tenant_id = $1 AND ev.seq > $2 AND e.action_id <> $3 AND `+standingSQL+`
		AND e.lot_id IN (SELECT lot_id FROM app_cogs_lot_moves WHERE tenant_id = $1 AND action_id = $3)`,
		tenantID, maxSeq, actionID).Scan(&later); err != nil {
		return false, fmt.Errorf("checking newer entries: %w", err)
	}
	return later == 0, nil
}

// actionLotBalances is the standing balance of every lot an action moved.
func actionLotBalances(ctx context.Context, q querier, tenantID, actionID uuid.UUID) (map[uuid.UUID]float64, error) {
	rows, err := q.Query(ctx, `SELECT m.lot_id, COALESCE(SUM(e.units) FILTER (WHERE `+standingSQL+`), 0)::float8
		FROM (SELECT DISTINCT lot_id FROM app_cogs_lot_moves WHERE tenant_id = $1 AND action_id = $2) m
		JOIN app_cogs_lot_moves e ON e.tenant_id = $1 AND e.lot_id = m.lot_id
		GROUP BY m.lot_id`, tenantID, actionID)
	if err != nil {
		return nil, fmt.Errorf("loading lot balances: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]float64{}
	for rows.Next() {
		var id uuid.UUID
		var units float64
		if err := rows.Scan(&id, &units); err != nil {
			return nil, fmt.Errorf("scanning lot balance: %w", err)
		}
		out[id] = units
	}
	return out, rows.Err()
}

// Undo reverses an action. People and devices may undo only the newest
// entry on what it touched; an admin may reverse any entry at any age, as
// long as what came after still adds up without it.
func (s *Service) Undo(ctx context.Context, caller *services.Caller, actionID uuid.UUID) (*ActionResult, error) {
	if s == nil || s.pool == nil {
		return nil, ErrNotConfigured
	}
	var result *ActionResult
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockTenant(ctx, tx, caller.TenantID); err != nil {
			return err
		}
		events, err := actionEvents(ctx, tx, caller.TenantID, actionID)
		if err != nil {
			return err
		}
		if len(events) == 0 || events[0].Kind == EvReversal {
			return fmt.Errorf("entry: %w", ErrNotFound)
		}
		if done, err := isReversed(ctx, tx, caller.TenantID, actionID); err != nil || done {
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: that entry was already undone", ErrNotUndoable)
		}
		if !caller.IsAdmin {
			newest, err := isNewest(ctx, tx, caller.TenantID, events)
			if err != nil {
				return err
			}
			if !newest {
				return fmt.Errorf("%w: something was logged after it. An admin can fix it from the console",
					ErrNotUndoable)
			}
		}
		msg, err := s.writeReversal(ctx, tx, caller, events)
		if err != nil {
			return err
		}
		result = &ActionResult{ActionID: actionID, Message: msg}
		return nil
	})
	return result, err
}

func (s *Service) writeReversal(ctx context.Context, tx pgx.Tx, caller *services.Caller, events []Event) (string, error) {
	rec, err := s.recorderFor(ctx, caller)
	if err != nil {
		return "", err
	}
	vessels := actionVessels(events)
	actionID := events[0].ActionID
	rev := Event{ActionID: uuid.New(), Kind: EvReversal, OccurredAt: events[0].OccurredAt,
		ReversesActionID: &actionID}
	if len(vessels) > 0 {
		rev.VesselID = &vessels[0]
	}
	before, err := actionLotBalances(ctx, tx, caller.TenantID, actionID)
	if err != nil {
		return "", err
	}
	if err := insertEvent(ctx, tx, caller.TenantID, rec, &rev); err != nil {
		return "", err
	}
	after, err := actionLotBalances(ctx, tx, caller.TenantID, actionID)
	if err != nil {
		return "", err
	}
	for id, units := range after {
		if units < -0.001 && units < before[id]-0.001 {
			return "", fmt.Errorf("%w: those kegs or cans were already repacked, counted or sent out. "+
				"Undo the later entries first", ErrNotUndoable)
		}
	}
	for _, id := range vessels {
		v, err := getVessel(ctx, tx, caller.TenantID, id)
		if err != nil {
			return "", err
		}
		if err := refreshVessel(ctx, tx, caller.TenantID, v); err != nil {
			return "", fmt.Errorf("%w (undo the later entries on %s first)", err, v.Name)
		}
	}
	line, err := describeAction(ctx, tx, caller.TenantID, events)
	if err != nil {
		return "", err
	}
	return "Undone: " + line, nil
}
