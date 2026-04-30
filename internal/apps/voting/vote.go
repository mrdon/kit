package voting

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
	"github.com/mrdon/kit/internal/chat"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// Action names used by the resolve handler. Participant actions
// (approve / object / abstain) live on the per-participant decision
// card; organizer actions live on the digest card.
const (
	actionApprove           = "approve"
	actionObject            = "object"
	actionAbstain           = "abstain"
	actionAccept            = "accept"
	actionReject            = "reject"
	actionAcceptAndShare    = "accept_and_share"
	actionRejectAndAnnounce = "reject_and_announce"
)

// surfaceParticipantVoteCards creates one decision card per participant
// scoped to that participant. Idempotent — skips any participant whose
// vote_card_id is already set.
func (a *VotingApp) surfaceParticipantVoteCards(ctx context.Context, v *Vote) error {
	if a.cards == nil {
		return errors.New("CardService not configured")
	}
	parts, err := ListParticipants(ctx, a.pool, v.TenantID, v.ID)
	if err != nil {
		return fmt.Errorf("listing participants: %w", err)
	}
	organizerName := a.lookupOrganizerName(ctx, v)
	for i := range parts {
		p := parts[i]
		if p.VoteCardID != nil {
			continue
		}
		if p.UserID == nil {
			slog.Warn("vote participant lacks kit user; skipping card",
				"vote", v.ID, "slack_id", p.Identifier)
			continue
		}
		caller, err := a.callerForUser(ctx, v.TenantID, *p.UserID)
		if err != nil {
			slog.Error("building participant caller", "error", err, "participant", p.ID)
			continue
		}
		body := mustRender("user_vote_participant_body.tmpl", map[string]any{
			"OrganizerName": organizerName,
			"Title":         v.Title,
			"ProposalText":  v.ProposalText,
			"ContextNotes":  v.ContextNotes,
		})

		approveArgs, _ := json.Marshal(map[string]any{
			"vote_id":        v.ID.String(),
			"action":         actionApprove,
			"participant_id": p.ID.String(),
		})
		objectArgs, _ := json.Marshal(map[string]any{
			"vote_id":        v.ID.String(),
			"action":         actionObject,
			"participant_id": p.ID.String(),
		})
		abstainArgs, _ := json.Marshal(map[string]any{
			"vote_id":        v.ID.String(),
			"action":         actionAbstain,
			"participant_id": p.ID.String(),
		})

		card, err := a.cards.CreateDecision(ctx, caller, cards.CardCreateInput{
			Kind:       cards.CardKindDecision,
			Title:      fmt.Sprintf("%s wants your vote on %q", organizerName, v.Title),
			Body:       body,
			UserScopes: []uuid.UUID{*p.UserID},
			Decision: &cards.DecisionCreateInput{
				Priority:            cards.DecisionPriorityHigh,
				RecommendedOptionID: actionApprove,
				Options: []cards.DecisionOption{
					{OptionID: actionApprove, Label: "Approve", ToolName: votingResolveCard, ToolArguments: approveArgs},
					{OptionID: actionObject, Label: "Object", ToolName: votingResolveCard, ToolArguments: objectArgs},
					{OptionID: actionAbstain, Label: "Abstain", ToolName: votingResolveCard, ToolArguments: abstainArgs},
				},
			},
		})
		if err != nil {
			slog.Error("creating participant vote card", "error", err, "participant", p.ID)
			continue
		}
		if err := UpdateParticipantCardID(ctx, a.pool, v.TenantID, p.ID, card.ID); err != nil {
			slog.Error("persisting vote card id", "error", err, "participant", p.ID)
		}
	}
	return nil
}

