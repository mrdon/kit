package main

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/cards"
)

// squareSalesCardAdapter lets the squaresales app create its daily briefing
// without importing internal/apps/cards, keeping the dependency graph
// one-way (cards never imports squaresales, and vice versa). Same shape as
// vaultCardAdapter.
//
// The card is system-generated — Kit reporting a tenant's own sales, with
// no human caller whose scope membership could be enforced — so it routes
// through CreateSystemBriefing, which skips the per-caller scope check.
type squareSalesCardAdapter struct {
	svc *cards.CardService
}

func newSquareSalesCardAdapter(svc *cards.CardService) *squareSalesCardAdapter {
	return &squareSalesCardAdapter{svc: svc}
}

func (a *squareSalesCardAdapter) CreateSystemBriefing(ctx context.Context, tenantID uuid.UUID, title, body, severity string, expiresAt *time.Time) error {
	if a.svc == nil {
		return nil
	}
	_, err := a.svc.CreateSystemBriefing(ctx, tenantID, cards.CardCreateInput{
		Kind:      cards.CardKindBriefing,
		Title:     title,
		Body:      body,
		ExpiresAt: expiresAt,
		// Tenant-wide: sales are not role-scoped in this workspace.
		Briefing: &cards.BriefingCreateInput{
			Severity: cards.BriefingSeverity(severity),
		},
	})
	return err
}
