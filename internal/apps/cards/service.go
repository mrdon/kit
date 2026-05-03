package cards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// TaskKicker is the minimal scheduler surface CardService needs to nudge
// the job loop after a resume. Decoupled interface (instead of importing
// scheduler) keeps the cards package dependency-light.
type TaskKicker interface {
	Kick()
}

// PolicyLookup returns a tool's DefaultPolicy, used at create-time to
// stamp is_gate_artifact on decision cards whose options invoke
// PolicyGate tools, and at resolve-time to re-check the gate before
// running the tool. Wired at startup once the static tool registry is
// known. A nil lookup means all tools are treated as PolicyAllow
// (acceptable for tests / startup ordering slips; the registry-level
// gate still catches runtime calls).
type PolicyLookup func(toolName string) tools.Policy

// ToolExecutor runs a registered tool with an approval token bound to
// ctx. Called by ResolveDecision after the user approves a card. The
// implementation (in main.go) builds a per-caller tools.Registry,
// populates an ExecContext, attaches approval.WithToken(ctx, mint(...)),
// and dispatches via Registry.ExecuteWithResult. Returns the tool
// handler's string output, whether the call halted (true only if the
// gate fired despite the approval — indicates tamper), and any error.
type ToolExecutor func(
	ctx context.Context, caller *services.Caller,
	cardID, resolveToken uuid.UUID,
	toolName string, toolArguments json.RawMessage,
) (output string, halted bool, err error)

// CardService bundles card create/update/list + scope enforcement for both
// decision and briefing kinds. Terminal transitions (resolve, ack) live in
// this same service but have extra moving parts (agent job creation, Slack
// DM lookup) wired up by the caller.
type CardService struct {
	pool         *pgxpool.Pool
	enc          *crypto.Encryptor // set by CardsApp.RegisterMCPTools; used for DM lookup
	kicker       TaskKicker        // set by CardsApp.ConfigureKicker; optional
	policyLookup PolicyLookup      // set by CardsApp.ConfigurePolicyLookup; nil = all-allow
	toolExec     ToolExecutor      // set by CardsApp.ConfigureToolExecutor; required for resolve-with-tool
	baseURL      string            // set by CardsApp.Configure; used to build HALTED card URLs
	pushNotifier PushNotifier      // set by CardsApp.ConfigurePushNotifier; optional out-of-band alerting
}

// PushNotifier is the small slice of Messenger CardService uses to send
// an immediate Slack DM when a card is created with Urgent=true. Apps
// opt in by setting Urgent on their CardCreateInput. Defined as an
// interface so the cards package doesn't import Messenger directly.
type PushNotifier interface {
	// PushUrgent sends a one-line out-of-band notification to user
	// userID about the card with the given title + URL. Implementations
	// MUST be best-effort: errors are logged and swallowed so a
	// transient Slack outage doesn't block card creation.
	PushUrgent(ctx context.Context, tenantID, userID uuid.UUID, title, body, cardURL string) error
}

// ConfigurePushNotifier wires the urgent-card push surface. Idempotent.
func (s *CardService) ConfigurePushNotifier(p PushNotifier) {
	s.pushNotifier = p
}

// NewService returns a CardService bound to pool. Exported so Phase 3
// builder bridges (and other external wiring) can construct a service
// without going through the app init path.
func NewService(pool *pgxpool.Pool) *CardService {
	return &CardService{pool: pool}
}

// ConfigurePolicyLookup wires the policy lookup used by CreateDecision
// (to stamp is_gate_artifact) and ResolveDecision (to re-check the
// gate). Safe to call multiple times; idempotent.
func (s *CardService) ConfigurePolicyLookup(lookup PolicyLookup) {
	s.policyLookup = lookup
}

// ConfigureToolExecutor wires the per-caller tool executor used by
// ResolveDecision to invoke gated tools after approval.
func (s *CardService) ConfigureToolExecutor(exec ToolExecutor) {
	s.toolExec = exec
}

// policyOf returns the policy for toolName, defaulting to PolicyAllow
// when no lookup is configured (avoids crashing tests and keeps startup
// forgiving).
func (s *CardService) policyOf(toolName string) tools.Policy {
	if s.policyLookup == nil || toolName == "" {
		return tools.PolicyAllow
	}
	return s.policyLookup(toolName)
}

