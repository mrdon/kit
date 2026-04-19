package cards

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// TaskKicker is the minimal scheduler surface CardService needs to nudge
// the task loop after a resume. Decoupled interface (instead of importing
// scheduler) keeps the cards package dependency-light.
type TaskKicker interface {
	Kick()
}

// CardService bundles card create/update/list + scope enforcement for both
// decision and briefing kinds. Terminal transitions (resolve, ack) live in
// this same service but have extra moving parts (agent task creation, Slack
// DM lookup) wired up by the caller.
type CardService struct {
	pool   *pgxpool.Pool
	enc    *crypto.Encryptor // set by CardsApp.RegisterMCPTools; used for DM lookup
	kicker TaskKicker        // set by CardsApp.ConfigureKicker; optional
}

// CreateDecision creates a new decision card. Non-admin callers may only
// scope the card to roles they hold.
func (s *CardService) CreateDecision(ctx context.Context, c *services.Caller, in CardCreateInput) (*Card, error) {
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
	if err := s.enforceScopeAccess(c, in.RoleScopes); err != nil {
		return nil, err
	}
	return createCardTx(ctx, s.pool, c.TenantID, in)
}

// CreateBriefing creates a new briefing card.
func (s *CardService) CreateBriefing(ctx context.Context, c *services.Caller, in CardCreateInput) (*Card, error) {
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
	if err := s.enforceScopeAccess(c, in.RoleScopes); err != nil {
		return nil, err
	}
	return createCardTx(ctx, s.pool, c.TenantID, in)
}

// Update applies a CardUpdates. Caller must have write access on the card.
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
	if u.RoleScopes != nil {
		if err := s.enforceScopeAccess(c, *u.RoleScopes); err != nil {
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
	return listStack(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs, c.IsAdmin)
}

// DMOpener is the minimal Slack surface required to resolve a decision
// with a chosen option that triggers an agent task. kitslack.Client satisfies
// this interface.
type DMOpener interface {
	OpenConversation(ctx context.Context, userID string) (string, error)
}

// ResolveDecision commits the user's choice on a decision card. If the
// chosen option carries a prompt, a one-shot agent task is created in the
// same transaction that flips the card state. The task posts to the
// caller's Slack DM, opened via dm.
//
// If optionID is empty, the card's recommended_option_id is used. Returns
// ErrAlreadyTerminal if the card is not pending, ErrOptionNotFound if the
// option doesn't exist, or ErrNoOptionPicked if no option was supplied and
// there's no recommendation.
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	locked, opt, err := beginResolveDecision(ctx, tx, c.TenantID, cardID, optionID)
	if err != nil {
		return nil, err
	}

	var taskID *uuid.UUID

	// Resume path: the decision was created by a task's agent. Append a
	// decision_resolved event to that task's session and requeue the task
	// with the session marked for resume — the original workflow picks
	// up the next time the scheduler claims it.
	resumed := false
	if locked.Decision != nil && locked.Decision.OriginTaskID != nil && locked.Decision.OriginSessionID != nil {
		originTaskID := *locked.Decision.OriginTaskID
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
		if err := models.RequeueTaskForResumeTx(ctx, tx, c.TenantID, originTaskID, originSessionID); err != nil {
			return nil, fmt.Errorf("waking origin task: %w", err)
		}
		taskID = &originTaskID
		resumed = true
	}

	// Fallback / ad-hoc path: the option carries its own prompt and the
	// decision isn't part of a resumable workflow — queue a fresh one-shot
	// agent task.
	if !resumed && opt.Prompt != "" {
		dmChannel, err := dm.OpenConversation(ctx, c.Identity)
		if err != nil {
			return nil, fmt.Errorf("opening DM channel: %w", err)
		}
		now := time.Now()
		roleID, userID := pickTaskScope(c)
		task, err := models.CreateTaskTx(
			ctx, tx,
			c.TenantID, c.UserID,
			opt.Prompt, "", "UTC", dmChannel,
			true, &now,
			roleID, userID,
		)
		if err != nil {
			return nil, fmt.Errorf("queuing agent task: %w", err)
		}
		taskID = &task.ID
	}

	if err := finishResolveDecision(ctx, tx, c.TenantID, cardID, c.UserID, opt.OptionID, taskID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}

	// Post-commit: wake the scheduler immediately so a resumed workflow
	// doesn't wait up to 60s for the next poll tick. Kick is non-blocking.
	if resumed && s.kicker != nil {
		s.kicker.Kick()
	}
	return s.Get(ctx, c, cardID)
}

// pickTaskScope chooses the scope row attached to the agent task. A
// user-scoped task matches the caller's UserID — the task is only
// visible/listable to them. Good enough for MVP; could later mirror the
// card's scope rows.
func pickTaskScope(c *services.Caller) (roleID, userID *uuid.UUID) {
	return nil, &c.UserID
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

// enforceScopeAccess prevents non-admins from scoping a card to a role they
// don't hold. Admins are trusted.
func (s *CardService) enforceScopeAccess(c *services.Caller, roleScopes []string) error {
	if c.IsAdmin {
		return nil
	}
	for _, role := range roleScopes {
		if !hasRole(c.Roles, role) {
			return services.ErrForbidden
		}
	}
	return nil
}

func hasRole(roles []string, target string) bool {
	return slices.Contains(roles, target)
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
