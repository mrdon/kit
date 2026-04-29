package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/services/messenger"
	"github.com/mrdon/kit/internal/tools"
)

// approvalDraft is one drafted message awaiting organizer approval,
// round-tripped through the card's tool args so resolution sends
// the exact approved text without re-drafting.
type approvalDraft struct {
	ParticipantID string `json:"participant_id"`
	Body          string `json:"body"`
}

// surfaceApprovalCard creates one approval card per drafted message —
// one per participant. Each card has its own Send / Don't send /
// Send + auto-approve future / Cancel options. This lets the organizer
// review and act on each draft independently (and, eventually, edit
// each one before sending).
func (a *CoordinationApp) surfaceApprovalCard(ctx context.Context, coord *Coordination, p *Participant, draft approvalDraft) error {
	if a.cards == nil {
		return errors.New("CardService not configured")
	}
	caller, err := a.organizerCaller(ctx, coord)
	if err != nil {
		return fmt.Errorf("building caller: %w", err)
	}

	name := p.Identifier
	if p.UserID != nil {
		if u, err := models.GetUserByID(ctx, a.pool, coord.TenantID, *p.UserID); err == nil && u != nil && u.DisplayName != nil && *u.DisplayName != "" {
			name = *u.DisplayName
		}
	}

	body := fmt.Sprintf(
		"Here's what I'd send to **%s**:\n\n---\n\n%s\n\n---\n\n"+
			"Swipe right to send. Swipe left to skip this one (I'll ask you what to do).\n"+
			"Tap for details to send-and-auto-approve future, or to cancel the whole coordination.",
		name, draft.Body,
	)

	drafts := []approvalDraft{draft}
	sendArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "send_drafts",
		"drafts":          drafts,
	})
	sendAutoArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "send_drafts_auto",
		"drafts":          drafts,
	})
	skipArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "skip_one",
		"drafts":          drafts,
	})
	cancelArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "cancel",
	})

	_, err = a.cards.CreateDecision(ctx, caller, cards.CardCreateInput{
		Title: fmt.Sprintf("Send DM to %s — %s", name, coord.Config.Title),
		Body:  body,
		Kind:  cards.CardKindDecision,
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: "send_drafts",
			Options: []cards.DecisionOption{
				{OptionID: "send_drafts", Label: "Send", ToolName: coordinationResolveDecision, ToolArguments: sendArgs},
				{OptionID: "skip_one", Label: "Don't send to " + name, ToolName: coordinationResolveDecision, ToolArguments: skipArgs},
				{OptionID: "send_drafts_auto", Label: "Send + auto-approve future", ToolName: coordinationResolveDecision, ToolArguments: sendAutoArgs},
				{OptionID: "cancel", Label: "Cancel coordination", ToolName: coordinationResolveDecision, ToolArguments: cancelArgs},
			},
		},
	})
	return err
}

// surfaceConvergenceCard creates a decision card for the organizer when
// the engine has detected a slot that works for all responded
// participants. Options invoke the internal coordination_resolve_decision
// tool with the chosen action.
func (a *CoordinationApp) surfaceConvergenceCard(ctx context.Context, coord *Coordination, slot Slot) error {
	if a.cards == nil {
		return errors.New("CardService not configured")
	}
	caller, err := a.organizerCaller(ctx, coord)
	if err != nil {
		return fmt.Errorf("building caller: %w", err)
	}

	confirmArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "confirm",
		"slot_key":        slot.Key(),
	})
	rejectArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "reject_slot",
		"slot_key":        slot.Key(),
	})
	cancelArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "cancel",
	})

	parts, _ := ListParticipants(ctx, a.pool, coord.TenantID, coord.ID)
	body := buildConvergenceBody(coord, slot, parts)

	_, err = a.cards.CreateDecision(ctx, caller, cards.CardCreateInput{
		Title: coord.Config.Title + " — slot agreed",
		Body:  body,
		Kind:  cards.CardKindDecision,
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: "confirm",
			Options: []cards.DecisionOption{
				{OptionID: "confirm", Label: "Confirm", ToolName: coordinationResolveDecision, ToolArguments: confirmArgs},
				// Swipe-left = "Try a different slot" (first non-recommended).
				{OptionID: "reject_slot", Label: "Try a different slot", ToolName: coordinationResolveDecision, ToolArguments: rejectArgs},
				{OptionID: "cancel", Label: "Cancel coordination", ToolName: coordinationResolveDecision, ToolArguments: cancelArgs},
			},
		},
	})
	return err
}

