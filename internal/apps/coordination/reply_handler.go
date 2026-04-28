package coordination

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services/messenger"
)

// handleInboundReply is registered with Messenger as origin="coordination".
// Messenger has already resolved the session and confirmed an awaiting
// outbound from us; this handler does meeting-specific intent
// disambiguation and state updates.
//
// Returns (true, nil) if the message is claimed (parser says it's about
// scheduling). Returns (false, nil) for "unrelated" — the agent loop
// then handles the message normally.
func (a *CoordinationApp) handleInboundReply(ctx context.Context, msg messenger.InboundMessage, originRef string) (bool, error) {
	participantID, err := uuid.Parse(originRef)
	if err != nil {
		return false, fmt.Errorf("invalid origin_ref %q: %w", originRef, err)
	}
	p, err := GetParticipant(ctx, a.pool, msg.Source.TenantID, participantID)
	if err != nil {
		return false, fmt.Errorf("loading participant: %w", err)
	}
	if p == nil {
		return false, nil
	}
	coord, err := GetCoordination(ctx, a.pool, p.TenantID, p.CoordinationID)
	if err != nil {
		return false, fmt.Errorf("loading coordination: %w", err)
	}
	if coord == nil || coord.Status != StatusActive {
		return false, nil
	}
	if p.SessionID == nil {
		return false, nil
	}

	log, err := a.buildMessageLog(ctx, msg.Source.TenantID, *p.SessionID)
	if err != nil {
		return false, fmt.Errorf("building message log: %w", err)
	}

	parsed, err := a.parseMeetingReply(ctx, log, coord.Config.CandidateSlots, coord.Config.OrganizerTZ)
	if err != nil {
		// Parse failure: log and don't claim — let the user-facing agent take it.
		slog.Error("parsing meeting reply", "error", err, "participant", p.ID)
		return false, nil
	}

	switch parsed.Intent {
	case "unrelated":
		return false, nil

	case "ambiguous":
		// Acknowledge but don't change constraints. Don't reschedule a
		// nudge yet — give the participant time. (next_nudge_at stays
		// where it was.)
		return true, nil

	case "decline":
		p.Status = ParticipantDeclined
		p.NextNudgeAt = nil
		if err := UpdateParticipant(ctx, a.pool, p); err != nil {
			return false, fmt.Errorf("updating participant on decline: %w", err)
		}
		slog.Info("participant declined", "participant", p.ID, "coord", coord.ID)
		if err := a.surfaceDeclineCard(ctx, coord, p); err != nil {
			slog.Error("surfacing decline card", "error", err, "coord", coord.ID)
		}
		return true, nil

	case "out_of_window":
		// The participant can't do the requested date range. Surface to
		// the organizer (also a Phase 1.5 card-wiring item). For now,
		// log + leave the participant as-is.
		slog.Info("participant out_of_window", "participant", p.ID, "coord", coord.ID, "notes", parsed.Notes)
		return true, nil

	case "reply":
		// Replace the participant's current constraints with the parser
		// output and mark them responded.
		p.Constraints = Constraints{
			SlotVerdicts: parsed.CurrentConstraints,
			Notes:        parsed.Notes,
		}
		p.Status = ParticipantResponded
		p.NextNudgeAt = nil
		if err := UpdateParticipant(ctx, a.pool, p); err != nil {
			return false, fmt.Errorf("updating participant: %w", err)
		}

		// Recompute candidates given the new constraint. Any responded
		// participant whose stance is invalidated gets next_nudge_at=now()
		// so the next sweep tick re-engages them.
		parts, err := ListParticipants(ctx, a.pool, coord.TenantID, coord.ID)
		if err == nil {
			_, invalidated := recomputeMeeting(coord, parts)
			now := time.Now()
			for _, id := range invalidated {
				if id == p.ID {
					continue // don't immediately re-engage the participant who just replied
				}
				for i := range parts {
					if parts[i].ID == id {
						parts[i].Status = ParticipantResponded
						parts[i].NextNudgeAt = &now
						_ = UpdateParticipant(ctx, a.pool, &parts[i])
						break
					}
				}
			}
		}
		return true, nil
	}

	return false, nil
}

// buildMessageLog reconstructs the per-participant conversation history
// from session_events. Used as input to parseMeetingReply.
func (a *CoordinationApp) buildMessageLog(ctx context.Context, tenantID, sessionID uuid.UUID) ([]MessageLogEntry, error) {
	events, err := models.GetSessionEvents(ctx, a.pool, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]MessageLogEntry, 0, len(events))
	for _, e := range events {
		// We only care about user-visible message events for the parser;
		// LLM-internal events (llm_request, tool_results, etc.) are noise.
		switch e.EventType { //nolint:exhaustive // intentional: only care about message events
		case models.EventTypeMessageSent:
			var data struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(e.Data, &data); err == nil && data.Text != "" {
				out = append(out, MessageLogEntry{Direction: "outbound", Body: data.Text, At: e.CreatedAt})
			}
		case models.EventTypeMessageReceived:
			var data struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(e.Data, &data); err == nil && data.Text != "" {
				out = append(out, MessageLogEntry{Direction: "inbound", Body: data.Text, At: e.CreatedAt})
			}
		}
	}
	return out, nil
}
