package cards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// updateCardTx applies a CardUpdates in one transaction, touching the
// parent row, the kind-specific child, options (if replaced), and scope
// rows (if replaced). Returns the refreshed card.
func updateCardTx(ctx context.Context, pool *pgxpool.Pool, tenantID, cardID uuid.UUID, u CardUpdates) (*Card, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the row for update, and verify kind for decision/briefing updates.
	var kind CardKind
	if err := tx.QueryRow(ctx, `SELECT kind FROM app_cards WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, cardID).Scan(&kind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil
		}
		return nil, fmt.Errorf("locking card: %w", err)
	}

	// Parent updates.
	sets, args := buildCardSets(u)
	if len(sets) > 0 {
		sets = append(sets, "updated_at = now()")
		query := fmt.Sprintf(`UPDATE app_cards SET %s WHERE tenant_id = $1 AND id = $2`, strings.Join(sets, ", "))
		args = append([]any{tenantID, cardID}, args...)
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return nil, fmt.Errorf("updating card: %w", err)
		}
	}

	// Decision child updates.
	if u.Decision != nil {
		if kind != CardKindDecision {
			return nil, fmt.Errorf("cannot apply decision updates to %s card", kind)
		}
		if err := updateDecisionTx(ctx, tx, tenantID, cardID, *u.Decision); err != nil {
			return nil, err
		}
	}

	// Briefing child updates.
	if u.Briefing != nil {
		if kind != CardKindBriefing {
			return nil, fmt.Errorf("cannot apply briefing updates to %s card", kind)
		}
		if u.Briefing.Severity != nil {
			if _, err := tx.Exec(ctx, `UPDATE app_card_briefings SET severity = $1 WHERE tenant_id = $2 AND card_id = $3`, *u.Briefing.Severity, tenantID, cardID); err != nil {
				return nil, fmt.Errorf("updating briefing severity: %w", err)
			}
		}
	}

	// Scope replacement. CardUpdates only exposes RoleScopes today;
	// extending to UserScopes would mirror the create-side change here.
	if u.RoleScopes != nil {
		if err := writeScopesTx(ctx, tx, tenantID, cardID, *u.RoleScopes, nil); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	return getCard(ctx, pool, tenantID, cardID)
}

func buildCardSets(u CardUpdates) ([]string, []any) {
	var sets []string
	var args []any
	argN := 2 // $1=tenant_id, $2=card_id taken by WHERE clause
	if u.Title != nil {
		argN++
		sets = append(sets, fmt.Sprintf("title = $%d", argN))
		args = append(args, *u.Title)
	}
	if u.Body != nil {
		argN++
		sets = append(sets, fmt.Sprintf("body = $%d", argN))
		args = append(args, *u.Body)
	}
	if u.State != nil {
		argN++
		sets = append(sets, fmt.Sprintf("state = $%d", argN))
		args = append(args, *u.State)
		// When moving out of pending, stamp terminal_at. When moving back to
		// pending, clear it.
		if *u.State == CardStatePending {
			sets = append(sets, "terminal_at = NULL", "terminal_by = NULL")
		} else {
			sets = append(sets, "terminal_at = now()")
		}
	}
	return sets, args
}

func updateDecisionTx(ctx context.Context, tx pgx.Tx, tenantID, cardID uuid.UUID, u DecisionUpdates) error {
	var sets []string
	var args []any
	argN := 2 // $1=tenant_id, $2=card_id in WHERE
	if u.Priority != nil {
		argN++
		sets = append(sets, fmt.Sprintf("priority = $%d", argN))
		args = append(args, *u.Priority)
	}
	if u.RecommendedOptionID != nil {
		argN++
		sets = append(sets, fmt.Sprintf("recommended_option_id = $%d", argN))
		args = append(args, nilIfEmpty(*u.RecommendedOptionID))
	}
	if len(sets) > 0 {
		query := fmt.Sprintf(`UPDATE app_card_decisions SET %s WHERE tenant_id = $1 AND card_id = $2`, strings.Join(sets, ", "))
		args = append([]any{tenantID, cardID}, args...)
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("updating decision: %w", err)
		}
	}

	// Full replacement of options when caller supplied them. Note: the
	// service layer (CardService.Update) refuses this path on pending
	// cards — it's reachable only for non-pending states (e.g. admin
	// rewrites on resolved cards) or from tests.
	if u.Options != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM app_card_decision_options WHERE tenant_id = $1 AND card_id = $2`, tenantID, cardID); err != nil {
			return fmt.Errorf("clearing options: %w", err)
		}
		for i, opt := range *u.Options {
			if _, err := tx.Exec(ctx, `
				INSERT INTO app_card_decision_options (tenant_id, card_id, option_id, sort_order, label, prompt, tool_name, tool_arguments)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				tenantID, cardID, opt.OptionID, i, opt.Label, nilIfEmpty(opt.Prompt), nilIfEmpty(opt.ToolName), nilIfEmptyBytes(opt.ToolArguments),
			); err != nil {
				return fmt.Errorf("inserting option %q: %w", opt.OptionID, err)
			}
		}
	}
	return nil
}

// reviseDecisionOptionTx updates tool_arguments and/or prompt on a single
// option of a pending card. Preserves tool_name, label, sort_order,
// option_id. Takes FOR UPDATE on the parent card and refuses if not
// pending. The caller is responsible for committing. Returns the refreshed
// option, or ErrAlreadyTerminal / ErrOptionNotFound.
//
// Only tool_arguments and prompt can change; tool_name is write-once at
// creation. This is the narrow-revise path that the chat-revision LLM
// calls; the broader Update path is forbidden on pending cards.
func reviseDecisionOptionTx(
	ctx context.Context, tx pgx.Tx,
	tenantID, cardID uuid.UUID, optionID string,
	newToolArguments *json.RawMessage, newPrompt *string,
) (*DecisionOption, error) {
	var state CardState
	if err := tx.QueryRow(ctx,
		`SELECT state FROM app_cards WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
		tenantID, cardID,
	).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCardNotFound
		}
		return nil, fmt.Errorf("locking card: %w", err)
	}
	if state != CardStatePending {
		return nil, ErrAlreadyTerminal
	}

	// Ensure the option exists before we do anything else. Surfaces a
	// clean ErrOptionNotFound instead of a zero-rows-affected UPDATE.
	existing, err := getDecisionOption(ctx, tx, tenantID, cardID, optionID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrOptionNotFound
	}

	var sets []string
	var args []any
	argN := 3 // $1=tenant_id, $2=card_id, $3=option_id in WHERE
	if newToolArguments != nil {
		argN++
		sets = append(sets, fmt.Sprintf("tool_arguments = $%d", argN))
		args = append(args, nilIfEmptyBytes(*newToolArguments))
	}
	if newPrompt != nil {
		argN++
		sets = append(sets, fmt.Sprintf("prompt = $%d", argN))
		args = append(args, nilIfEmpty(*newPrompt))
	}
	if len(sets) > 0 {
		query := fmt.Sprintf(
			`UPDATE app_card_decision_options SET %s WHERE tenant_id = $1 AND card_id = $2 AND option_id = $3`,
			strings.Join(sets, ", "),
		)
		args = append([]any{tenantID, cardID, optionID}, args...)
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return nil, fmt.Errorf("updating option: %w", err)
		}
	}

	// Bump parent updated_at so stack re-renders pick up the change.
	if _, err := tx.Exec(ctx,
		`UPDATE app_cards SET updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenantID, cardID,
	); err != nil {
		return nil, fmt.Errorf("bumping card updated_at: %w", err)
	}

	return getDecisionOption(ctx, tx, tenantID, cardID, optionID)
}

// flipCardToResolvingTx marks the chosen option, sets state='resolving',
// stamps resolving_deadline and resolve_token. Called inside the resolve
// tx before the tool call runs; the tx commits immediately afterward so
// the slow tool call doesn't hold the row lock.
func flipCardToResolvingTx(
	ctx context.Context, tx pgx.Tx,
	tenantID, cardID, resolvedBy uuid.UUID,
	optionID string, resolveToken uuid.UUID, resolvingDeadline time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE app_card_decisions
		SET resolved_option_id = $1,
		    resolving_deadline = $2,
		    resolve_token      = $3,
		    last_error         = NULL
		WHERE tenant_id = $4 AND card_id = $5`,
		optionID, resolvingDeadline, resolveToken, tenantID, cardID,
	); err != nil {
		return fmt.Errorf("stamping decision resolve fields: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_cards
		SET state = $1, terminal_by = $2, updated_at = now()
		WHERE tenant_id = $3 AND id = $4`,
		CardStateResolving, resolvedBy, tenantID, cardID,
	); err != nil {
		return fmt.Errorf("flipping card to resolving: %w", err)
	}
	return nil
}

// completeResolvingCardTx flips a card from 'resolving' to 'resolved',
// records the tool result and optional follow-up task. Called in a fresh
// tx after the tool handler returns.
func completeResolvingCardTx(
	ctx context.Context, tx pgx.Tx,
	tenantID, cardID uuid.UUID,
	toolResult string, taskID *uuid.UUID,
) error {
	now := time.Now()
	if _, err := tx.Exec(ctx, `
		UPDATE app_card_decisions
		SET resolved_tool_result = $1,
		    resolved_at          = $2,
		    resolved_task_id     = $3,
		    resolving_deadline   = NULL
		WHERE tenant_id = $4 AND card_id = $5`,
		nilIfEmpty(toolResult), now, taskID, tenantID, cardID,
	); err != nil {
		return fmt.Errorf("writing resolved fields: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_cards
		SET state = $1, terminal_at = now(), updated_at = now()
		WHERE tenant_id = $2 AND id = $3`,
		CardStateResolved, tenantID, cardID,
	); err != nil {
		return fmt.Errorf("flipping card to resolved: %w", err)
	}
	return nil
}

