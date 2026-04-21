package cards

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

func registerCardsAgentTools(r *tools.Registry, isAdmin bool, svc *CardService) {
	for _, meta := range cardsTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     cardsAgentHandler(meta.Name, svc),
		})
	}
}

func cardsAgentHandler(name string, svc *CardService) tools.HandlerFunc {
	switch name {
	case ToolCreateDecision:
		return handleCreateDecision(svc)
	case ToolCreateBriefing:
		return handleCreateBriefing(svc)
	case ToolUpdateDecision:
		return handleUpdateDecision(svc)
	case ToolUpdateBriefing:
		return handleUpdateBriefing(svc)
	case ToolListDecisions:
		return handleListDecisions(svc)
	case ToolListBriefings:
		return handleListBriefings(svc)
	case ToolAckBriefing:
		return handleAckBriefing(svc)
	case ToolResolveDecision:
		return handleResolveDecision(svc)
	case ToolReviseDecisionOption:
		return handleReviseDecisionOption(svc)
	case ToolGetDecisionToolResult:
		return handleGetDecisionToolResult(svc)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown cards tool: %s", name)
		}
	}
}

type createDecisionInput struct {
	Title               string           `json:"title"`
	Context             string           `json:"context"`
	Options             []DecisionOption `json:"options"`
	RecommendedOptionID string           `json:"recommended_option_id"`
	Priority            string           `json:"priority"`
	RoleScopes          []string         `json:"role_scopes"`
}

func handleCreateDecision(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp createDecisionInput
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		var originSessionID *uuid.UUID
		if ec.TaskID != nil && ec.Session != nil {
			sid := ec.Session.ID
			originSessionID = &sid
		}
		card, err := svc.CreateDecision(ec.Ctx, ec.Caller(), CardCreateInput{
			Title:      inp.Title,
			Body:       inp.Context,
			RoleScopes: inp.RoleScopes,
			Decision: &DecisionCreateInput{
				Priority:            DecisionPriority(inp.Priority),
				RecommendedOptionID: inp.RecommendedOptionID,
				Options:             inp.Options,
				OriginTaskID:        ec.TaskID,
				OriginSessionID:     originSessionID,
			},
		})
		if err != nil {
			return handleServiceErr(err, "creating decision")
		}
		return fmt.Sprintf("Created decision card [%s]: %s (%d options)", card.ID, card.Title, len(card.Decision.Options)), nil
	}
}

type createBriefingInput struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Severity   string   `json:"severity"`
	RoleScopes []string `json:"role_scopes"`
}

func handleCreateBriefing(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp createBriefingInput
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		card, err := svc.CreateBriefing(ec.Ctx, ec.Caller(), CardCreateInput{
			Title:      inp.Title,
			Body:       inp.Body,
			RoleScopes: inp.RoleScopes,
			Briefing:   &BriefingCreateInput{Severity: BriefingSeverity(inp.Severity)},
		})
		if err != nil {
			return handleServiceErr(err, "creating briefing")
		}
		return fmt.Sprintf("Created briefing card [%s]: %s", card.ID, card.Title), nil
	}
}

type updateDecisionInput struct {
	CardID              string            `json:"card_id"`
	Title               *string           `json:"title,omitempty"`
	Context             *string           `json:"context,omitempty"`
	Priority            *string           `json:"priority,omitempty"`
	RecommendedOptionID *string           `json:"recommended_option_id,omitempty"`
	Options             *[]DecisionOption `json:"options,omitempty"`
	State               *string           `json:"state,omitempty"`
	RoleScopes          *[]string         `json:"role_scopes,omitempty"`
}

func handleUpdateDecision(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp updateDecisionInput
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		cardID, err := uuid.Parse(inp.CardID)
		if err != nil {
			return "Invalid card_id.", nil
		}
		u := CardUpdates{Title: inp.Title, Body: inp.Context, RoleScopes: inp.RoleScopes}
		if inp.State != nil {
			s := CardState(*inp.State)
			u.State = &s
		}
		if inp.Priority != nil || inp.RecommendedOptionID != nil || inp.Options != nil {
			d := &DecisionUpdates{RecommendedOptionID: inp.RecommendedOptionID, Options: inp.Options}
			if inp.Priority != nil {
				p := DecisionPriority(*inp.Priority)
				d.Priority = &p
			}
			u.Decision = d
		}
		card, err := svc.Update(ec.Ctx, ec.Caller(), cardID, u)
		if err != nil {
			return handleServiceErr(err, "updating decision")
		}
		return fmt.Sprintf("Updated decision [%s]: %s", card.ID, card.Title), nil
	}
}