// surfaceAbandonmentCard creates a decision card when a coordination
// has run out of nudges, hit its deadline, or otherwise can't converge.
// Phase 1: shows the audit trail and asks Abandon vs. Extend.
func (a *CoordinationApp) surfaceAbandonmentCard(ctx context.Context, coord *Coordination, reason string) error {
	if a.cards == nil {
		return errors.New("CardService not configured")
	}
	caller, err := a.organizerCaller(ctx, coord)
	if err != nil {
		return fmt.Errorf("building caller: %w", err)
	}

	parts, _ := ListParticipants(ctx, a.pool, coord.TenantID, coord.ID)
	body := buildAbandonmentBody(coord, reason, parts)

	abandonArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "abandon",
	})
	extendArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "extend",
	})

	_, err = a.cards.CreateDecision(ctx, caller, cards.CardCreateInput{
		Title: fmt.Sprintf("%s — %s", coord.Config.Title, reason),
		Body:  body,
		Kind:  cards.CardKindDecision,
		Decision: &cards.DecisionCreateInput{
			Priority: cards.DecisionPriorityMedium,
			// Swipe-right = "Extend"; swipe-left = "Abandon". Recommended
			// is "Extend" because it's the less destructive action.
			RecommendedOptionID: "extend",
			Options: []cards.DecisionOption{
				{OptionID: "extend", Label: "Extend by 7 days", ToolName: coordinationResolveDecision, ToolArguments: extendArgs},
				{OptionID: "abandon", Label: "Abandon", ToolName: coordinationResolveDecision, ToolArguments: abandonArgs},
			},
		},
	})
	return err
}

// surfaceConvergenceCardFreeForm surfaces a confirmation card when
// the LLM solver decides everyone aligned on a specific time. The
// chosenTime is human-readable (e.g. "Wed Apr 30 at 4:00 PM"); we
// don't have a structured Slot for it because the negotiation flow
// works with free-form availability statements.
func (a *CoordinationApp) surfaceConvergenceCardFreeForm(ctx context.Context, coord *Coordination, chosenTime, summary string) error {
	if a.cards == nil {
		return errors.New("CardService not configured")
	}
	caller, err := a.organizerCaller(ctx, coord)
	if err != nil {
		return err
	}
	confirmArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "confirm_freeform",
		"slot_key":        chosenTime, // reuse field for human-readable time
	})
	cancelArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "cancel",
	})

	body := fmt.Sprintf("**%s** works for everyone.\n\n%s\n\nConfirm to mark this resolved. The bot will notify everyone. (You'll still need to send the calendar invite yourself for now.)", chosenTime, summary)

	_, err = a.cards.CreateDecision(ctx, caller, cards.CardCreateInput{
		Title: coord.Config.Title + " — slot agreed",
		Body:  body,
		Kind:  cards.CardKindDecision,
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: "confirm_freeform",
			Options: []cards.DecisionOption{
				{OptionID: "confirm_freeform", Label: "Confirm " + chosenTime, ToolName: coordinationResolveDecision, ToolArguments: confirmArgs},
				{OptionID: "cancel", Label: "Cancel coordination", ToolName: coordinationResolveDecision, ToolArguments: cancelArgs},
			},
		},
	})
	return err
}

