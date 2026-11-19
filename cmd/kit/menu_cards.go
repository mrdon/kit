package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/apps/menu"
)

// menuCardAdapter lets the menu app's nightly Square check file decision
// cards and briefings without importing internal/apps/cards, the same way
// vaultCardAdapter does for the vault. The cards are Kit's own findings, so
// they go through CreateSystem{Decision,Briefing}: there is no caller whose
// role membership to check.
type menuCardAdapter struct {
	svc *cards.CardService
}

func (a *menuCardAdapter) CreateDecision(ctx context.Context, tenantID uuid.UUID, in menu.CardDecision) (uuid.UUID, error) {
	if a.svc == nil {
		return uuid.Nil, nil
	}
	opts := make([]cards.DecisionOption, 0, len(in.Options))
	for i, o := range in.Options {
		opts = append(opts, cards.DecisionOption{
			OptionID:      o.ID,
			SortOrder:     i,
			Label:         o.Label,
			ToolName:      o.ToolName,
			ToolArguments: o.Args,
		})
	}
	card, err := a.svc.CreateSystemDecision(ctx, tenantID, cards.CardCreateInput{
		Kind:       cards.CardKindDecision,
		Title:      in.Title,
		Body:       in.Body,
		RoleScopes: in.Roles,
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: in.Recommended,
			Options:             opts,
		},
	})
	if err != nil {
		return uuid.Nil, err
	}
	return card.ID, nil
}

func (a *menuCardAdapter) CreateBriefing(ctx context.Context, tenantID uuid.UUID, title, body string, roles []string) error {
	if a.svc == nil {
		return nil
	}
	_, err := a.svc.CreateSystemBriefing(ctx, tenantID, cards.CardCreateInput{
		Kind:       cards.CardKindBriefing,
		Title:      title,
		Body:       body,
		RoleScopes: roles,
		Briefing:   &cards.BriefingCreateInput{Severity: cards.BriefingSeverityInfo},
	})
	return err
}