// CreateDecision creates a new decision card. Non-admin callers may only
// scope the card to roles they hold. If any option's ToolName is a
// PolicyGate tool, the card is automatically stamped with
// is_gate_artifact=true so ResolveDecision will let the gate pass at
// approval time (see §4a and §5 of the plan).
func (s *CardService) CreateDecision(ctx context.Context, c *services.Caller, in CardCreateInput) (*Card, error) {
	if err := s.enforceScopeAccess(c, in.RoleScopes, in.UserScopes); err != nil {
		return nil, err
	}
	return s.createDecisionImpl(ctx, c.TenantID, in)
}

// CreateSystemDecision creates a decision card as Kit itself, with no
// user-attributable actor. Used by trusted internal code paths that
// generate cards on behalf of the system — e.g. the vault's
// grant-request card targeting the admin role on behalf of a registering
// non-admin user. Bypasses enforceScopeAccess (since there is no caller
// whose scope membership to enforce against) but otherwise behaves
// identically to CreateDecision. NEVER expose this to user-facing
// surfaces (agent, MCP, web).
func (s *CardService) CreateSystemDecision(ctx context.Context, tenantID uuid.UUID, in CardCreateInput) (*Card, error) {
	return s.createDecisionImpl(ctx, tenantID, in)
}

// createDecisionImpl is the shared body of CreateDecision and
// CreateSystemDecision: validation + gate-stamping + insert + urgent
// push. Caller-scope enforcement (if any) is the caller's
// responsibility.
func (s *CardService) createDecisionImpl(ctx context.Context, tenantID uuid.UUID, in CardCreateInput) (*Card, error) {
	if in.Kind != "" && in.Kind != CardKindDecision {
		return nil, fmt.Errorf("CreateDecision: kind mismatch (%s)", in.Kind)
	}
	in.Kind = CardKindDecision
	if in.Decision == nil {
		return nil, errors.New("decision fields required")
	}
	if in.Decision.Priority == "" {
		in.Decision.Priority = DecisionPriorityMedium
	}
	if !in.Decision.Priority.Valid() {
		return nil, fmt.Errorf("invalid priority %q", in.Decision.Priority)
	}
	if len(in.Decision.Options) == 0 {
		return nil, errors.New("a decision needs at least one option")
	}
	if err := validateOptions(in.Decision.Options, in.Decision.RecommendedOptionID); err != nil {
		return nil, err
	}

	// Stamp is_gate_artifact for any option that invokes a PolicyGate
	// tool. ResolveDecision re-checks this before running the tool; a
	// mismatched flag means tamper. Callers can also explicitly set
	// IsGateArtifact (e.g. the registry's auto-gate path) and we OR.
	for _, opt := range in.Decision.Options {
		if opt.ToolName == "" {
			continue
		}
		if s.policyOf(opt.ToolName) == tools.PolicyGate {
			in.Decision.IsGateArtifact = true
			break
		}
	}

	card, err := createCardTx(ctx, s.pool, tenantID, in)
	if err != nil {
		return nil, err
	}
	s.maybePushUrgent(ctx, card, in)
	return card, nil
}

// CreateBriefing creates a new briefing card.
func (s *CardService) CreateBriefing(ctx context.Context, c *services.Caller, in CardCreateInput) (*Card, error) {
	if err := s.enforceScopeAccess(c, in.RoleScopes, in.UserScopes); err != nil {
		return nil, err
	}
	return s.createBriefingImpl(ctx, c.TenantID, in)
}

// CreateSystemBriefing is the system-caller counterpart to
// CreateBriefing — see CreateSystemDecision for the caveats.
func (s *CardService) CreateSystemBriefing(ctx context.Context, tenantID uuid.UUID, in CardCreateInput) (*Card, error) {
	return s.createBriefingImpl(ctx, tenantID, in)
}

func (s *CardService) createBriefingImpl(ctx context.Context, tenantID uuid.UUID, in CardCreateInput) (*Card, error) {
	if in.Kind != "" && in.Kind != CardKindBriefing {
		return nil, fmt.Errorf("CreateBriefing: kind mismatch (%s)", in.Kind)
	}
	in.Kind = CardKindBriefing
	if in.Briefing == nil {
		in.Briefing = &BriefingCreateInput{Severity: BriefingSeverityInfo}
	}
	if in.Briefing.Severity == "" {
		in.Briefing.Severity = BriefingSeverityInfo
	}
	if !in.Briefing.Severity.Valid() {
		return nil, fmt.Errorf("invalid severity %q", in.Briefing.Severity)
	}
	card, err := createCardTx(ctx, s.pool, tenantID, in)
	if err != nil {
		return nil, err
	}
	s.maybePushUrgent(ctx, card, in)
	return card, nil
}