// surfaceTimedOutCard surfaces a card to the organizer when a
// participant has missed the configured number of nudges. The
// organizer can either cancel the whole coord or reset the participant's
// nudge counter to give them another chance.
func (a *CoordinationApp) surfaceTimedOutCard(ctx context.Context, coord *Coordination, p *Participant) error {
	if a.cards == nil {
		return errors.New("CardService not configured")
	}
	caller, err := a.organizerCaller(ctx, coord)
	if err != nil {
		return err
	}
	name := participantName(p)
	if p.UserID != nil {
		if u, err := models.GetUserByID(ctx, a.pool, coord.TenantID, *p.UserID); err == nil && u != nil && u.DisplayName != nil && *u.DisplayName != "" {
			name = *u.DisplayName
		}
	}

	resetArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "reset_nudge",
		"slot_key":        p.ID.String(), // reuse slot_key field for participant_id
	})
	cancelArgs, _ := json.Marshal(map[string]any{
		"coordination_id": coord.ID.String(),
		"action":          "cancel",
	})

	body := fmt.Sprintf("**%s** hasn't responded after %d nudge(s).\n\nReset their nudge counter to give them another chance, or cancel the coordination?", name, p.NudgeCount)

	_, err = a.cards.CreateDecision(ctx, caller, cards.CardCreateInput{
		Title: fmt.Sprintf("%s — %s hasn't replied", coord.Config.Title, name),
		Body:  body,
		Kind:  cards.CardKindDecision,
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: "reset_nudge",
			Options: []cards.DecisionOption{
				{OptionID: "reset_nudge", Label: "Try again (reset nudges)", ToolName: coordinationResolveDecision, ToolArguments: resetArgs},
				{OptionID: "cancel", Label: "Cancel coordination", ToolName: coordinationResolveDecision, ToolArguments: cancelArgs},
			},
		},
	})
	return err
}

// surfaceOutOfWindowCard removed — replaced by the iterative
// negotiation flow in handleInboundReply (engine.AdvanceRound), which
// keeps the negotiation alive and broadcasts new info to others rather
// than terminating with a cancel-only card.

// organizerCaller constructs a services.Caller for the coordination's
// organizer, used when creating decision cards on their behalf.
func (a *CoordinationApp) organizerCaller(ctx context.Context, coord *Coordination) (*services.Caller, error) {
	user, err := models.GetUserByID(ctx, a.pool, coord.TenantID, coord.OrganizerID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("organizer %s not found", coord.OrganizerID)
	}
	tenant, err := models.GetTenantByID(ctx, a.pool, coord.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, fmt.Errorf("tenant %s not found", coord.TenantID)
	}
	return &services.Caller{
		TenantID: coord.TenantID,
		UserID:   coord.OrganizerID,
		Identity: user.SlackUserID,
		Timezone: services.ResolveTimezone(user.Timezone, tenant.Timezone),
	}, nil
}

func participantName(p *Participant) string {
	if p == nil {
		return "(participant)"
	}
	return p.Identifier
}

