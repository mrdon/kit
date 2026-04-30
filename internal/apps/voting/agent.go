package voting

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// votingTools is the shared metadata for agent + MCP surfaces.
var votingTools = []services.ToolMeta{
	{
		Name: "start_vote",
		Description: `Start a proposal vote across a named participant list. Each participant
gets a decision card in their swipe feed (no Slack DM goes out — the
card itself is the ask). They swipe Approve / Object or tap Abstain;
an optional comment can be attached by long-pressing the card to chat
before resolving.

Use this for quorum sign-off, "do we all agree on this", or any
rubberstamp where you have a named stakeholder list. Don't use it for
casual channel-wide polls — Slack's built-in poll is better for that.

When all participants resolve or the deadline hits, the organizer sees
a digest decision card with verbatim objection reasons and four
options: accept, reject, accept-and-share-with-team, or
reject-and-announce. The "_and_*" variants drop a sanitized briefing
card in each participant's feed; the plain accept/reject record the
decision silently.

Votes are one-shot — there's no automatic compromise round if
objections come in. Don't include the organizer in the participants
list; they're the asker, not a voter.`,
		Schema: services.PropsReq(map[string]any{
			"title":          services.Field("string", "What the vote is about (e.g. 'Adopt new linter settings')"),
			"proposal_text":  services.Field("string", "The full proposal participants are voting on. A few sentences is typical."),
			"participants":   services.Field("array", "Array of Slack user IDs to ask (e.g. ['U09...', 'U07...']). Use find_user first. Don't include the organizer."),
			"context_notes":  services.Field("string", "Optional extra context shown to participants below the proposal."),
			"deadline_hours": services.Field("integer", "Hours until the vote auto-completes with whatever responses arrived. Default 48."),
		}, "title", "proposal_text", "participants"),
	},
	{
		Name:        "list_votes",
		Description: "List your recent votes and their statuses.",
		Schema: services.Props(map[string]any{
			"limit": services.Field("integer", "How many to return (default 25)"),
		}),
	},
	{
		Name:        "get_vote",
		Description: "Get full status of a vote: who voted what, who hasn't, the digest if surfaced.",
		Schema: services.PropsReq(map[string]any{
			"vote_id": services.Field("string", "The vote UUID"),
		}, "vote_id"),
	},
	{
		Name:        "cancel_vote",
		Description: "Cancel an active vote. Outstanding participant cards are left in their feed (they'll resolve as no-ops); the organizer just won't get a digest.",
		Schema: services.PropsReq(map[string]any{
			"vote_id": services.Field("string", "The vote UUID"),
		}, "vote_id"),
	},
}

func registerVotingAgentTools(r *tools.Registry, isAdmin bool, svc *Service) {
	for _, meta := range votingTools {
		r.Register(tools.Def{
			Name:          meta.Name,
			Description:   meta.Description,
			Schema:        meta.Schema,
			DefaultPolicy: tools.PolicyAllow,
			Handler:       agentHandlerFor(meta.Name, svc),
		})
	}
	if svc != nil && svc.app != nil {
		registerResolveCardTool(r, svc.app)
	}
	_ = isAdmin
}

func agentHandlerFor(name string, svc *Service) tools.HandlerFunc {
	switch name {
	case "start_vote":
		return startVoteHandler(svc)
	case "list_votes":
		return listVotesHandler(svc)
	case "get_vote":
		return getVoteHandler(svc)
	case "cancel_vote":
		return cancelVoteHandler(svc)
	}
	return nil
}

