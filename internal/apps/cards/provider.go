package cards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/cards/shared"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// cardsProvider adapts CardService to the generic apps.CardProvider
// contract. It preserves the existing decision/briefing tier interleave
// (high decision > medium decision > important briefing > low decision >
// notable briefing > info briefing) by mapping each priority/severity to
// one of the six shared tiers.
type cardsProvider struct {
	app *CardsApp
}

func (p *cardsProvider) SourceApp() string { return "cards" }

func (p *cardsProvider) StackItems(ctx context.Context, caller *services.Caller, cursor string, limit int) (shared.StackPage, error) {
	// The existing Stack() already returns pending cards in the correct
	// tier order and caps at 100 rows. We don't paginate further for now
	// — cursor is reserved for future use.
	_ = cursor
	cards, err := p.app.svc.Stack(ctx, caller)
	if err != nil {
		return shared.StackPage{}, err
	}
	if limit > 0 && len(cards) > limit {
		cards = cards[:limit]
	}
	items := make([]shared.StackItem, 0, len(cards))
	for _, c := range cards {
		it, err := cardToStackItem(c)
		if err != nil {
			return shared.StackPage{}, err
		}
		items = append(items, it)
	}
	return shared.StackPage{Items: items}, nil
}

func (p *cardsProvider) GetItem(ctx context.Context, caller *services.Caller, kind, id string) (*shared.DetailResponse, error) {
	cardID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	card, err := p.app.svc.Get(ctx, caller, cardID)
	if err != nil {
		return nil, err
	}
	if string(card.Kind) != kind {
		return nil, services.ErrNotFound
	}
	item, err := cardToStackItem(card)
	if err != nil {
		return nil, err
	}
	resp := &shared.DetailResponse{Item: item}
	if card.Decision != nil && card.Decision.ResolvedTaskID != nil {
		task, err := models.GetTask(ctx, p.app.pool, caller.TenantID, *card.Decision.ResolvedTaskID)
		if err == nil && task != nil {
			encoded, _ := json.Marshal(map[string]any{
				"id":          task.ID,
				"status":      task.Status,
				"description": task.Description,
				"last_run_at": task.LastRunAt,
				"last_error":  task.LastError,
			})
			resp.Extras = map[string]json.RawMessage{"task": encoded}
		}
	}
	return resp, nil
}

func (p *cardsProvider) DoAction(ctx context.Context, caller *services.Caller, kind, id, actionID string, params json.RawMessage) (*shared.ActionResult, error) {
	cardID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	switch {
	case kind == string(CardKindDecision) && actionID == "resolve":
		var body struct {
			OptionID string `json:"option_id"`
		}
		if len(params) > 0 {
			_ = json.Unmarshal(params, &body)
		}
		slackClient, err := slackClientForCaller(ctx, p.app.svc, caller)
		if err != nil {
			return nil, err
		}
		_, err = p.app.svc.ResolveDecision(ctx, caller, cardID, body.OptionID, slackClient)
		if err != nil {
			return nil, err
		}
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("cards", kind, id)}}, nil
	case kind == string(CardKindBriefing):
		ackKind, ok := briefingAckFromAction(actionID)
		if !ok {
			return nil, fmt.Errorf("unknown briefing action %q", actionID)
		}
		if _, err := p.app.svc.AckBriefing(ctx, caller, cardID, ackKind); err != nil {
			return nil, err
		}
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("cards", kind, id)}}, nil
	}
	return nil, fmt.Errorf("unknown action %s/%s", kind, actionID)
}