func buildConvergenceBody(coord *Coordination, slot Slot, parts []Participant) string {
	loc, err := loadTZ(coord.Config.OrganizerTZ)
	if err != nil {
		loc = time.UTC
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** works for everyone.\n\n", slot.Start.In(loc).Format("Mon Jan 2 at 3:04 PM"))
	fmt.Fprintf(&b, "Title: %s\n", coord.Config.Title)
	fmt.Fprintf(&b, "Duration: %d min\n\n", coord.Config.DurationMinutes)
	fmt.Fprintf(&b, "Attendees:\n")
	for _, p := range parts {
		switch p.Status {
		case ParticipantResponded:
			fmt.Fprintf(&b, "- %s — accepted\n", p.Identifier)
		case ParticipantDeclined:
			fmt.Fprintf(&b, "- %s — declined (excluded)\n", p.Identifier)
		case ParticipantTimedOut:
			fmt.Fprintf(&b, "- %s — timed out (excluded)\n", p.Identifier)
		default:
			fmt.Fprintf(&b, "- %s — %s\n", p.Identifier, p.Status)
		}
	}
	b.WriteString("\nConfirm to mark this resolved. The bot will notify everyone. (You'll still need to send the calendar invite yourself for now.)")
	return b.String()
}

func buildAbandonmentBody(coord *Coordination, reason string, parts []Participant) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Couldn't land on a time everyone can make: %s\n\n", reason)
	if coord.Config.LatestProposal.Summary != "" {
		fmt.Fprintf(&b, "Latest summary: %s\n\n", coord.Config.LatestProposal.Summary)
	}
	fmt.Fprintf(&b, "Where each person stands:\n")
	for _, p := range parts {
		availability := p.Availability
		if availability == "" {
			availability = "(no availability stated)"
		}
		switch p.Status {
		case ParticipantDeclined:
			fmt.Fprintf(&b, "- %s — **declined**\n", p.Identifier)
		case ParticipantTimedOut:
			fmt.Fprintf(&b, "- %s — **didn't reply** after %d nudge(s)\n", p.Identifier, p.NudgeCount)
		case ParticipantPending, ParticipantContacted:
			fmt.Fprintf(&b, "- %s — still awaiting reply\n", p.Identifier)
		default:
			fmt.Fprintf(&b, "- %s — %s\n", p.Identifier, availability)
		}
	}
	fmt.Fprintf(&b, "\nExtend gives everyone another %d rounds. Cancel ends the coordination — you can start a fresh one with a different attendee list if needed.", MaxRounds)
	return b.String()
}

// coordinationResolveDecision is the tool name used by decision card
// options to call back into coordination on user resolution. Internal —
// not exposed in the system prompt or visible to the agent for direct
// invocation; only fires via card resolution.
const coordinationResolveDecision = "coordination_resolve_decision"

// resolveDecisionInput is the args shape passed by decision card options.
type resolveDecisionInput struct {
	CoordinationID      string          `json:"coordination_id"`
	Action              string          `json:"action"`
	SlotKey             string          `json:"slot_key,omitempty"`
	DeclinedParticipant string          `json:"declined_participant,omitempty"`
	Drafts              []approvalDraft `json:"drafts,omitempty"`
}

// registerResolveDecisionTool wires the internal resolve tool into the
// agent registry. Called from registerCoordinationAgentTools — the tool
// is PolicyAllow (no further gate) and not visible in the catalog
// (callers don't invoke it directly).
func registerResolveDecisionTool(r *tools.Registry, app *CoordinationApp) {
	r.Register(tools.Def{
		Name:        coordinationResolveDecision,
		Description: "Internal: resolve a coordination decision card. Invoked by card options, not by users.",
		Schema: services.PropsReq(map[string]any{
			"coordination_id":      services.Field("string", "Coordination UUID"),
			"action":               services.Field("string", "confirm | reject_slot | cancel | abandon | extend | proceed_without | send_drafts | send_drafts_auto | skip_one"),
			"slot_key":             services.Field("string", "Slot key (for confirm/reject_slot actions)"),
			"declined_participant": services.Field("string", "Participant id (for proceed_without action)"),
			"drafts":               services.Field("array", "Drafted messages to send (for send_drafts/send_drafts_auto/skip_one actions)"),
		}, "coordination_id", "action"),
		DefaultPolicy:  tools.PolicyAllow,
		AdminOnly:      false,
		DenyCallerGate: true,
		Handler:        resolveDecisionHandler(app),
	})
}