func startVoteHandler(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var inp struct {
			Title         string   `json:"title"`
			ProposalText  string   `json:"proposal_text"`
			Participants  []string `json:"participants"`
			ContextNotes  string   `json:"context_notes"`
			DeadlineHours int      `json:"deadline_hours"`
		}
		if err := json.Unmarshal(raw, &inp); err != nil {
			return "", fmt.Errorf("parsing args: %w", err)
		}
		v, err := svc.StartVote(ec.Ctx, ec.Caller(), StartVoteInput{
			Title:         inp.Title,
			ProposalText:  inp.ProposalText,
			ContextNotes:  inp.ContextNotes,
			Participants:  inp.Participants,
			DeadlineHours: inp.DeadlineHours,
		})
		if err != nil {
			return "", err
		}
		deadlineHours := inp.DeadlineHours
		if deadlineHours <= 0 {
			deadlineHours = 48
		}
		return fmt.Sprintf(
			"Vote started (id=%s, %d participant(s)) — each gets a decision card in their feed. "+
				"You'll see a digest card once everyone's responded or the %dh deadline hits. "+
				"When reporting back to the user, do NOT say 'vote passed' or similar — say you're collecting votes, "+
				"since nothing is resolved until you act on the digest.",
			v.ID, len(inp.Participants), deadlineHours,
		), nil
	}
}

func listVotesHandler(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var inp struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(raw, &inp)
		votes, err := svc.ListForCaller(ec.Ctx, ec.Caller(), inp.Limit)
		if err != nil {
			return "", err
		}
		if len(votes) == 0 {
			return "No votes.", nil
		}
		var b strings.Builder
		for _, v := range votes {
			fmt.Fprintf(&b, "- %s [%s] %q (created %s)\n",
				v.ID, v.Status, v.Title, v.CreatedAt.Format(time.RFC3339))
		}
		return b.String(), nil
	}
}

func getVoteHandler(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var inp struct {
			VoteID string `json:"vote_id"`
		}
		if err := json.Unmarshal(raw, &inp); err != nil {
			return "", err
		}
		id, err := uuid.Parse(inp.VoteID)
		if err != nil {
			return "", fmt.Errorf("invalid vote_id: %w", err)
		}
		st, err := svc.GetStatus(ec.Ctx, ec.Caller(), id)
		if err != nil {
			return "", err
		}
		return formatStatus(st), nil
	}
}

func cancelVoteHandler(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var inp struct {
			VoteID string `json:"vote_id"`
		}
		if err := json.Unmarshal(raw, &inp); err != nil {
			return "", err
		}
		id, err := uuid.Parse(inp.VoteID)
		if err != nil {
			return "", fmt.Errorf("invalid vote_id: %w", err)
		}
		if err := svc.Cancel(ec.Ctx, ec.Caller(), id); err != nil {
			return "", err
		}
		return "Vote cancelled.", nil
	}
}

func formatStatus(st *Status) string {
	v := st.Vote
	var b strings.Builder
	fmt.Fprintf(&b, "%q [%s]\n", v.Title, v.Status)
	fmt.Fprintf(&b, "  Created: %s\n", v.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "  Deadline: %s\n", v.DeadlineAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "  Proposal: %s\n", v.ProposalText)
	if v.ContextNotes != "" {
		fmt.Fprintf(&b, "  Context: %s\n", v.ContextNotes)
	}
	fmt.Fprintf(&b, "  Participants:\n")
	for _, p := range st.Participants {
		verdict := "pending"
		if p.Verdict != "" {
			verdict = string(p.Verdict)
		}
		line := fmt.Sprintf("    - %s [%s]", p.Identifier, verdict)
		if p.Reason != "" {
			line += " — " + p.Reason
		}
		fmt.Fprintf(&b, "%s\n", line)
	}
	if v.Outcome != nil {
		fmt.Fprintf(&b, "  Tally: approve=%d, object=%d, abstain=%d, no_response=%d\n",
			v.Outcome.Tally.Approve, v.Outcome.Tally.Object,
			v.Outcome.Tally.Abstain, v.Outcome.Tally.NoResponse)
		if v.Outcome.Action != "" {
			fmt.Fprintf(&b, "  Decision: %s\n", v.Outcome.Action)
		}
	}
	return b.String()
}