// maybePushUrgent fires an out-of-band push (Slack DM) for cards created
// with Urgent=true and at least one user scope. Best-effort: a missing
// notifier, slug lookup failure, or per-user push error all just log and
// move on so card creation never fails because of the push side-channel.
func (s *CardService) maybePushUrgent(ctx context.Context, card *Card, in CardCreateInput) {
	if !in.Urgent || s.pushNotifier == nil || card == nil {
		return
	}
	if len(in.UserScopes) == 0 {
		// Urgent only makes sense for explicitly-targeted cards. A
		// tenant- or role-scoped urgent would DM half the workspace.
		return
	}
	tenant, err := models.GetTenantByID(ctx, s.pool, card.TenantID)
	if err != nil || tenant == nil {
		slog.WarnContext(ctx, "urgent push skipped: tenant lookup failed",
			"card_id", card.ID, "err", err)
		return
	}
	cardURL := ""
	if s.baseURL != "" && tenant.Slug != "" {
		cardURL = fmt.Sprintf("%s/%s/#cards:%s:%s", s.baseURL, tenant.Slug, card.Kind, card.ID)
	}
	for _, uid := range in.UserScopes {
		if err := s.pushNotifier.PushUrgent(ctx, card.TenantID, uid, in.Title, in.Body, cardURL); err != nil {
			slog.WarnContext(ctx, "urgent push failed",
				"card_id", card.ID, "user_id", uid, "err", err)
		}
	}
}

// Update applies a CardUpdates. Caller must have write access on the card.
//
// On pending decision cards, `options` replacement is refused — all
// option edits must flow through ReviseDecisionOption (narrow, only
// mutates tool_arguments and prompt, can't swap tool_name). This closes
// a class of tamper routes where an LLM or admin could swap a
// PolicyAllow option's tool_name to PolicyGate or add new gated
// options post-creation. See §4 of the plan.
func (s *CardService) Update(ctx context.Context, c *services.Caller, cardID uuid.UUID, u CardUpdates) (*Card, error) {
	existing, err := s.Get(ctx, c, cardID)
	if err != nil {
		return nil, err
	}
	if !canWrite(c, existing) {
		return nil, services.ErrForbidden
	}
	// Validate enum fields on updates.
	if u.State != nil && !u.State.Valid() {
		return nil, fmt.Errorf("invalid state %q", *u.State)
	}
	if u.Decision != nil {
		if u.Decision.Priority != nil && !u.Decision.Priority.Valid() {
			return nil, fmt.Errorf("invalid priority %q", *u.Decision.Priority)
		}
		if u.Decision.Options != nil {
			// Refuse options replacement on pending cards entirely.
			// Narrow revisions use ReviseDecisionOption instead.
			if existing.Kind == CardKindDecision && existing.State == CardStatePending {
				return nil, errors.New("cannot replace options on a pending decision card; use revise_decision_option for per-option edits")
			}
			rec := ""
			if u.Decision.RecommendedOptionID != nil {
				rec = *u.Decision.RecommendedOptionID
			} else if existing.Decision != nil {
				rec = existing.Decision.RecommendedOptionID
			}
			if err := validateOptions(*u.Decision.Options, rec); err != nil {
				return nil, err
			}
		}
	}
	if u.Briefing != nil && u.Briefing.Severity != nil && !u.Briefing.Severity.Valid() {
		return nil, fmt.Errorf("invalid severity %q", *u.Briefing.Severity)
	}
	if u.RoleScopes != nil || u.UserScopes != nil {
		var roles []string
		var users []uuid.UUID
		if u.RoleScopes != nil {
			roles = *u.RoleScopes
		}
		if u.UserScopes != nil {
			users = *u.UserScopes
		}
		if err := s.enforceScopeAccess(c, roles, users); err != nil {
			return nil, err
		}
	}
	card, err := updateCardTx(ctx, s.pool, c.TenantID, cardID, u)
	if err != nil {
		return nil, err
	}
	if card == nil {
		return nil, services.ErrNotFound
	}
	return card, nil
}

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

// Get loads a single card. Returns ErrNotFound if missing or not visible
// to the caller.
func (s *CardService) Get(ctx context.Context, c *services.Caller, cardID uuid.UUID) (*Card, error) {
	card, err := getCard(ctx, s.pool, c.TenantID, cardID)
	if err != nil {
		return nil, err
	}
	if card == nil {
		return nil, services.ErrNotFound
	}
	if !c.IsAdmin {
		ok, err := s.callerCanSee(ctx, c, cardID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, services.ErrNotFound
		}
	}
	return card, nil
}