func resolveDecisionHandler(app *CoordinationApp) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var inp resolveDecisionInput
		if err := json.Unmarshal(raw, &inp); err != nil {
			return "", err
		}
		coordID, err := uuid.Parse(inp.CoordinationID)
		if err != nil {
			return "", fmt.Errorf("invalid coordination_id: %w", err)
		}
		coord, err := GetCoordination(ec.Ctx, app.pool, ec.Tenant.ID, coordID)
		if err != nil {
			return "", fmt.Errorf("loading coordination: %w", err)
		}
		if coord == nil {
			return "", errors.New("coordination not found")
		}

		switch inp.Action {
		case "confirm":
			return resolveConfirm(ec.Ctx, app, coord, inp.SlotKey)
		case "reject_slot":
			return resolveRejectSlot(ec.Ctx, app, coord, inp.SlotKey)
		case "cancel":
			return resolveCancel(ec.Ctx, app, coord)
		case "abandon":
			return resolveAbandon(ec.Ctx, app, coord)
		case "extend":
			return resolveExtend(ec.Ctx, app, coord)
		case "proceed_without":
			return resolveProceedWithout(ec.Ctx, app, coord, inp.DeclinedParticipant)
		case "send_drafts":
			return resolveSendDrafts(ec.Ctx, app, coord, inp.Drafts, false)
		case "send_drafts_auto":
			return resolveSendDrafts(ec.Ctx, app, coord, inp.Drafts, true)
		case "skip_one":
			return resolveSkipOne(ec.Ctx, app, coord, inp.Drafts)
		case "reset_nudge":
			// SlotKey field is reused as the participant id for the
			// timed-out card path (avoids adding another field to the
			// resolve schema).
			return resolveResetNudge(ec.Ctx, app, coord, inp.SlotKey)
		case "confirm_freeform":
			// SlotKey carries the human-readable chosen time string.
			return resolveConfirmFreeform(ec.Ctx, app, coord, inp.SlotKey)
		}
		return "", fmt.Errorf("unknown action %q", inp.Action)
	}
}

func resolveConfirm(ctx context.Context, app *CoordinationApp, coord *Coordination, slotKey string) (string, error) {
	if coord.Status != StatusActive && coord.Status != StatusConverged {
		return "Coordination is " + coord.Status + "; nothing to confirm.", nil
	}
	slot := findSlotByKey(coord.Config.CandidateSlots, slotKey)
	if slot == nil {
		return "", errors.New("slot not in candidate set")
	}
	if err := UpdateCoordinationStatus(ctx, app.pool, coord.TenantID, coord.ID, StatusConfirmed, &CoordinationResult{ChosenSlot: slot}); err != nil {
		return "", err
	}
	if err := app.notifyParticipantsConfirmed(ctx, coord, *slot); err != nil {
		slog.Error("notifying confirmed", "error", err)
	}
	loc, _ := loadTZ(coord.Config.OrganizerTZ)
	if loc == nil {
		loc = time.UTC
	}
	when := slot.Start.In(loc).Format("Mon Jan 2 at 3:04 PM")
	notifyOrganizer(ctx, app, coord, fmt.Sprintf("Confirmed %q for %s. Participants notified. You'll need to send the actual calendar invite — I can't do that yet.", coord.Config.Title, when))
	return "Coordination confirmed. Participants notified.", nil
}

func resolveRejectSlot(ctx context.Context, app *CoordinationApp, coord *Coordination, slotKey string) (string, error) {
	if findSlotByKey(coord.Config.CandidateSlots, slotKey) == nil {
		return "", errors.New("slot not in candidate set")
	}
	newSlots := make([]Slot, 0, len(coord.Config.CandidateSlots))
	for _, s := range coord.Config.CandidateSlots {
		if s.Key() != slotKey {
			newSlots = append(newSlots, s)
		}
	}
	coord.Config.CandidateSlots = newSlots
	if err := UpdateCoordinationConfig(ctx, app.pool, coord.TenantID, coord.ID, coord.Config); err != nil {
		return "", err
	}
	if err := UpdateCoordinationStatus(ctx, app.pool, coord.TenantID, coord.ID, StatusActive, nil); err != nil {
		return "", err
	}
	if err := app.armResponded(ctx, coord); err != nil {
		slog.Error("re-arming participants", "error", err)
	}
	return "Slot rejected; coordination reopened.", nil
}