// resolveParticipantVoteCard records a participant's verdict and reads
// their card-scoped chat for any reason text. Idempotent at the DB
// level via UpdateParticipantVerdict's `WHERE verdict IS NULL`. The
// next cron tick (≤60s) surfaces the organizer's digest card if this
// was the last outstanding vote — we don't kick the engine
// synchronously because that opens a race against a concurrent Tick
// that can double-surface the digest.
func (a *VotingApp) resolveParticipantVoteCard(ctx context.Context, v *Vote, action, participantRef string) (string, error) {
	if v.Status != StatusActive {
		return "This vote is no longer active.", nil
	}
	if participantRef == "" {
		return "", errors.New("participant_id required")
	}
	pid, err := uuid.Parse(participantRef)
	if err != nil {
		return "", fmt.Errorf("invalid participant_id: %w", err)
	}
	p, err := GetParticipant(ctx, a.pool, v.TenantID, pid)
	if err != nil {
		return "", fmt.Errorf("loading participant: %w", err)
	}
	if p == nil {
		return "", fmt.Errorf("participant %s not found", pid)
	}

	verdict, err := verdictFromAction(action)
	if err != nil {
		return "", err
	}

	reason := ""
	if p.UserID != nil && p.VoteCardID != nil {
		reason = a.readVoteCardChat(ctx, v.TenantID, *p.UserID, *p.VoteCardID)
	}

	recorded, err := UpdateParticipantVerdict(ctx, a.pool, v.TenantID, p.ID, verdict, reason, time.Now())
	if err != nil {
		return "", fmt.Errorf("updating participant verdict: %w", err)
	}
	if !recorded {
		return "Vote already recorded.", nil
	}

	slog.Info("vote response recorded",
		"vote", v.ID, "participant", p.Identifier, "verdict", verdict, "has_reason", reason != "")

	switch verdict {
	case VerdictApprove:
		return "Recorded — thanks.", nil
	case VerdictObject:
		return "Got it — objection noted. I'll pass that along.", nil
	case VerdictAbstain:
		return "Recorded as abstain.", nil
	}
	return "Recorded.", nil
}