// ListDecisions returns decision cards matching the filter, visible to the caller.
func (s *CardService) ListDecisions(ctx context.Context, c *services.Caller, f CardFilters) ([]*Card, error) {
	if f.State != "" && !f.State.Valid() {
		return nil, fmt.Errorf("invalid state %q", f.State)
	}
	if f.Priority != "" && !f.Priority.Valid() {
		return nil, fmt.Errorf("invalid priority %q", f.Priority)
	}
	return listCards(ctx, s.pool, c.TenantID, c.UserID, CardKindDecision, f, c.RoleIDs, c.IsAdmin)
}

// ListBriefings returns briefing cards matching the filter, visible to the caller.
func (s *CardService) ListBriefings(ctx context.Context, c *services.Caller, f CardFilters) ([]*Card, error) {
	if f.State != "" && !f.State.Valid() {
		return nil, fmt.Errorf("invalid state %q", f.State)
	}
	if f.Severity != "" && !f.Severity.Valid() {
		return nil, fmt.Errorf("invalid severity %q", f.Severity)
	}
	return listCards(ctx, s.pool, c.TenantID, c.UserID, CardKindBriefing, f, c.RoleIDs, c.IsAdmin)
}

// Stack returns pending cards visible to the caller, in the PRD's
// interleaved priority-vs-severity order. Briefings the caller has
// already acked are filtered out so role-scoped briefings stay visible
// to teammates who haven't yet seen them.
func (s *CardService) Stack(ctx context.Context, c *services.Caller) ([]*Card, error) {
	return listStack(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
}

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
	if s.toolExec == nil {
		// Abort the resolving state so the user can retry once wiring
		// is fixed. Return an explicit error so they aren't left
		// staring at a stuck-looking card without explanation.
		s.abortResolving(ctx, c.TenantID, cardID, "tool executor not configured")
		return nil, fmt.Errorf("tool executor not configured; cannot run %q", opt.ToolName)
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
				return nil, errors.New(msg)
			}
		}
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
			return nil, fmt.Errorf("appending decision_resolved event: %w", err)
		}
		if err := models.RequeueJobForResumeTx(ctx, tx, c.TenantID, originTaskID, originSessionID); err != nil {
			return nil, fmt.Errorf("waking origin job: %w", err)
		}
		jobID = &originTaskID
		resumed = true
	}

	// Branch B (prompt set, non-resume): queue an ad-hoc follow-up job
	// whose description contains the prompt + tool result.
	if hasPrompt && !resumed {
		dmChannel, err := dm.OpenConversation(ctx, c.Identity)
		if err != nil {
			return nil, fmt.Errorf("opening DM channel: %w", err)
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
			return nil, fmt.Errorf("queuing follow-up job: %w", err)
		}
		jobID = &job.ID
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

// cardSnoozeHourLocal is the clock hour (in tenant timezone) a snoozed
// card reappears at. 7am puts the card back on the user's morning feed —
// cards are deliberative, so unlike tasks (3am) we want them to land
// during the workday, not pile up overnight.
const cardSnoozeHourLocal = 7

// SnoozeForUser hides a card from this user's swipe feed (and any future
// notification surface — Slack DMs, email digests) until `until`. Per-user
// state, not per-card: another user sharing the role-scope still sees it.
// The card itself stays pending and reachable via list_decisions / direct
// link / MCP; this only filters it out of the personal feeds.
func (s *CardService) SnoozeForUser(ctx context.Context, c *services.Caller, cardID uuid.UUID, until time.Time) error {
	card, err := s.Get(ctx, c, cardID)
	if err != nil {
		return err
	}
	if !canWrite(c, card) {
		return services.ErrForbidden
	}
	return upsertUserCardSnooze(ctx, s.pool, c.TenantID, cardID, c.UserID, until)
}

// SnoozeForUserOneMonth is the convenience the swipe-UI's "Sleep 1 month"
// action calls: 30 calendar days from now at cardSnoozeHourLocal local in
// the tenant timezone. Email-link and Slack-button handlers (when those
// ship) can use the same helper to compute the timestamp consistently.
func (s *CardService) SnoozeForUserOneMonth(ctx context.Context, c *services.Caller, cardID uuid.UUID) error {
	tz, err := s.tenantTimezone(ctx, c.TenantID)
	if err != nil {
		return err
	}
	until, err := cardSnoozeOneMonthAt(time.Now(), tz)
	if err != nil {
		return err
	}
	return s.SnoozeForUser(ctx, c, cardID, until)
}

func (s *CardService) tenantTimezone(ctx context.Context, tenantID uuid.UUID) (string, error) {
	tenant, err := models.GetTenantByID(ctx, s.pool, tenantID)
	if err != nil {
		return "", fmt.Errorf("looking up tenant: %w", err)
	}
	if tenant == nil || tenant.Timezone == "" {
		return "UTC", nil
	}
	return tenant.Timezone, nil
}

// cardSnoozeOneMonthAt returns the snoozed_until timestamp for the "Sleep
// 1 month" action: 30 calendar days from `now`, clock set to
// cardSnoozeHourLocal (07:00) in tz, converted to UTC. Pure function so
// tests can pin wall time.
func cardSnoozeOneMonthAt(now time.Time, tz string) (time.Time, error) {
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("loading timezone %q: %w", tz, err)
	}
	local := now.In(loc)
	advanced := time.Date(local.Year(), local.Month(), local.Day()+30,
		cardSnoozeHourLocal, 0, 0, 0, loc)
	return advanced.UTC(), nil
}