func resolveCancel(ctx context.Context, app *CoordinationApp, coord *Coordination) (string, error) {
	if err := UpdateCoordinationStatus(ctx, app.pool, coord.TenantID, coord.ID, StatusCancelled, nil); err != nil {
		return "", err
	}
	if app.engine != nil {
		_ = app.engine.NotifyCancel(ctx, coord)
	}
	parts, _ := ListParticipants(ctx, app.pool, coord.TenantID, coord.ID)
	contacted := 0
	for _, p := range parts {
		if p.Status == ParticipantContacted || p.Status == ParticipantResponded {
			contacted++
		}
	}
	msg := fmt.Sprintf("Cancelled %q.", coord.Config.Title)
	if contacted > 0 {
		msg += fmt.Sprintf(" Sent a brief 'cancelled' note to %d participant(s).", contacted)
	}
	notifyOrganizer(ctx, app, coord, msg)
	return "Coordination cancelled.", nil
}

// resolveResetNudge resets the participant's nudge counter and re-arms
// them so the engine drafts another outreach next sweep tick.
func resolveResetNudge(ctx context.Context, app *CoordinationApp, coord *Coordination, participantRef string) (string, error) {
	pid, err := uuid.Parse(participantRef)
	if err != nil {
		return "", fmt.Errorf("invalid participant id: %w", err)
	}
	p, err := GetParticipant(ctx, app.pool, coord.TenantID, pid)
	if err != nil || p == nil {
		return "", fmt.Errorf("loading participant: %w", err)
	}
	now := time.Now()
	p.Status = ParticipantPending
	p.NudgeCount = 0
	p.NextNudgeAt = &now
	if err := UpdateParticipant(ctx, app.pool, p); err != nil {
		return "", fmt.Errorf("resetting nudge: %w", err)
	}
	return fmt.Sprintf("Reset nudges for %s. They'll get a fresh outreach soon.", p.Identifier), nil
}

// resolveSkipOne is fired when the organizer rejects a single drafted
// DM via swipe-left. It does NOT cancel the coordination or notify the
// other participants — just stops THIS one draft and asks the organizer
// what to do for that participant. The participant stays parked
// (next_nudge_at nil) until the organizer indicates how to proceed.
func resolveSkipOne(ctx context.Context, app *CoordinationApp, coord *Coordination, drafts []approvalDraft) (string, error) {
	if len(drafts) == 0 {
		return "Skipped (no drafts in card args).", nil
	}
	d := drafts[0]
	pid, err := uuid.Parse(d.ParticipantID)
	if err != nil {
		return "", fmt.Errorf("invalid participant_id: %w", err)
	}
	p, err := GetParticipant(ctx, app.pool, coord.TenantID, pid)
	if err != nil || p == nil {
		return "", fmt.Errorf("loading participant: %w", err)
	}
	name := p.Identifier
	if p.UserID != nil {
		if u, err := models.GetUserByID(ctx, app.pool, coord.TenantID, *p.UserID); err == nil && u != nil && u.DisplayName != nil && *u.DisplayName != "" {
			name = *u.DisplayName
		}
	}
	msg := fmt.Sprintf(
		"Got it — I won't send that draft to **%s** for %q. They're parked: "+
			"no outbound to them until you say how to proceed.\n\n"+
			"What would you like me to do? Some options:\n"+
			"• Reach out to %s yourself, then tell me what they said\n"+
			"• Have me try again with different times or framing\n"+
			"• Skip them entirely (proceed with the others)\n"+
			"• Cancel the coordination",
		name, coord.Config.Title, name,
	)
	notifyOrganizer(ctx, app, coord, msg)
	return fmt.Sprintf("Skipped DM to %s. Asked the organizer what to do next.", name), nil
}

func resolveAbandon(ctx context.Context, app *CoordinationApp, coord *Coordination) (string, error) {
	if err := UpdateCoordinationStatus(ctx, app.pool, coord.TenantID, coord.ID, StatusAbandoned, nil); err != nil {
		return "", err
	}
	notifyOrganizer(ctx, app, coord, fmt.Sprintf("Abandoned %q. Nothing further will go out.", coord.Config.Title))
	return "Coordination abandoned.", nil
}

