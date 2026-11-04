package cogs

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// HistoryEntry is one action as the history lists it.
type HistoryEntry struct {
	ActionID  uuid.UUID   `json:"action_id"`
	At        time.Time   `json:"at"`
	Kind      string      `json:"kind"`
	Text      string      `json:"text"`
	By        string      `json:"by"`
	Reversed  bool        `json:"reversed"`
	VesselIDs []uuid.UUID `json:"vessel_ids"`
	// CanUndo: the caller may undo it from here. For people and devices
	// that is only the newest entry on what it touched.
	CanUndo bool `json:"can_undo"`
}

// History lists recent actions, newest first, optionally for one vessel.
func (s *Service) History(ctx context.Context, caller *services.Caller, vesselID *uuid.UUID, limit int) ([]HistoryEntry, error) {
	if s == nil || s.pool == nil {
		return nil, ErrNotConfigured
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT e.action_id FROM app_cogs_events e
		WHERE e.tenant_id = $1 AND e.kind <> 'reversal'
		AND ($2::uuid IS NULL OR e.vessel_id = $2 OR e.to_vessel_id = $2)
		GROUP BY e.action_id ORDER BY MAX(e.seq) DESC LIMIT $3`, caller.TenantID, vesselID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing history: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning history: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing history: %w", err)
	}
	return s.historyEntries(ctx, caller, ids)
}

func (s *Service) historyEntries(ctx context.Context, caller *services.Caller, ids []uuid.UUID) ([]HistoryEntry, error) {
	out := []HistoryEntry{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+eventCols+` FROM app_cogs_events e
		WHERE e.tenant_id = $1 AND (e.action_id = ANY($2) OR e.reverses_action_id = ANY($2)) ORDER BY e.seq`,
		caller.TenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("loading history events: %w", err)
	}
	events, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	byAction := map[uuid.UUID][]Event{}
	reversed := map[uuid.UUID]bool{}
	for _, ev := range events {
		if ev.Kind == EvReversal && ev.ReversesActionID != nil {
			reversed[*ev.ReversesActionID] = true
			continue
		}
		byAction[ev.ActionID] = append(byAction[ev.ActionID], ev)
	}
	n, err := newNames(ctx, s.pool, caller.TenantID)
	if err != nil {
		return nil, err
	}
	firstStanding := true
	for _, id := range ids {
		evs := byAction[id]
		if len(evs) == 0 {
			continue
		}
		e := HistoryEntry{ActionID: id, At: evs[0].OccurredAt, Kind: evs[0].Kind, Text: n.describe(evs),
			By: evs[0].RecordedLabel, Reversed: reversed[id], VesselIDs: actionVessels(evs)}
		if !e.Reversed {
			switch {
			case caller.IsAdmin:
				e.CanUndo = true
			case firstStanding:
				if e.CanUndo, err = isNewest(ctx, s.pool, caller.TenantID, evs); err != nil {
					return nil, err
				}
			}
			firstStanding = false
		}
		out = append(out, e)
	}
	return out, nil
}
