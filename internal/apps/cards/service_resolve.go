package cards

// Decision resolution. ResolveDecision commits the user's choice on a
// decision card (tool-only, tool+prompt, or prompt-only branches),
// ResolveDecisionFromAgent is the agent-path wrapper that refuses gated
// options, and runResolveTool / abortResolving handle the two-transaction
// tool-execution path with its resolving state. The small helpers at the
// bottom are only used here.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// DMOpener is the minimal Slack surface required to resolve a decision
// with a chosen option that triggers an agent job. kitslack.Client satisfies
// this interface.
type DMOpener interface {
	OpenConversation(ctx context.Context, userID string) (string, error)
}

// ResolveDecision commits the user's choice on a decision card. One of
// three branches runs depending on the option:
//
//   - Branch A (tool only): option has ToolName but no Prompt. The tool
//     executes synchronously via the injected ToolExecutor with an
//     approval token; the result is stored on the card. No follow-up
//     job.
//
//   - Branch B (tool + prompt): option has both. The tool runs first,
//     then a follow-up agent job is queued (or the origin job is
//     resumed) with the tool_result as context + the prompt.
//
//   - Branch C (prompt only): today's behavior. Queue / resume with
//     just the prompt. No tool call at the resolve layer.
//
// Before executing anything, the resolve path re-checks the option's
// policy against PolicyLookup and refuses if a PolicyGate tool lacks
// is_gate_artifact (tamper defense — see §5 of the plan).
//
// Tool execution happens OUTSIDE any DB transaction so a slow
// send_email doesn't starve the pgx pool. The card is first flipped to
// state='resolving' with a resolving_deadline and a resolve_token; the
// tool handler is passed the approval token + resolve_token via ctx for
// idempotency; on success a second tx flips to 'resolved' and writes
// the tool result; on error the card is flipped back to 'pending' with
// last_error set.
func (s *CardService) ResolveDecision(ctx context.Context, c *services.Caller, cardID uuid.UUID, optionID string, dm DMOpener) (*Card, error) {
	// Visibility check first — don't leak existence to unauthorized callers.
	card, err := s.Get(ctx, c, cardID)
	if err != nil {
		return nil, err
	}
	if card.Kind != CardKindDecision {
		return nil, fmt.Errorf("card %s is not a decision", cardID)
	}
	if !canWrite(c, card) {
		return nil, services.ErrForbidden
	}

	// Transaction 1: lock, pick the option, re-check policy, flip to
	// resolving with a deadline + resolve token. Commit immediately so
	// the slow tool call doesn't hold the row lock.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	locked, opt, err := beginResolveDecision(ctx, tx, c.TenantID, cardID, optionID)
	if err != nil {
		return nil, err
	}

	// Policy re-check: refuses if the option's current tool_name is a
	// PolicyGate tool without is_gate_artifact=true on the decision. This
	// catches post-creation tamper where tool_name got swapped from an
	// Allow tool to a Gate tool (Update refuses pending-card options
	// replacement, but a direct DB edit can still happen).
	if opt.ToolName != "" && s.policyOf(opt.ToolName) == tools.PolicyGate {
		if locked.Decision == nil || !locked.Decision.IsGateArtifact {
			return nil, fmt.Errorf("option %q invokes gated tool %q but this card is not a gate artifact — refusing", opt.OptionID, opt.ToolName)
		}
	}

	hasTool := opt.ToolName != ""
	hasPrompt := opt.Prompt != ""

	resolveToken := uuid.New()
	resolvingDeadline := time.Now().Add(5 * time.Minute)

	if hasTool {
		// Flip to resolving and commit before invoking the tool. This
		// path handles both Branch A (tool only) and Branch B (tool +
		// prompt) up through the tool execution; the prompt's follow-up
		// job is queued inside Transaction 2 after the tool succeeds.
		if err := flipCardToResolvingTx(ctx, tx, c.TenantID, cardID, c.UserID, opt.OptionID, resolveToken, resolvingDeadline); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("committing resolving tx: %w", err)
		}
		return s.runResolveTool(ctx, c, cardID, opt, resolveToken, locked, hasPrompt, dm)
	}

	return s.resolvePromptOnly(ctx, tx, c, cardID, opt, locked, hasPrompt, dm)
}