func resolveExtend(ctx context.Context, app *CoordinationApp, coord *Coordination) (string, error) {
	// Extend covers two contexts: deadline-driven abandonment (push the
	// deadline by 7 days) and round-limit-driven abandonment (give the
	// negotiation another MaxRounds rounds). We reset both so the coord
	// has fresh runway either way.
	newDeadline := time.Now().Add(7 * 24 * time.Hour)
	_, err := app.pool.Exec(ctx, `
		UPDATE app_coordinations
		SET deadline_at = $3, round_count = 0, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, coord.TenantID, coord.ID, newDeadline)
	if err != nil {
		return "", err
	}
	notifyOrganizer(ctx, app, coord, fmt.Sprintf("Extended %q — deadline +7 days, %d more rounds available.", coord.Config.Title, MaxRounds))
	return fmt.Sprintf("Extended: deadline +7 days, %d more rounds.", MaxRounds), nil
}

func resolveProceedWithout(ctx context.Context, app *CoordinationApp, coord *Coordination, declinedRef string) (string, error) {
	if declinedRef == "" {
		return "", errors.New("declined_participant required")
	}
	declinedID, err := uuid.Parse(declinedRef)
	if err != nil {
		return "", err
	}
	p, err := GetParticipant(ctx, app.pool, coord.TenantID, declinedID)
	if err != nil {
		return "", err
	}
	if p != nil {
		p.Status = ParticipantDeclined
		p.NextNudgeAt = nil
		if err := UpdateParticipant(ctx, app.pool, p); err != nil {
			return "", err
		}
	}
	if err := app.armResponded(ctx, coord); err != nil {
		slog.Error("re-arming after proceed_without", "error", err)
	}
	return "Proceeding without that participant.", nil
}

// resolveConfirmFreeform handles confirmation of an LLM-detected
// convergence. chosenTime is the human-readable string; we just stash
// it in result and notify everyone.
func resolveConfirmFreeform(ctx context.Context, app *CoordinationApp, coord *Coordination, chosenTime string) (string, error) {
	if err := UpdateCoordinationStatus(ctx, app.pool, coord.TenantID, coord.ID, StatusConfirmed, &CoordinationResult{}); err != nil {
		return "", err
	}
	parts, _ := ListParticipants(ctx, app.pool, coord.TenantID, coord.ID)
	for _, p := range parts {
		if p.Status != ParticipantResponded {
			continue
		}
		if app.msg == nil {
			break
		}
		var userID uuid.UUID
		if p.UserID != nil {
			userID = *p.UserID
		}
		body := fmt.Sprintf("Confirmed for %s. The organizer will send a calendar invite shortly.", chosenTime)
		_, _ = app.msg.Send(ctx, messenger.SendRequest{
			TenantID:         coord.TenantID,
			Channel:          "slack",
			Recipient:        messenger.Recipient{SlackUserID: p.Identifier},
			Body:             body,
			Origin:           MessengerOrigin,
			OriginRef:        p.ID.String(),
			AwaitReply:       true,
			UserID:           userID,
			SessionThreadKey: coordSessionThreadKey(coord, &p),
		})
	}
	notifyOrganizer(ctx, app, coord, fmt.Sprintf("Confirmed %q for %s. Participants notified. You'll need to send the actual calendar invite — I can't do that yet.", coord.Config.Title, chosenTime))
	return "Coordination confirmed.", nil
}

// resolveSendDrafts dispatches the organizer-approved batch of drafted
// messages. If autoApprove is true, it flips coord.config.auto_approve
// so subsequent batches send without re-prompting.
func resolveSendDrafts(ctx context.Context, app *CoordinationApp, coord *Coordination, drafts []approvalDraft, autoApprove bool) (string, error) {
	if autoApprove && !coord.Config.AutoApprove {
		coord.Config.AutoApprove = true
		if err := UpdateCoordinationConfig(ctx, app.pool, coord.TenantID, coord.ID, coord.Config); err != nil {
			return "", fmt.Errorf("flipping auto_approve: %w", err)
		}
	}
	if app.engine == nil {
		return "", errors.New("engine not configured")
	}
	if err := app.engine.SendApprovedBatch(ctx, coord, drafts); err != nil {
		return "", fmt.Errorf("sending approved batch: %w", err)
	}
	if autoApprove {
		return fmt.Sprintf("Sent %d message(s). Future outbound on this coordination will go without further approval.", len(drafts)), nil
	}
	return fmt.Sprintf("Sent %d message(s).", len(drafts)), nil
}

func findSlotByKey(slots []Slot, key string) *Slot {
	for i := range slots {
		if slots[i].Key() == key {
			return &slots[i]
		}
	}
	return nil
}

// armResponded sets next_nudge_at = now() on every responded participant
// so the next sweep tick re-evaluates them against the new candidate
// set (after a slot rejection or a participant being excluded).
func (a *CoordinationApp) armResponded(ctx context.Context, coord *Coordination) error {
	now := time.Now()
	_, err := a.pool.Exec(ctx, `
		UPDATE app_coordination_participants
		SET next_nudge_at = $3, updated_at = now()
		WHERE tenant_id = $1 AND coordination_id = $2 AND status = 'responded'
	`, coord.TenantID, coord.ID, now)
	return err
}

// notifyOrganizer DMs the organizer in Slack via Messenger. AwaitReply
// is false so any reply they post falls through to the regular agent
// loop (which has access to the coordination tools and can act on
// natural-language follow-ups).
func notifyOrganizer(ctx context.Context, app *CoordinationApp, coord *Coordination, body string) {
	if app.msg == nil {
		return
	}
	user, err := models.GetUserByID(ctx, app.pool, coord.TenantID, coord.OrganizerID)
	if err != nil || user == nil || user.SlackUserID == "" {
		return
	}
	_, err = app.msg.Send(ctx, messenger.SendRequest{
		TenantID:   coord.TenantID,
		Channel:    "slack",
		Recipient:  messenger.Recipient{SlackUserID: user.SlackUserID},
		Body:       body,
		Origin:     MessengerOrigin,
		OriginRef:  coord.ID.String(),
		AwaitReply: false,
		UserID:     coord.OrganizerID,
	})
	if err != nil {
		slog.Error("notifying organizer", "error", err, "coord", coord.ID)
	}
}

// notifyParticipantsConfirmed sends a closure DM to each responded
// participant after the organizer confirms a slot.
func (a *CoordinationApp) notifyParticipantsConfirmed(ctx context.Context, coord *Coordination, slot Slot) error {
	parts, err := ListParticipants(ctx, a.pool, coord.TenantID, coord.ID)
	if err != nil {
		return err
	}
	loc, _ := loadTZ(coord.Config.OrganizerTZ)
	if loc == nil {
		loc = time.UTC
	}
	when := slot.Start.In(loc).Format("Mon Jan 2 at 3:04 PM")
	for _, p := range parts {
		if p.Status != ParticipantResponded {
			continue
		}
		body := fmt.Sprintf("Confirmed for %s. The organizer will send a calendar invite shortly.", when)
		_, err := a.msg.Send(ctx, messenger.SendRequest{
			TenantID:  coord.TenantID,
			Channel:   "slack",
			Recipient: messenger.Recipient{SlackUserID: p.Identifier},
			Body:      body,
			Origin:    MessengerOrigin,
			OriginRef: p.ID.String(),
			// Keep awaiting so late corrections ("actually 11 not 10")
			// still route back to this coord rather than falling through
			// to the agent loop.
			AwaitReply:       true,
			SessionThreadKey: coordSessionThreadKey(coord, &p),
		})
		if err != nil {
			slog.Error("notifying confirmed", "error", err, "participant", p.ID)
		}
	}
	return nil
}
