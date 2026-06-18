package main

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/apps/expense"
)

// expenseCardAdapter wraps a CardService so the expense package can raise the
// approval decision card without importing internal/apps/cards directly (keeps
// the dep graph one-way, same pattern as vaultCardAdapter).
//
// The approve/reject options carry the expense tool names + {report_id}; when a
// reviewer resolves the card, CardService.ResolveDecision fires the matching
// tool through the per-caller registry (buildResolveToolExecutor), which runs
// ApproveReport/RejectReport as the resolving caller — so canApprove still
// enforces "not the submitter".
type expenseCardAdapter struct {
	svc *cards.CardService
}

func newExpenseCardAdapter(svc *cards.CardService) *expenseCardAdapter {
	return &expenseCardAdapter{svc: svc}
}

func (a *expenseCardAdapter) CreateApprovalDecision(ctx context.Context, tenantID uuid.UUID, in expense.ApprovalDecisionInput) (uuid.UUID, error) {
	if a.svc == nil {
		return uuid.Nil, nil
	}
	args, err := json.Marshal(map[string]string{"report_id": in.ReportID.String()})
	if err != nil {
		return uuid.Nil, err
	}
	cardIn := cards.CardCreateInput{
		Kind:  cards.CardKindDecision,
		Title: in.Title,
		Body:  in.Body,
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: "approve",
			Options: []cards.DecisionOption{
				{OptionID: "approve", SortOrder: 0, Label: "Approve", ToolName: "approve_expense_report", ToolArguments: args},
				{OptionID: "reject", SortOrder: 1, Label: "Reject", ToolName: "reject_expense_report", ToolArguments: args},
			},
		},
	}
	// A specific assigned approver scopes the card to that user; otherwise it
	// goes to every holder of the owning role.
	if in.ApproverUserID != nil {
		cardIn.UserScopes = []uuid.UUID{*in.ApproverUserID}
	} else {
		cardIn.RoleScopes = []string{in.ApproverRoleName}
	}
	card, err := a.svc.CreateSystemDecision(ctx, tenantID, cardIn)
	if err != nil {
		return uuid.Nil, err
	}
	return card.ID, nil
}