// buildDecisionActions turns a decision's options into swipe actions.
// The recommended option is the right swipe (using its own label so a
// gated send_email reads "Send" not "Approve"). For gate-artifact
// cards we also add a left swipe bound to the alternate option so the
// user can reject inline instead of drilling into the detail view —
// it's the gate-card equivalent of a briefing's "Not useful" swipe.
func buildDecisionActions(d *DecisionData) []shared.StackAction {
	if d == nil {
		return nil
	}
	rec := findOption(d.Options, d.RecommendedOptionID)
	rightLabel := "Approve"
	if rec != nil && rec.Label != "" {
		rightLabel = rec.Label
	}
	actions := []shared.StackAction{{
		ID:        "resolve",
		Direction: "right",
		Label:     rightLabel,
		Emoji:     "✅",
	}}
	if !d.IsGateArtifact {
		return actions
	}
	// Gate cards are always "approve / skip" — find the non-recommended
	// option and make it the left swipe.
	for _, opt := range d.Options {
		if opt.OptionID == d.RecommendedOptionID {
			continue
		}
		params, err := json.Marshal(map[string]string{"option_id": opt.OptionID})
		if err != nil {
			break
		}
		label := opt.Label
		if label == "" {
			label = "Skip"
		}
		actions = append(actions, shared.StackAction{
			ID:        "resolve",
			Direction: "left",
			Label:     label,
			Emoji:     "🚫",
			Params:    params,
		})
		break
	}
	return actions
}

// findOption returns the option with the given id, or nil.
func findOption(opts []DecisionOption, id string) *DecisionOption {
	for i := range opts {
		if opts[i].OptionID == id {
			return &opts[i]
		}
	}
	return nil
}

func briefingAckFromAction(actionID string) (BriefingAckKind, bool) {
	switch actionID {
	case "ack_archived":
		return BriefingAckArchived, true
	case "ack_dismissed":
		return BriefingAckDismissed, true
	case "ack_saved":
		return BriefingAckSaved, true
	}
	return "", false
}

// cardToStackItem maps a legacy Card to the shared wire type. The tier
// mapping preserves the order of the old SQL CASE statement.
func cardToStackItem(c *Card) (shared.StackItem, error) {
	it := shared.StackItem{
		SourceApp: "cards",
		Kind:      string(c.Kind),
		ID:        c.ID.String(),
		Title:     c.Title,
		Body:      c.Body,
		CreatedAt: c.CreatedAt,
	}
	switch c.Kind {
	case CardKindDecision:
		it.KindLabel = "Decision"
		it.Icon = "🧭"
		it.KindWeight = 0
		if c.Decision == nil {
			return it, errors.New("decision card missing decision data")
		}
		it.PriorityTier = decisionTier(c.Decision.Priority)
		it.Actions = buildDecisionActions(c.Decision)
		meta, err := json.Marshal(map[string]any{
			"priority":              c.Decision.Priority,
			"recommended_option_id": c.Decision.RecommendedOptionID,
			"resolved_option_id":    c.Decision.ResolvedOptionID,
			"resolved_task_id":      c.Decision.ResolvedTaskID,
			"options":               c.Decision.Options,
		})
		if err != nil {
			return it, fmt.Errorf("encoding decision metadata: %w", err)
		}
		it.Metadata = meta
	case CardKindBriefing:
		it.KindLabel = "Briefing"
		it.Icon = "📣"
		it.KindWeight = 1
		sev := BriefingSeverityInfo
		if c.Briefing != nil {
			sev = c.Briefing.Severity
		}
		it.PriorityTier = briefingTier(sev)
		it.Actions = []shared.StackAction{
			{ID: "ack_archived", Direction: "right", Label: "Useful", Emoji: "👍"},
			{ID: "ack_dismissed", Direction: "left", Label: "Not useful", Emoji: "👎"},
		}
		meta, err := json.Marshal(map[string]any{"severity": sev})
		if err != nil {
			return it, fmt.Errorf("encoding briefing metadata: %w", err)
		}
		it.Metadata = meta
	default:
		return it, fmt.Errorf("unknown card kind %q", c.Kind)
	}
	return it, nil
}

func decisionTier(p DecisionPriority) shared.PriorityTier {
	switch p {
	case DecisionPriorityHigh:
		return shared.TierCritical
	case DecisionPriorityMedium:
		return shared.TierHigh
	case DecisionPriorityLow:
		return shared.TierMedium
	}
	return shared.TierHigh
}

func briefingTier(s BriefingSeverity) shared.PriorityTier {
	switch s {
	case BriefingSeverityImportant:
		return shared.TierElevated
	case BriefingSeverityNotable:
		return shared.TierLow
	case BriefingSeverityInfo:
		return shared.TierMinimal
	}
	return shared.TierMinimal
}
