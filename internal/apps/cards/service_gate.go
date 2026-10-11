package cards

// Gate-card creation and per-option revision. CreateGateCard is the
// tools.GateCreator implementation that Registry.Execute calls when a
// PolicyGate tool is invoked without an approval token;
// CreateGateCardForCaller is the same shape for surfaces without a full
// ExecContext (MCP). ReviseDecisionOption is the narrow mutation path the
// chat-revision flow uses on a pending card's option.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// CreateGateCard implements tools.GateCreator. When Registry.Execute
// intercepts a PolicyGate tool call without an approval token, it
// calls this to mint the decision card the user will approve. The
// card has two options: an Approve option carrying the intercepted
// tool_name + tool_arguments, and a Skip option that cancels.
// OriginJobID/OriginSessionID are stamped from the ExecContext so
// the authoring session resumes on approve. is_gate_artifact is
// explicitly true so ResolveDecision's re-check passes.
//
// Title is auto-generated from tool_name for MVP — the email PR (or
// any future gated-tool PR) can extend this with a per-tool title
// helper, but the default is workable.
func (s *CardService) CreateGateCard(
	ctx context.Context, ec *tools.ExecContext,
	toolName string, toolArguments json.RawMessage,
	preview tools.GateCardPreview,
) (uuid.UUID, string, error) {
	if ec == nil || ec.Tenant == nil || ec.User == nil {
		return uuid.Nil, "", errors.New("gate creation requires tenant + user on ec")
	}
	caller := ec.Caller()
	var originSessionID *uuid.UUID
	if ec.Session != nil {
		sid := ec.Session.ID
		originSessionID = &sid
	}

	title := preview.Title
	if title == "" {
		title = "Kit needs your approval"
	}
	body := preview.Body
	if body == "" {
		body = "Kit drafted the action shown below. Review it and approve to continue, or skip to cancel."
	}
	approveLabel := preview.ApproveLabel
	if approveLabel == "" {
		approveLabel = "Approve"
	}
	skipLabel := preview.SkipLabel
	if skipLabel == "" {
		skipLabel = "Skip"
	}

	in := CardCreateInput{
		Kind:  CardKindDecision,
		Title: title,
		Body:  body,
		Decision: &DecisionCreateInput{
			Priority:            DecisionPriorityHigh,
			RecommendedOptionID: "approve",
			IsGateArtifact:      true,
			OriginJobID:         ec.TaskID,
			OriginSessionID:     originSessionID,
			Options: []DecisionOption{
				{
					OptionID:      "approve",
					SortOrder:     0,
					Label:         approveLabel,
					ToolName:      toolName,
					ToolArguments: toolArguments,
				},
				{
					OptionID:  "skip",
					SortOrder: 1,
					Label:     skipLabel,
				},
			},
		},
	}

	card, err := s.CreateDecision(ctx, caller, in)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("creating gate card: %w", err)
	}
	// Link to the feed root with a hash anchor so the PWA can scroll
	// this specific card into view. The format matches itemKey on the
	// client (source_app:kind:id) so the frontend can match directly.
	cardURL := ""
	if s.baseURL != "" && ec.Tenant.Slug != "" {
		cardURL = fmt.Sprintf("%s/%s/#cards:decision:%s", s.baseURL, ec.Tenant.Slug, card.ID)
	}
	return card.ID, cardURL, nil
}

// CreateGateCardForCaller is the caller-centric entry point used by
// surfaces that don't have a full tools.ExecContext (notably MCP).
// Fetches the tenant and user models from the caller IDs, builds a
// minimal ExecContext, and delegates to CreateGateCard so the card
// shape stays identical across both paths.
func (s *CardService) CreateGateCardForCaller(
	ctx context.Context, c *services.Caller,
	toolName string, toolArguments json.RawMessage,
	preview tools.GateCardPreview,
) (uuid.UUID, string, error) {
	if c == nil {
		return uuid.Nil, "", errors.New("gate creation requires a caller")
	}
	tenant, err := models.GetTenantByID(ctx, s.pool, c.TenantID)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return uuid.Nil, "", errors.New("tenant not found")
	}
	user, err := models.GetUserByID(ctx, s.pool, c.TenantID, c.UserID)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("loading user: %w", err)
	}
	if user == nil {
		return uuid.Nil, "", errors.New("user not found")
	}
	ec := &tools.ExecContext{
		Ctx:    ctx,
		Pool:   s.pool,
		Tenant: tenant,
		User:   user,
	}
	return s.CreateGateCard(ctx, ec, toolName, toolArguments, preview)
}

// ReviseDecisionOption mutates tool_arguments and/or prompt on a single
// option of a pending decision card. Preserves tool_name (immutable
// after creation), label, sort_order, and option_id at the service
// layer — not just in the tool schema. This is the narrow path that the
// chat-revision LLM uses; broader replacement via Update.Options is
// forbidden on pending cards (see Update above).
//
// Either newToolArguments or newPrompt (or both) may be non-nil; a nil
// field means "don't change." Empty values are treated as explicit
// "clear the field."
func (s *CardService) ReviseDecisionOption(
	ctx context.Context, c *services.Caller, cardID uuid.UUID, optionID string,
	newToolArguments *json.RawMessage, newPrompt *string,
) (*DecisionOption, error) {
	// Visibility / write check. Uses the full Get path so non-admin
	// callers can only revise cards their scopes allow.
	existing, err := s.Get(ctx, c, cardID)
	if err != nil {
		return nil, err
	}
	if existing.Kind != CardKindDecision {
		return nil, fmt.Errorf("card %s is not a decision", cardID)
	}
	if !canWrite(c, existing) {
		return nil, services.ErrForbidden
	}
	// Nothing to update is a no-op that still returns the current state.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	opt, err := reviseDecisionOptionTx(ctx, tx, c.TenantID, cardID, optionID, newToolArguments, newPrompt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing revise: %w", err)
	}
	return opt, nil
}
