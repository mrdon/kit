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
	// Messenger defers writing the message_received event until the
	// handler claims the inbound, so it isn't in session_events yet.
	// Append it explicitly so the parser sees the latest reply.
	log = append(log, MessageLogEntry{
		Direction: "inbound",
		Body:      msg.Body,
		At:        time.Now(),
	})

	parsed, err := a.parseMeetingReply(ctx, log, coord.Config.CandidateSlots, coord.Config.OrganizerTZ)
	if err != nil {
		// Parse failure: log and don't claim — let the user-facing agent take it.
		slog.Error("parsing meeting reply", "error", err, "participant", p.ID)
		return false, nil
	}
	slog.Info("coord reply parsed",
		"coord", coord.ID, "participant", p.ID,
		"intent", parsed.Intent, "availability", parsed.Availability,
		"accepted_time", parsed.AcceptedTime)

	switch parsed.Intent {
	case "unrelated":
		return false, nil

	case "decline":
		p.Status = ParticipantDeclined
		p.Availability = ""
		p.NextNudgeAt = nil
		if err := UpdateParticipant(ctx, a.pool, p); err != nil {
			return false, fmt.Errorf("updating participant on decline: %w", err)
		}
		slog.Info("participant declined", "participant", p.ID, "coord", coord.ID)
		ackParticipant(ctx, a, coord, p, "Understood, thanks for letting me know. I'll pass that along to "+organizerNameFor(ctx, a, coord)+".")
		notifyOrganizer(ctx, a, coord, fmt.Sprintf("**%s** declined the meeting %q. The negotiation will continue with the remaining participants.", participantDisplayName(ctx, a, coord, p), coord.Config.Title))
		// Trigger a fresh round since participant set changed.
		if a.engine != nil {
			_ = a.engine.AdvanceRound(ctx, coord)
		}
		return true, nil

	case "vague":
		p.Availability = parsed.Availability
		p.Status = ParticipantResponded
		p.NextNudgeAt = nil
		if err := UpdateParticipant(ctx, a.pool, p); err != nil {
			return false, fmt.Errorf("updating participant on vague reply: %w", err)
		}
		// Bot follows up with this same participant for specifics.
		ackParticipant(ctx, a, coord, p, "Thanks — could you give me a more specific time? E.g. \"Tuesday at 2pm\" or \"Friday morning 9-11\".")
		return true, nil

	case "accept":
		p.Availability = parsed.Availability
		p.AcceptedTime = parsed.AcceptedTime
		p.Status = ParticipantResponded
		p.NextNudgeAt = nil
		if err := UpdateParticipant(ctx, a.pool, p); err != nil {
			return false, fmt.Errorf("updating participant on accept: %w", err)
		}
		ackParticipant(ctx, a, coord, p, "Got it, "+parsed.AcceptedTime+" works for you. I'll check with the others and let you know when we're locked in.")
		// Run a propose pass — if everyone has accepted the same time, we're converged.
		if a.engine != nil {
			_ = a.engine.AdvanceRound(ctx, coord)
		}
		return true, nil

	case "refine":
		p.Availability = parsed.Availability
		p.AcceptedTime = "" // refining = no specific commitment
		p.Status = ParticipantResponded
		p.NextNudgeAt = nil
		if err := UpdateParticipant(ctx, a.pool, p); err != nil {
			return false, fmt.Errorf("updating participant on refine: %w", err)
		}
		ackParticipant(ctx, a, coord, p, "Got it. I'll check with everyone else and circle back.")
		if a.engine != nil {
			_ = a.engine.AdvanceRound(ctx, coord)
		}
		return true, nil
	}

	return false, nil
}

// ackParticipant DMs the participant a brief acknowledgment so they
// know their reply landed. Doesn't await a reply — just informational.
func ackParticipant(ctx context.Context, a *CoordinationApp, coord *Coordination, p *Participant, body string) {
	if a.msg == nil {
		return
	}
	var userID uuid.UUID
	if p.UserID != nil {
		userID = *p.UserID
	}
	_, err := a.msg.Send(ctx, messenger.SendRequest{
		TenantID:         coord.TenantID,
		Channel:          "slack",
		Recipient:        messenger.Recipient{SlackUserID: p.Identifier},
		Body:             body,
		Origin:           MessengerOrigin,
		OriginRef:        p.ID.String(),
		AwaitReply:       true, // keep awaiting so further corrections route back
		UserID:           userID,
		SessionThreadKey: participantSessionThreadKey(p.ID),
	})
	if err != nil {
		slog.Error("acking participant", "error", err, "participant", p.ID)
	}
}

// organizerNameFor returns the organizer's display name (or a generic
// fallback) for use in messages back to participants.
func organizerNameFor(ctx context.Context, a *CoordinationApp, coord *Coordination) string {
	u, err := models.GetUserByID(ctx, a.pool, coord.TenantID, coord.OrganizerID)
	if err != nil || u == nil || u.DisplayName == nil || *u.DisplayName == "" {
		return "the organizer"
	}
	return *u.DisplayName
}

// participantDisplayName resolves a participant to a friendly name.
// Falls back to the raw Slack ID if the user record has no display
// name set.
func participantDisplayName(ctx context.Context, a *CoordinationApp, coord *Coordination, p *Participant) string {
	if p.UserID != nil {
		if u, err := models.GetUserByID(ctx, a.pool, coord.TenantID, *p.UserID); err == nil && u != nil && u.DisplayName != nil && *u.DisplayName != "" {
			return *u.DisplayName
		}
	}
	return p.Identifier
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