type updateBriefingInput struct {
	CardID     string    `json:"card_id"`
	Title      *string   `json:"title,omitempty"`
	Body       *string   `json:"body,omitempty"`
	Severity   *string   `json:"severity,omitempty"`
	State      *string   `json:"state,omitempty"`
	RoleScopes *[]string `json:"role_scopes,omitempty"`
}

func handleUpdateBriefing(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp updateBriefingInput
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		cardID, err := uuid.Parse(inp.CardID)
		if err != nil {
			return "Invalid card_id.", nil
		}
		u := CardUpdates{Title: inp.Title, Body: inp.Body, RoleScopes: inp.RoleScopes}
		if inp.State != nil {
			s := CardState(*inp.State)
			u.State = &s
		}
		if inp.Severity != nil {
			sev := BriefingSeverity(*inp.Severity)
			u.Briefing = &BriefingUpdates{Severity: &sev}
		}
		card, err := svc.Update(ec.Ctx, ec.Caller(), cardID, u)
		if err != nil {
			return handleServiceErr(err, "updating briefing")
		}
		return fmt.Sprintf("Updated briefing [%s]: %s", card.ID, card.Title), nil
	}
}

func handleListDecisions(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			State    string `json:"state"`
			Priority string `json:"priority"`
		}
		_ = json.Unmarshal(input, &inp)
		cards, err := svc.ListDecisions(ec.Ctx, ec.Caller(), CardFilters{
			State:    CardState(inp.State),
			Priority: DecisionPriority(inp.Priority),
		})
		if err != nil {
			return handleServiceErr(err, "listing decisions")
		}
		return formatCardList(cards, "decisions"), nil
	}
}

func handleListBriefings(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			State    string `json:"state"`
			Severity string `json:"severity"`
		}
		_ = json.Unmarshal(input, &inp)
		cards, err := svc.ListBriefings(ec.Ctx, ec.Caller(), CardFilters{
			State:    CardState(inp.State),
			Severity: BriefingSeverity(inp.Severity),
		})
		if err != nil {
			return handleServiceErr(err, "listing briefings")
		}
		return formatCardList(cards, "briefings"), nil
	}
}

func handleAckBriefing(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			CardID string `json:"card_id"`
			Kind   string `json:"kind"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		cardID, err := uuid.Parse(inp.CardID)
		if err != nil {
			return "Invalid card_id.", nil
		}
		card, err := svc.AckBriefing(ec.Ctx, ec.Caller(), cardID, BriefingAckKind(inp.Kind))
		if err != nil {
			if errors.Is(err, ErrAlreadyTerminal) {
				return "That briefing has already been acknowledged.", nil
			}
			return handleServiceErr(err, "acknowledging briefing")
		}
		return fmt.Sprintf("Briefing [%s] marked %s.", card.ID, card.State), nil
	}
}

func handleResolveDecision(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			CardID   string `json:"card_id"`
			OptionID string `json:"option_id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		cardID, err := uuid.Parse(inp.CardID)
		if err != nil {
			return "Invalid card_id.", nil
		}
		if ec.Slack == nil {
			return "Slack client unavailable — cannot resolve from this context.", nil
		}
		card, err := svc.ResolveDecisionFromAgent(ec.Ctx, ec.Caller(), cardID, inp.OptionID, ec.Slack)
		if err != nil {
			if errors.Is(err, ErrGatedResolveFromAgent) {
				return "That decision is gated — the user has to approve it on the card. Tell them it's queued for their review; don't retry.", nil
			}
			if errors.Is(err, ErrAlreadyTerminal) {
				return "That decision has already been resolved.", nil
			}
			if errors.Is(err, ErrOptionNotFound) {
				return "That option does not exist on this decision.", nil
			}
			if errors.Is(err, ErrNoOptionPicked) {
				return "No option chosen and no recommended option is set.", nil
			}
			return handleServiceErr(err, "resolving decision")
		}
		msg := fmt.Sprintf("Decision [%s] resolved with option %q.", card.ID, card.Decision.ResolvedOptionID)
		if card.Decision.ResolvedTaskID != nil {
			msg += fmt.Sprintf(" Kit queued task %s to act on it.", *card.Decision.ResolvedTaskID)
		}
		return msg, nil
	}
}

