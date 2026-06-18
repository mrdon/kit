package expense

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/cards/shared"
	"github.com/mrdon/kit/internal/services"
)

// cardProvider surfaces the submitter's actionable reports in the swipe feed:
// drafts to finish & submit, and rejected reports to fix & resubmit. The
// approver-side decision card is raised separately by the cards app at submit
// time (see SubmitReport), so this provider is purely the submitter's view.
type cardProvider struct {
	app *ExpenseApp
}

func (p *cardProvider) SourceApp() string { return "expense" }

func (p *cardProvider) StackItems(ctx context.Context, caller *services.Caller, _ string, limit int) (shared.StackPage, error) {
	if limit <= 0 {
		limit = 50
	}
	// Only the caller's own draft/rejected reports need their attention.
	reports, err := p.app.svc.List(ctx, caller, ReportFilters{MineOnly: true, IncludeClosed: true})
	if err != nil {
		return shared.StackPage{}, err
	}
	items := make([]shared.StackItem, 0)
	for i := range reports {
		r := &reports[i]
		if r.Status != StatusDraft && r.Status != StatusRejected {
			continue
		}
		items = append(items, reportToStackItem(r))
		if len(items) >= limit {
			break
		}
	}
	return shared.StackPage{Items: items}, nil
}

func (p *cardProvider) GetItem(ctx context.Context, caller *services.Caller, kind, id string) (*shared.DetailResponse, error) {
	if kind != "expense" {
		return nil, services.ErrNotFound
	}
	reportID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	r, events, err := p.app.svc.Get(ctx, caller, reportID)
	if err != nil {
		return nil, err
	}
	item := reportToStackItem(r)
	encodedEvents, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("encoding events: %w", err)
	}
	return &shared.DetailResponse{
		Item:   item,
		Extras: map[string]json.RawMessage{"events": encodedEvents},
	}, nil
}

func (p *cardProvider) DoAction(ctx context.Context, caller *services.Caller, kind, id, actionID string, _ json.RawMessage) (*shared.ActionResult, error) {
	if kind != "expense" {
		return nil, services.ErrNotFound
	}
	reportID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	switch actionID {
	case "submit":
		if _, err := p.app.svc.SubmitReport(ctx, caller, reportID); err != nil {
			return nil, err
		}
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("expense", "expense", id)}}, nil
	case "reopen":
		if _, err := p.app.svc.ReopenReport(ctx, caller, reportID); err != nil {
			return nil, err
		}
		// Stays in the feed — now a draft to finish; client refetches.
		return &shared.ActionResult{}, nil
	}
	return nil, fmt.Errorf("unknown expense action %q", actionID)
}

func reportToStackItem(r *Report) shared.StackItem {
	body := fmt.Sprintf("%s · %d item(s) · %s",
		formatCents(r.TotalCents, r.Currency), len(r.Items), statusLabel(r.Status))
	if r.RejectionReason != "" {
		body += "\nRejected: " + r.RejectionReason
	}
	it := shared.StackItem{
		SourceApp:    "expense",
		Kind:         "expense",
		KindLabel:    "Expense",
		Icon:         "🧾",
		ID:           r.ID.String(),
		Title:        r.Title,
		Body:         body,
		KindWeight:   3,
		PriorityTier: shared.TierMedium,
		CreatedAt:    r.CreatedAt,
	}
	switch r.Status {
	case StatusDraft:
		it.Actions = []shared.StackAction{
			{ID: "submit", Direction: "right", Label: "Submit", Emoji: "📤"},
		}
	case StatusRejected:
		it.Actions = []shared.StackAction{
			{ID: "reopen", Direction: "right", Label: "Reopen to fix", Emoji: "↩️"},
		}
	}
	return it
}