// resolvePromptOnly finishes Branch C (prompt-only) inside the caller's
// open Transaction 1, which it commits. It is the prompt-side counterpart
// of runResolveTool.
func (s *CardService) resolvePromptOnly(
	ctx context.Context, tx pgx.Tx, c *services.Caller, cardID uuid.UUID,
	opt *DecisionOption, locked *Card, hasPrompt bool, dm DMOpener,
) (*Card, error) {
	// Branch C (prompt-only) keeps today's single-tx behavior: we queue
	// the follow-up job (or resume the origin) and finish the resolve
	// in one go, no intermediate resolving state.
	var jobID *uuid.UUID
	resumed := false
	if hasPrompt && locked.Decision != nil && locked.Decision.OriginJobID != nil && locked.Decision.OriginSessionID != nil {
		originTaskID := *locked.Decision.OriginJobID
		originSessionID := *locked.Decision.OriginSessionID
		if err := models.AppendSessionEventTx(ctx, tx, c.TenantID, originSessionID, models.EventTypeDecisionResolved, map[string]any{
			"card_id":      cardID,
			"card_title":   locked.Title,
			"option_id":    opt.OptionID,
			"option_label": opt.Label,
			"resolved_by":  c.Identity,
		}); err != nil {
			return nil, fmt.Errorf("appending decision_resolved event: %w", err)
		}
		if err := models.RequeueJobForResumeTx(ctx, tx, c.TenantID, originTaskID, originSessionID); err != nil {
			return nil, fmt.Errorf("waking origin job: %w", err)
		}
		jobID = &originTaskID
		resumed = true
	}
	if hasPrompt && !resumed {
		dmChannel, err := dm.OpenConversation(ctx, c.Identity)
		if err != nil {
			return nil, fmt.Errorf("opening DM channel: %w", err)
		}
		now := time.Now()
		roleID, userID := pickTaskScope(c)
		job, err := models.CreateJobTx(
			ctx, tx,
			c.TenantID, c.UserID,
			opt.Prompt, "", "UTC", dmChannel, "", nil,
			true, &now,
			roleID, userID,
		)
		if err != nil {
			return nil, fmt.Errorf("queuing agent job: %w", err)
		}
		jobID = &job.ID
	}

	if err := finishResolveDecision(ctx, tx, c.TenantID, cardID, c.UserID, opt.OptionID, jobID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}

	if resumed && s.kicker != nil {
		s.kicker.Kick()
	}
	return s.Get(ctx, c, cardID)
}

// ResolveDecisionFromAgent is the agent-path wrapper around ResolveDecision.
// It refuses to resolve any option whose tool_name is a PolicyGate tool, so
// an agent cannot use resolve_decision to approve its own send_email (or
// similar) card and bypass the registry gate. Non-gated resolves fall
// through to ResolveDecision unchanged — the agent is still allowed to
// resolve plain prompt-only or allow-policy-tool options.
//
// Gated tool_name is write-once at creation (ReviseDecisionOption refuses
// to touch it), so a TOCTOU between this check and the eventual resolve
// is not reachable through the service layer.
func (s *CardService) ResolveDecisionFromAgent(ctx context.Context, c *services.Caller, cardID uuid.UUID, optionID string, dm DMOpener) (*Card, error) {
	card, err := s.Get(ctx, c, cardID)
	if err != nil {
		return nil, err
	}
	if card.Kind != CardKindDecision || card.Decision == nil {
		return nil, fmt.Errorf("card %s is not a decision", cardID)
	}
	pick := optionID
	if pick == "" {
		pick = card.Decision.RecommendedOptionID
	}
	if pick == "" {
		return nil, ErrNoOptionPicked
	}
	var opt *DecisionOption
	for i := range card.Decision.Options {
		if card.Decision.Options[i].OptionID == pick {
			opt = &card.Decision.Options[i]
			break
		}
	}
	if opt == nil {
		return nil, ErrOptionNotFound
	}
	if opt.ToolName != "" && s.policyOf(opt.ToolName) == tools.PolicyGate {
		return nil, ErrGatedResolveFromAgent
	}
	return s.ResolveDecision(ctx, c, cardID, optionID, dm)
}