// AckBriefing transitions a briefing card to a terminal state. Caller must
// have write access.
func (s *CardService) AckBriefing(ctx context.Context, c *services.Caller, cardID uuid.UUID, kind BriefingAckKind) (*Card, error) {
	if !kind.Valid() {
		return nil, fmt.Errorf("invalid ack kind %q", kind)
	}
	card, err := s.Get(ctx, c, cardID)
	if err != nil {
		return nil, err
	}
	if card.Kind != CardKindBriefing {
		return nil, fmt.Errorf("card %s is not a briefing", cardID)
	}
	if !canWrite(c, card) {
		return nil, services.ErrForbidden
	}
	out, err := ackBriefing(ctx, s.pool, c.TenantID, cardID, c.UserID, kind)
	if err != nil {
		if errors.Is(err, ErrAlreadyTerminal) {
			return nil, ErrAlreadyTerminal
		}
		return nil, err
	}
	return out, nil
}

// callerCanSee returns true if the caller's scopes include the card. Used
// for single-row Get so we don't leak existence.
func (s *CardService) callerCanSee(ctx context.Context, c *services.Caller, cardID uuid.UUID) (bool, error) {
	scopeSQL, scopeArgs := c.ScopeFilterIDs("sc", 3)
	args := []any{c.TenantID, cardID}
	args = append(args, scopeArgs...)
	query := `SELECT 1 FROM app_card_scopes acs JOIN scopes sc ON sc.id = acs.scope_id WHERE acs.tenant_id = $1 AND acs.card_id = $2 AND (` + scopeSQL + `) LIMIT 1`
	var one int
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&one); err != nil {
		return false, nil //nolint:nilerr // no-rows means no access
	}
	return true, nil
}

// enforceScopeAccess prevents non-admins from scoping a card to a role
// they don't hold or to a user other than themselves. Admins are trusted.
func (s *CardService) enforceScopeAccess(c *services.Caller, roleScopes []string, userScopes []uuid.UUID) error {
	if c.IsAdmin {
		return nil
	}
	for _, role := range roleScopes {
		if !slices.Contains(c.Roles, role) {
			return services.ErrForbidden
		}
	}
	for _, uid := range userScopes {
		if uid != c.UserID {
			return services.ErrForbidden
		}
	}
	return nil
}

// canWrite is the write-access check for update / resolve / ack. Admins and
// non-admins pass as long as the card is visible — more granular write-vs-
// read split is an open question per the plan.
func canWrite(c *services.Caller, card *Card) bool {
	if c.IsAdmin {
		return true
	}
	// If the caller could see the card via Get, they can write. Read/write
	// parity is acceptable for the MVP slice.
	_ = card
	return true
}

// validateOptions checks option ids are unique and the recommended id (if
// set) matches one of the options.
func validateOptions(opts []DecisionOption, recommended string) error {
	seen := map[string]struct{}{}
	for _, o := range opts {
		if o.OptionID == "" {
			return errors.New("option.option_id is required")
		}
		if o.Label == "" {
			return fmt.Errorf("option %q: label is required", o.OptionID)
		}
		if _, dup := seen[o.OptionID]; dup {
			return fmt.Errorf("duplicate option_id %q", o.OptionID)
		}
		seen[o.OptionID] = struct{}{}
	}
	if recommended != "" {
		if _, ok := seen[recommended]; !ok {
			return fmt.Errorf("recommended_option_id %q is not in options", recommended)
		}
	}
	return nil
}