// abortResolvingCardTx flips a wedged 'resolving' card back to 'pending'
// with a last_error note, clearing resolving_deadline + resolve_token.
// Used both by the tool-error path (ResolveDecision rolls back after a
// handler failure) and the scheduler sweep (past-deadline recovery).
// The resolve_token stays on the card just long enough for a re-approve
// to hit the handler's dedupe table; we clear it here so the next
// approve mints a fresh one.
func abortResolvingCardTx(
	ctx context.Context, tx pgx.Tx,
	tenantID, cardID uuid.UUID,
	lastError string,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE app_card_decisions
		SET resolved_option_id = NULL,
		    resolving_deadline = NULL,
		    resolve_token      = NULL,
		    last_error         = $1
		WHERE tenant_id = $2 AND card_id = $3`,
		nilIfEmpty(lastError), tenantID, cardID,
	); err != nil {
		return fmt.Errorf("clearing decision resolve fields: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_cards
		SET state = $1, terminal_at = NULL, terminal_by = NULL, updated_at = now()
		WHERE tenant_id = $2 AND id = $3`,
		CardStatePending, tenantID, cardID,
	); err != nil {
		return fmt.Errorf("flipping card back to pending: %w", err)
	}
	return nil
}

// sweepStuckResolvingCards flips cards in 'resolving' past their deadline
// back to 'pending' with a timeout error. Returns the number of cards
// recovered.
//
// Runs in one transaction with two statements. A single CTE was
// tempting but Postgres' visibility rules between data-modifying
// CTEs aren't worth the subtle footguns here — two updates inside a
// tx is clear and correct.
func sweepStuckResolvingCards(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin sweep tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Step 1: find stuck ids + clear their resolve state in one shot,
	// returning the card ids so we can flip the parent rows next.
	rows, err := tx.Query(ctx, `
		UPDATE app_card_decisions
		SET resolved_option_id = NULL,
		    resolving_deadline = NULL,
		    resolve_token      = NULL,
		    last_error         = 'timed out while resolving; requeued for retry'
		WHERE resolving_deadline IS NOT NULL AND resolving_deadline < now()
		RETURNING tenant_id, card_id`,
	)
	if err != nil {
		return 0, fmt.Errorf("clearing stuck decision fields: %w", err)
	}
	type key struct {
		tenantID uuid.UUID
		cardID   uuid.UUID
	}
	var stuck []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.tenantID, &k.cardID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scanning stuck row: %w", err)
		}
		stuck = append(stuck, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating stuck rows: %w", err)
	}
	if len(stuck) == 0 {
		_ = tx.Commit(ctx)
		return 0, nil
	}

	// Step 2: flip each parent card back to pending. Guarded by
	// state='resolving' so we don't trample a card someone else just
	// legitimately moved to resolved between our two statements.
	recovered := 0
	for _, k := range stuck {
		tag, err := tx.Exec(ctx, `
			UPDATE app_cards
			SET state = 'pending', terminal_at = NULL, terminal_by = NULL, updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND state = 'resolving'`,
			k.tenantID, k.cardID,
		)
		if err != nil {
			return 0, fmt.Errorf("flipping card %s back to pending: %w", k.cardID, err)
		}
		recovered += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing sweep: %w", err)
	}
	return recovered, nil
}