// runResolveTool executes the gated tool for Branch A/B (after the
// first tx has flipped the card to 'resolving'). On success, writes a
// second tx that either just completes the resolve (A) or queues the
// follow-up job with the tool result (B). On tool error, aborts the
// resolving state back to 'pending' with last_error set and returns
// the error to the caller.
//
// The ToolExecutor attaches approval.WithToken(ctx, Mint(cardID,
// resolveToken)) so Registry.Execute lets the call through. Halted
// here means the registry refused the approval token (shouldn't happen
// unless the gate creator was mis-wired); surface it as an error.
func (s *CardService) runResolveTool(
	ctx context.Context, c *services.Caller, cardID uuid.UUID,
	opt *DecisionOption, resolveToken uuid.UUID,
	locked *Card, hasPrompt bool, dm DMOpener,
) (*Card, error) {
	if err := s.preflightResolveTool(ctx, c, cardID, opt, locked); err != nil {
		return nil, err
	}

	output, halted, err := s.toolExec(ctx, c, cardID, resolveToken, opt.ToolName, opt.ToolArguments)
	if err != nil {
		s.abortResolving(ctx, c.TenantID, cardID, err.Error())
		return nil, fmt.Errorf("running tool %q: %w", opt.ToolName, err)
	}
	if halted {
		s.abortResolving(ctx, c.TenantID, cardID, "gate refused approval; wiring mismatch")
		return nil, fmt.Errorf("tool %q refused approval at resolve time", opt.ToolName)
	}

	// Transaction 2: write the tool result, queue follow-up job
	// (Branch B), mark resolved.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning resolved tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	jobID, resumed, err := s.queueToolFollowupTx(ctx, tx, c, cardID, opt, locked, output, hasPrompt, dm)
	if err != nil {
		return nil, err
	}

	if err := completeResolvingCardTx(ctx, tx, c.TenantID, cardID, output, jobID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing resolved tx: %w", err)
	}
	if resumed && s.kicker != nil {
		s.kicker.Kick()
	}
	return s.Get(ctx, c, cardID)
}

// preflightResolveTool runs the checks that must pass before the tool is
// invoked. On failure it aborts the resolving state and returns the
// error for the caller to surface.
func (s *CardService) preflightResolveTool(
	ctx context.Context, c *services.Caller, cardID uuid.UUID,
	opt *DecisionOption, locked *Card,
) error {
	if s.toolExec == nil {
		// Abort the resolving state so the user can retry once wiring
		// is fixed. Return an explicit error so they aren't left
		// staring at a stuck-looking card without explanation.
		s.abortResolving(ctx, c.TenantID, cardID, "tool executor not configured")
		return fmt.Errorf("tool executor not configured; cannot run %q", opt.ToolName)
	}

	// Resume-path policy re-check. If the card's origin job has a
	// policy whose allowed_tools list no longer includes this tool
	// (edited between card creation and approval), refuse the resolve
	// with a clear error rather than silently running. Pinned args are
	// frozen at card-create — opt.ToolArguments already reflects the
	// pinned values the user approved on the card face.
	if locked.Decision != nil && locked.Decision.OriginJobID != nil {
		job, err := models.GetJob(ctx, s.pool, c.TenantID, *locked.Decision.OriginJobID)
		if err == nil && job != nil {
			if policy, perr := models.ParseConfigPolicy(job.Config); perr == nil && !policy.IsAllowed(opt.ToolName) {
				msg := fmt.Sprintf("job policy no longer permits %q", opt.ToolName)
				s.abortResolving(ctx, c.TenantID, cardID, msg)
				return errors.New(msg)
			}
		}
	}
	return nil
}

