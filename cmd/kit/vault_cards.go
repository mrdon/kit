package main

import (
	"context"
	"encoding/json"

	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/apps/vault"
	"github.com/mrdon/kit/internal/services"
)

// vaultCardAdapter wraps a CardService so the vault package can create
// decision cards and briefings without importing internal/apps/cards
// directly. Keeps the dep graph one-way (cards never imports vault, and
// vault never imports cards).
type vaultCardAdapter struct {
	svc *cards.CardService
}

func newVaultCardAdapter(svc *cards.CardService) *vaultCardAdapter {
	return &vaultCardAdapter{svc: svc}
}

func (a *vaultCardAdapter) CreateDecision(ctx context.Context, c *services.Caller, in vault.CardCreateInput) error {
	if a.svc == nil || in.Decision == nil {
		return nil
	}

	opts := make([]cards.DecisionOption, 0, len(in.Decision.Options))
	for i, o := range in.Decision.Options {
		opts = append(opts, cards.DecisionOption{
			OptionID:      o.OptionID,
			SortOrder:     i,
			Label:         o.Label,
			ToolName:      o.ToolName,
			ToolArguments: json.RawMessage(o.Arguments),
		})
	}

	prio := cards.DecisionPriority(in.Decision.Priority)
	if !prio.Valid() {
		prio = cards.DecisionPriorityMedium
	}

	_, err := a.svc.CreateDecision(ctx, c, cards.CardCreateInput{
		Kind:       cards.CardKindDecision,
		Title:      in.Title,
		Body:       in.Body,
		RoleScopes: in.RoleScopes,
		UserScopes: in.UserScopes,
		Urgent:     in.Urgent,
		Decision: &cards.DecisionCreateInput{
			Priority:            prio,
			RecommendedOptionID: in.Decision.RecommendedOptionID,
			Options:             opts,
		},
	})
	return err
}

func (a *vaultCardAdapter) CreateBriefing(ctx context.Context, c *services.Caller, in vault.CardCreateInput) error {
	if a.svc == nil {
		return nil
	}
	sev := cards.BriefingSeverityInfo
	if in.Briefing != nil {
		s := cards.BriefingSeverity(in.Briefing.Severity)
		if s.Valid() {
			sev = s
		}
	}
	_, err := a.svc.CreateBriefing(ctx, c, cards.CardCreateInput{
		Kind:       cards.CardKindBriefing,
		Title:      in.Title,
		Body:       in.Body,
		RoleScopes: in.RoleScopes,
		UserScopes: in.UserScopes,
		Urgent:     in.Urgent,
		Briefing:   &cards.BriefingCreateInput{Severity: sev},
	})
	return err
}