// beginResolveDecision locks the card and its chosen option, returning them
// for the caller to use inside the transaction it began. The caller is
// responsible for committing. Returns (*Card, *DecisionOption, error).
// Returns ErrAlreadyTerminal if the card is not in pending state.
//
// The returned Card.Decision includes is_gate_artifact and existing
// resolve_token, which ResolveDecision needs for its policy re-check
// and idempotency handling.
func beginResolveDecision(ctx context.Context, tx pgx.Tx, tenantID, cardID uuid.UUID, optionID string) (*Card, *DecisionOption, error) {
	// Lock the card row with FOR UPDATE to serialize concurrent resolves.
	var c Card
	var priority DecisionPriority
	var recommendedOptionID, resolvedOptionID *string
	var resolvedTaskID, originTaskID, originSessionID, resolveToken *uuid.UUID
	var isGateArtifact bool
	err := tx.QueryRow(ctx, `
		SELECT
			c.id, c.tenant_id, c.kind, c.title, c.body, c.state,
			c.created_at, c.updated_at,
			d.priority, d.recommended_option_id, d.resolved_option_id, d.resolved_task_id,
			d.origin_task_id, d.origin_session_id,
			d.is_gate_artifact, d.resolve_token
		FROM app_cards c
		JOIN app_card_decisions d ON d.card_id = c.id AND d.tenant_id = c.tenant_id
		WHERE c.tenant_id = $1 AND c.id = $2
		FOR UPDATE`,
		tenantID, cardID,
	).Scan(
		&c.ID, &c.TenantID, &c.Kind, &c.Title, &c.Body, &c.State,
		&c.CreatedAt, &c.UpdatedAt,
		&priority, &recommendedOptionID, &resolvedOptionID, &resolvedTaskID,
		&originTaskID, &originSessionID,
		&isGateArtifact, &resolveToken,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrCardNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("locking decision card: %w", err)
	}
	if c.State != CardStatePending {
		return nil, nil, ErrAlreadyTerminal
	}
	c.Decision = &DecisionData{
		Priority:        priority,
		OriginTaskID:    originTaskID,
		OriginSessionID: originSessionID,
		IsGateArtifact:  isGateArtifact,
		ResolveToken:    resolveToken,
	}
	if recommendedOptionID != nil {
		c.Decision.RecommendedOptionID = *recommendedOptionID
	}

	// Resolve the option id — use recommended if caller omitted.
	pickID := optionID
	if pickID == "" {
		pickID = c.Decision.RecommendedOptionID
	}
	if pickID == "" {
		return nil, nil, ErrNoOptionPicked
	}
	opt, err := getDecisionOption(ctx, tx, tenantID, cardID, pickID)
	if err != nil {
		return nil, nil, err
	}
	if opt == nil {
		return nil, nil, ErrOptionNotFound
	}
	return &c, opt, nil
}