type reviseDecisionOptionInput struct {
	CardID        string          `json:"card_id"`
	OptionID      string          `json:"option_id"`
	ToolArguments json.RawMessage `json:"tool_arguments,omitempty"`
	Prompt        *string         `json:"prompt,omitempty"`
}

// handleReviseDecisionOption handles the narrow revise tool. Only
// tool_arguments and prompt can change; tool_name / label / option_id /
// sort_order are immutable post-creation. The service layer enforces
// this (the schema just omits the fields as defense-in-depth).
func handleReviseDecisionOption(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp reviseDecisionOptionInput
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		cardID, err := uuid.Parse(inp.CardID)
		if err != nil {
			return "Invalid card_id.", nil
		}
		if inp.OptionID == "" {
			return "option_id is required.", nil
		}
		var newArgs *json.RawMessage
		if len(inp.ToolArguments) > 0 && !isJSONNull(inp.ToolArguments) {
			newArgs = &inp.ToolArguments
		}
		opt, err := svc.ReviseDecisionOption(ec.Ctx, ec.Caller(), cardID, inp.OptionID, newArgs, inp.Prompt)
		if err != nil {
			if errors.Is(err, ErrAlreadyTerminal) {
				return "That card is no longer pending and can't be revised.", nil
			}
			if errors.Is(err, ErrOptionNotFound) {
				return "That option does not exist on this card.", nil
			}
			return handleServiceErr(err, "revising decision option")
		}
		return fmt.Sprintf("Revised option %q on decision [%s].", opt.OptionID, cardID), nil
	}
}

// isJSONNull reports whether the raw message is literal JSON null.
// Used to treat an explicit null as "don't change" rather than
// clearing to an empty-args value.
func isJSONNull(raw json.RawMessage) bool {
	for _, b := range raw {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		return b == 'n'
	}
	return false
}

// handleGetDecisionToolResult returns the full tool output for a
// resolved decision card. Used by the resumed authoring agent when the
// replay-truncated 2KB version is insufficient.
func handleGetDecisionToolResult(svc *CardService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			CardID string `json:"card_id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		cardID, err := uuid.Parse(inp.CardID)
		if err != nil {
			return "Invalid card_id.", nil
		}
		card, err := svc.Get(ec.Ctx, ec.Caller(), cardID)
		if err != nil {
			return handleServiceErr(err, "loading decision")
		}
		if card.Kind != CardKindDecision || card.Decision == nil {
			return "Card is not a decision.", nil
		}
		if card.Decision.ResolvedToolResult == "" {
			return "No tool result recorded on this card.", nil
		}
		return card.Decision.ResolvedToolResult, nil
	}
}

// handleServiceErr converts common service errors into user-facing strings
// and keeps everything else as an actual error.
func handleServiceErr(err error, action string) (string, error) {
	if errors.Is(err, services.ErrForbidden) {
		return "You don't have permission to do that.", nil
	}
	if errors.Is(err, services.ErrNotFound) || errors.Is(err, ErrCardNotFound) {
		return "Card not found.", nil
	}
	return "", fmt.Errorf("%s: %w", action, err)
}

// formatCardList renders a small list of cards for the agent.
func formatCardList(cards []*Card, label string) string {
	if len(cards) == 0 {
		return fmt.Sprintf("No %s matched.", label)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s:\n", len(cards), label)
	for _, c := range cards {
		b.WriteString(formatCardLine(c))
		b.WriteString("\n")
	}
	return b.String()
}

func formatCardLine(c *Card) string {
	var extra string
	switch c.Kind {
	case CardKindDecision:
		if c.Decision != nil {
			extra = fmt.Sprintf(" [%s, %d options]", c.Decision.Priority, len(c.Decision.Options))
		}
	case CardKindBriefing:
		if c.Briefing != nil {
			extra = fmt.Sprintf(" [severity %s]", c.Briefing.Severity)
		}
	}
	return fmt.Sprintf("- [%s] %s — %s (state: %s)%s", c.ID, c.Title, c.Kind, c.State, extra)
}
