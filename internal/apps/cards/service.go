package cards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

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
	in.ExpiresAt = defaultBriefingExpiry(in.ExpiresAt, in.Briefing.Severity)
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

// MaxBulkCards caps how many cards one bulk update may touch. High
// enough for any real backlog cleanup, low enough that a malformed id list
// can't turn into an unbounded write loop.
const MaxBulkCards = 500

// BulkCardFailure records one card a bulk update couldn't apply, so a
// partial success tells the caller exactly what to look at rather than
// just a smaller number than they expected.
type BulkCardFailure struct {
	CardID uuid.UUID
	Err    error
}

// UpdateMany applies the same CardUpdates to several cards and returns the
// ones that succeeded alongside per-card failures.
//
// Deliberately not one transaction. Each card goes through the full Update
// path (visibility, write permission, enum validation) and those verdicts
// are per-card by nature. One card the caller can't see shouldn't roll back
// the other 91, so this reports partial success instead.
func (s *CardService) UpdateMany(ctx context.Context, c *services.Caller, cardIDs []uuid.UUID, u CardUpdates) ([]*Card, []BulkCardFailure, error) {
	if len(cardIDs) == 0 {
		return nil, nil, errors.New("no card ids supplied")
	}
	if len(cardIDs) > MaxBulkCards {
		return nil, nil, fmt.Errorf("too many cards: %d (max %d)", len(cardIDs), MaxBulkCards)
	}
	var updated []*Card
	var failures []BulkCardFailure
	seen := make(map[uuid.UUID]bool, len(cardIDs))
	for _, id := range cardIDs {
		// A repeated id would otherwise be updated twice and double-counted.
		if seen[id] {
			continue
		}
		seen[id] = true
		card, err := s.Update(ctx, c, id, u)
		if err != nil {
			failures = append(failures, BulkCardFailure{CardID: id, Err: err})
			continue
		}
		updated = append(updated, card)
	}
	return updated, failures, nil
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