// surfaceVoteOrganizerCard creates the digest decision card for the
// organizer once all participants have resolved or the deadline has
// passed.
//
// Idempotency: Outcome.Tally is non-zero once the digest has been
// surfaced. Because the cron's two sweeps and any synchronous resolve
// path all come through this function, the in-memory check is enough
// for non-concurrent callers. If two cron Ticks ever ran in parallel
// against the same vote (they don't today — single-instance scheduler),
// this would double-surface; a row lock or `WHERE outcome IS NULL`
// guard in the persist would close that loophole.
func (a *VotingApp) surfaceVoteOrganizerCard(ctx context.Context, v *Vote) error {
	if a.cards == nil {
		return errors.New("CardService not configured")
	}
	if v.Outcome != nil && v.Outcome.Tally != (Tally{}) {
		return nil
	}
	parts, err := ListParticipants(ctx, a.pool, v.TenantID, v.ID)
	if err != nil {
		return fmt.Errorf("listing participants: %w", err)
	}
	tally := buildTally(parts)
	headline := buildHeadline(tally)
	body := a.buildOrganizerBody(ctx, v, parts, tally)

	caller, err := a.callerForUser(ctx, v.TenantID, v.OrganizerID)
	if err != nil {
		return fmt.Errorf("building organizer caller: %w", err)
	}

	acceptArgs, _ := json.Marshal(map[string]any{"vote_id": v.ID.String(), "action": actionAccept})
	rejectArgs, _ := json.Marshal(map[string]any{"vote_id": v.ID.String(), "action": actionReject})
	acceptShareArgs, _ := json.Marshal(map[string]any{"vote_id": v.ID.String(), "action": actionAcceptAndShare})
	rejectAnnArgs, _ := json.Marshal(map[string]any{"vote_id": v.ID.String(), "action": actionRejectAndAnnounce})

	_, err = a.cards.CreateDecision(ctx, caller, cards.CardCreateInput{
		Kind:       cards.CardKindDecision,
		Title:      fmt.Sprintf("Vote on %q: %s", v.Title, headline),
		Body:       body,
		UserScopes: []uuid.UUID{v.OrganizerID},
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: actionAccept,
			Options: []cards.DecisionOption{
				{OptionID: actionAccept, Label: "Accept", ToolName: votingResolveCard, ToolArguments: acceptArgs},
				{OptionID: actionReject, Label: "Reject", ToolName: votingResolveCard, ToolArguments: rejectArgs},
				{OptionID: actionAcceptAndShare, Label: "Accept & share with team", ToolName: votingResolveCard, ToolArguments: acceptShareArgs},
				{OptionID: actionRejectAndAnnounce, Label: "Reject & announce", ToolName: votingResolveCard, ToolArguments: rejectAnnArgs},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating organizer card: %w", err)
	}

	v.Outcome = &Outcome{Tally: tally}
	if err := UpdateVoteStatus(ctx, a.pool, v.TenantID, v.ID, v.Status, v.Outcome); err != nil {
		return fmt.Errorf("persisting vote tally: %w", err)
	}

	slog.Info("vote card surfaced", "vote", v.ID, "tally", tally, "headline", headline)
	return nil
}

// resolveVoteOrganizerCard handles the organizer's choice on the digest
// card. Flips status, fills outcome.action, and broadcasts result
// briefings to participants for the "_and_*" variants.
func (a *VotingApp) resolveVoteOrganizerCard(ctx context.Context, v *Vote, action string) (string, error) {
	if v.Status != StatusActive {
		return fmt.Sprintf("This vote is already %s.", v.Status), nil
	}
	switch action {
	case actionAccept, actionReject, actionAcceptAndShare, actionRejectAndAnnounce:
	default:
		return "", fmt.Errorf("unknown vote organizer action %q", action)
	}
	if v.Outcome == nil {
		// Defensive: rebuild the tally if status flips before the
		// surface helper persisted (shouldn't happen — surface always
		// stamps tally before the digest card is created).
		parts, _ := ListParticipants(ctx, a.pool, v.TenantID, v.ID)
		v.Outcome = &Outcome{Tally: buildTally(parts)}
	}
	v.Outcome.Action = action

	newStatus := StatusConfirmed
	if action == actionReject || action == actionRejectAndAnnounce {
		newStatus = StatusAbandoned
	}
	if err := UpdateVoteStatus(ctx, a.pool, v.TenantID, v.ID, newStatus, v.Outcome); err != nil {
		return "", fmt.Errorf("updating vote status: %w", err)
	}
	v.Status = newStatus

	if action == actionAcceptAndShare || action == actionRejectAndAnnounce {
		if err := a.broadcastVoteResult(ctx, v, action); err != nil {
			slog.Error("broadcasting vote result", "error", err, "vote", v.ID)
		}
	}

	slog.Info("vote organizer decision", "vote", v.ID, "action", action)
	return fmt.Sprintf("Vote resolved: %s.", action), nil
}

// broadcastVoteResult creates a sanitized briefing card in each
// participant's feed. No verbatim objection reasons — those stay
// private to the organizer who saw them on the digest card.
func (a *VotingApp) broadcastVoteResult(ctx context.Context, v *Vote, action string) error {
	parts, err := ListParticipants(ctx, a.pool, v.TenantID, v.ID)
	if err != nil {
		return err
	}
	organizerName := a.lookupOrganizerName(ctx, v)
	outcome := "accepted"
	severity := cards.BriefingSeverityInfo
	if action == actionRejectAndAnnounce {
		outcome = "decided not to move forward"
		severity = cards.BriefingSeverityNotable
	}
	tally := Tally{}
	if v.Outcome != nil {
		tally = v.Outcome.Tally
	}
	body := mustRender("user_vote_recap.tmpl", map[string]any{
		"OrganizerName": organizerName,
		"Title":         v.Title,
		"Outcome":       outcome,
		"Approve":       tally.Approve,
		"Object":        tally.Object,
		"Abstain":       tally.Abstain,
	})
	for i := range parts {
		p := parts[i]
		if p.UserID == nil {
			continue
		}
		caller, err := a.callerForUser(ctx, v.TenantID, *p.UserID)
		if err != nil {
			slog.Error("building participant caller", "error", err, "participant", p.ID)
			continue
		}
		title := fmt.Sprintf("Vote on %q: %s", v.Title, outcome)
		if _, err := a.cards.CreateBriefing(ctx, caller, cards.CardCreateInput{
			Kind:       cards.CardKindBriefing,
			Title:      title,
			Body:       body,
			UserScopes: []uuid.UUID{*p.UserID},
			Briefing:   &cards.BriefingCreateInput{Severity: severity},
		}); err != nil {
			slog.Error("creating recap briefing", "error", err, "participant", p.ID)
		}
	}
	return nil
}

// verdictFromAction maps a participant action name to a verdict.
func verdictFromAction(action string) (VoteVerdict, error) {
	switch action {
	case actionApprove:
		return VerdictApprove, nil
	case actionObject:
		return VerdictObject, nil
	case actionAbstain:
		return VerdictAbstain, nil
	default:
		return "", fmt.Errorf("unknown vote action %q", action)
	}
}

// buildTally counts each verdict bucket plus the no-response bucket
// for participants who never resolved their card.
func buildTally(parts []Participant) Tally {
	var t Tally
	for _, p := range parts {
		switch p.Verdict {
		case VerdictApprove:
			t.Approve++
		case VerdictObject:
			t.Object++
		case VerdictAbstain:
			t.Abstain++
		default:
			t.NoResponse++
		}
	}
	return t
}

// buildHeadline picks a short summary line for the digest title.
func buildHeadline(t Tally) string {
	if t.Object > 0 {
		if t.Object == 1 {
			return "1 objection"
		}
		return fmt.Sprintf("%d objections", t.Object)
	}
	if t.NoResponse > 0 {
		if t.NoResponse == 1 {
			return "1 no response"
		}
		return fmt.Sprintf("%d no response", t.NoResponse)
	}
	return "unanimous approve"
}

// buildOrganizerBody renders the organizer digest card body with
// per-participant verdicts and verbatim reasons (only the organizer
// sees this).
func (a *VotingApp) buildOrganizerBody(ctx context.Context, v *Vote, parts []Participant, t Tally) string {
	type partView struct {
		Name    string
		Verdict string
		Reason  string
	}
	views := make([]partView, 0, len(parts))
	for _, p := range parts {
		name := p.Identifier
		if p.UserID != nil {
			if u, err := models.GetUserByID(ctx, a.pool, v.TenantID, *p.UserID); err == nil && u != nil && u.DisplayName != nil && *u.DisplayName != "" {
				name = *u.DisplayName
			}
		}
		view := partView{Name: name}
		if p.Verdict == "" {
			view.Verdict = "no response"
		} else {
			view.Verdict = string(p.Verdict)
			view.Reason = p.Reason
		}
		views = append(views, view)
	}
	return mustRender("user_vote_organizer_body.tmpl", map[string]any{
		"Title":        v.Title,
		"ProposalText": v.ProposalText,
		"ContextNotes": v.ContextNotes,
		"Approve":      t.Approve,
		"Object":       t.Object,
		"Abstain":      t.Abstain,
		"NoResponse":   t.NoResponse,
		"Participants": views,
	})
}

// readVoteCardChat returns the participant's accumulated chat messages
// on the per-participant vote card session. Returns "" on any lookup
// failure — chat is optional and missing-=-empty-reason is expected
// for participants who didn't long-press to add context.
//
// Reads message_received events keyed under chat.SentinelChannel +
// CardThreadKey("cards", "decision", cardID, userID) — the same key
// the long-press chat orchestrator uses, so any text the participant
// typed into the card-chat lands here.
func (a *VotingApp) readVoteCardChat(ctx context.Context, tenantID, userID, cardID uuid.UUID) string {
	threadKey := chat.CardThreadKey("cards", "decision", cardID.String(), userID)
	session, err := models.FindSessionByThread(ctx, a.pool, tenantID, chat.SentinelChannel, threadKey)
	if err != nil || session == nil {
		return ""
	}
	events, err := models.GetSessionEvents(ctx, a.pool, tenantID, session.ID)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range events {
		if e.EventType != models.EventTypeMessageReceived {
			continue
		}
		var data struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(e.Data, &data); err != nil {
			continue
		}
		msg := strings.TrimSpace(data.Text)
		if msg == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(msg)
	}
	return strings.TrimSpace(b.String())
}

func (a *VotingApp) lookupOrganizerName(ctx context.Context, v *Vote) string {
	if u, err := models.GetUserByID(ctx, a.pool, v.TenantID, v.OrganizerID); err == nil && u != nil && u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return "the organizer"
}

// callerForUser builds a services.Caller for the given user so cards
// created on their behalf land in their feed only (via the per-user
// scope row written by writeScopesTx).
func (a *VotingApp) callerForUser(ctx context.Context, tenantID, userID uuid.UUID) (*services.Caller, error) {
	user, err := models.GetUserByID(ctx, a.pool, tenantID, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("user %s not found", userID)
	}
	tenant, err := models.GetTenantByID(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, fmt.Errorf("tenant %s not found", tenantID)
	}
	return &services.Caller{
		TenantID: tenantID,
		UserID:   userID,
		Identity: user.SlackUserID,
		Timezone: services.ResolveTimezone(user.Timezone, tenant.Timezone),
	}, nil
}