// queueToolFollowupTx is the follow-up half of Transaction 2: resume the
// origin job with the tool result, or queue a Branch B job. Returns the
// job that will carry on (nil for Branch A) and whether an origin job was
// resumed, so the caller knows to kick the scheduler after commit.
func (s *CardService) queueToolFollowupTx(
	ctx context.Context, tx pgx.Tx, c *services.Caller, cardID uuid.UUID,
	opt *DecisionOption, locked *Card, output string, hasPrompt bool, dm DMOpener,
) (*uuid.UUID, bool, error) {
	var jobID *uuid.UUID
	resumed := false
	// Resume path: origin job is waiting — append the decision_resolved
	// event (truncated tool_result so replay stays bounded) and requeue.
	if locked.Decision != nil && locked.Decision.OriginJobID != nil && locked.Decision.OriginSessionID != nil {
		originTaskID := *locked.Decision.OriginJobID
		originSessionID := *locked.Decision.OriginSessionID
		if err := models.AppendSessionEventTx(ctx, tx, c.TenantID, originSessionID, models.EventTypeDecisionResolved, map[string]any{
			"card_id":        cardID,
			"card_title":     locked.Title,
			"option_id":      opt.OptionID,
			"option_label":   opt.Label,
			"resolved_by":    c.Identity,
			"tool_name":      opt.ToolName,
			"tool_arguments": opt.ToolArguments,
			// Truncate for replay. Full value stays on the decision row
			// and is fetched via get_decision_tool_result.
			"tool_result": truncateForReplay(output, 2048),
		}); err != nil {
			return nil, false, fmt.Errorf("appending decision_resolved event: %w", err)
		}
		if err := models.RequeueJobForResumeTx(ctx, tx, c.TenantID, originTaskID, originSessionID); err != nil {
			return nil, false, fmt.Errorf("waking origin job: %w", err)
		}
		jobID = &originTaskID
		resumed = true
	}

	// Branch B (prompt set, non-resume): queue an ad-hoc follow-up job
	// whose description contains the prompt + tool result.
	if hasPrompt && !resumed {
		dmChannel, err := dm.OpenConversation(ctx, c.Identity)
		if err != nil {
			return nil, false, fmt.Errorf("opening DM channel: %w", err)
		}
		now := time.Now()
		roleID, userID := pickTaskScope(c)
		description := fmt.Sprintf(
			"%s\n\nTool `%s` returned:\n%s",
			opt.Prompt, opt.ToolName, truncateForReplay(output, 2048),
		)
		job, err := models.CreateJobTx(
			ctx, tx,
			c.TenantID, c.UserID,
			description, "", "UTC", dmChannel, "", nil,
			true, &now,
			roleID, userID,
		)
		if err != nil {
			return nil, false, fmt.Errorf("queuing follow-up job: %w", err)
		}
		jobID = &job.ID
	}
	return jobID, resumed, nil
}

// abortResolving flips a card out of the 'resolving' state back to
// 'pending' with a last_error note. Best-effort — if the abort itself
// fails we log and move on so the caller can surface the original error.
func (s *CardService) abortResolving(ctx context.Context, tenantID, cardID uuid.UUID, lastError string) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := abortResolvingCardTx(ctx, tx, tenantID, cardID, lastError); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// truncateForReplay caps a string at maxBytes and appends a suffix
// indicating truncation so downstream agents know the full value lives
// elsewhere (callable via get_decision_tool_result).
func truncateForReplay(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + fmt.Sprintf("\n… [truncated at %d bytes of %d total; call get_decision_tool_result for full output]", maxBytes, len(s))
}

// pickTaskScope chooses the scope row attached to the agent job. A
// user-scoped job matches the caller's UserID — the job is only
// visible/listable to them. Good enough for MVP; could later mirror the
// card's scope rows.
func pickTaskScope(c *services.Caller) (roleID, userID *uuid.UUID) {
	return nil, &c.UserID
}