// finishResolveDecision writes the resolved_option_id, optional task link,
// and flips state to resolved. Runs inside the caller's tx.
func finishResolveDecision(ctx context.Context, tx pgx.Tx, tenantID, cardID, resolvedBy uuid.UUID, optionID string, taskID *uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE app_card_decisions
		SET resolved_option_id = $1, resolved_task_id = $2
		WHERE tenant_id = $3 AND card_id = $4`,
		optionID, taskID, tenantID, cardID,
	); err != nil {
		return fmt.Errorf("writing resolved decision fields: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_cards
		SET state = $1, terminal_at = now(), terminal_by = $2, updated_at = now()
		WHERE tenant_id = $3 AND id = $4`,
		CardStateResolved, resolvedBy, tenantID, cardID,
	); err != nil {
		return fmt.Errorf("flipping card state: %w", err)
	}
	return nil
}

// ackBriefing records a per-user acknowledgment. Role-scoped briefings
// need to stay visible to other role members after one person dismisses
// them, so the ack lives in app_card_user_acks rather than flipping the
// card-level state. Idempotent: re-acking with a different kind updates
// the existing row. Returns ErrAlreadyTerminal if the card has been
// globally cancelled by an admin.
func ackBriefing(ctx context.Context, pool *pgxpool.Pool, tenantID, cardID, ackedBy uuid.UUID, kind BriefingAckKind) (*Card, error) {
	var state CardState
	var cardKind CardKind
	if err := pool.QueryRow(ctx, `
		SELECT state, kind FROM app_cards WHERE tenant_id = $1 AND id = $2`,
		tenantID, cardID,
	).Scan(&state, &cardKind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCardNotFound
		}
		return nil, fmt.Errorf("loading briefing: %w", err)
	}
	if cardKind != CardKindBriefing {
		return nil, fmt.Errorf("cannot ack %s card as briefing", cardKind)
	}
	if state != CardStatePending {
		return nil, ErrAlreadyTerminal
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO app_card_user_acks (tenant_id, card_id, user_id, ack_kind)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (card_id, user_id) DO UPDATE SET ack_kind = EXCLUDED.ack_kind, created_at = now()`,
		tenantID, cardID, ackedBy, kind,
	); err != nil {
		return nil, fmt.Errorf("recording briefing ack: %w", err)
	}
	return getCard(ctx, pool, tenantID, cardID)
}

// Module-level errors returned from the write layer.
var (
	ErrCardNotFound    = errors.New("card not found")
	ErrAlreadyTerminal = errors.New("card is not pending")
	ErrNoOptionPicked  = errors.New("no option_id supplied and no recommended option")
	ErrOptionNotFound  = errors.New("option not found on this decision")
	// ErrGatedResolveFromAgent is returned by ResolveDecisionFromAgent when
	// the picked option's tool is PolicyGate. Gated resolves require a
	// human in the loop (swipe UI, MCP admin, interactive Slack block) —
	// the agent's resolve_decision tool must not double as an approval.
	ErrGatedResolveFromAgent = errors.New("gated tool resolves must be approved by the user, not the agent")
)
