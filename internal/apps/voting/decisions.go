package voting

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// votingResolveCard is the tool name used by decision card options to
// call back into voting on user resolution. Internal — not exposed in
// the system prompt or visible to the agent for direct invocation; only
// fires via card resolution. The same handler dispatches both the
// participant-side resolve (approve/object/abstain) and the organizer
// digest resolve (accept/reject/etc.) — there's only one resolve path
// in voting, so a single tool name is fine.
const votingResolveCard = "voting_resolve_card"

// resolveCardInput is the args shape passed by decision card options.
type resolveCardInput struct {
	VoteID        string `json:"vote_id"`
	Action        string `json:"action"`
	ParticipantID string `json:"participant_id,omitempty"`
}

func registerResolveCardTool(r *tools.Registry, app *VotingApp) {
	r.Register(tools.Def{
		Name:        votingResolveCard,
		Description: "Internal: resolve a voting decision card. Invoked by card options, not by users.",
		Schema: services.PropsReq(map[string]any{
			"vote_id":        services.Field("string", "Vote UUID"),
			"action":         services.Field("string", "approve | object | abstain (participant) | accept | reject | accept_and_share | reject_and_announce (organizer)"),
			"participant_id": services.Field("string", "Participant UUID (only required for participant-side actions)"),
		}, "vote_id", "action"),
		DefaultPolicy:  tools.PolicyAllow,
		AdminOnly:      false,
		DenyCallerGate: true,
		// Internal: only invoked by the card-resolve dispatch path. Keeping
		// the tool out of the LLM's catalog prevents the agent from
		// recording verdicts directly with `action=object` and skipping the
		// CardService.ResolveDecision flow (which is what flips the card to
		// resolved and removes it from the participant's feed).
		Internal: true,
		Handler:  resolveCardHandler(app),
	})
}

func resolveCardHandler(app *VotingApp) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var inp resolveCardInput
		if err := json.Unmarshal(raw, &inp); err != nil {
			return "", fmt.Errorf("parsing args: %w", err)
		}
		voteID, err := uuid.Parse(inp.VoteID)
		if err != nil {
			return "", fmt.Errorf("invalid vote_id: %w", err)
		}
		v, err := GetVote(ec.Ctx, app.pool, ec.Tenant.ID, voteID)
		if err != nil {
			return "", fmt.Errorf("loading vote: %w", err)
		}
		if v == nil {
			return "", errors.New("vote not found")
		}

		switch inp.Action {
		case actionApprove, actionObject, actionAbstain:
			return app.resolveParticipantVoteCard(ec.Ctx, v, inp.Action, inp.ParticipantID)
		case actionAccept, actionReject, actionAcceptAndShare, actionRejectAndAnnounce:
			return app.resolveVoteOrganizerCard(ec.Ctx, v, inp.Action)
		}
		return "", fmt.Errorf("unknown action %q", inp.Action)
	}
}
