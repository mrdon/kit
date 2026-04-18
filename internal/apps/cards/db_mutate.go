package cards

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
		if err := updateDecisionTx(ctx, tx, cardID, *u.Decision); err != nil {
			return nil, err
		}
	}

	// Briefing child updates.
	if u.Briefing != nil {
		if kind != CardKindBriefing {
			return nil, fmt.Errorf("cannot apply briefing updates to %s card", kind)
		}
		if u.Briefing.Severity != nil {
			if _, err := tx.Exec(ctx, `UPDATE app_card_briefings SET severity = $1 WHERE card_id = $2`, *u.Briefing.Severity, cardID); err != nil {
				return nil, fmt.Errorf("updating briefing severity: %w", err)
			}
		}
	}

	// Scope replacement.
	if u.RoleScopes != nil {
		if err := writeScopesTx(ctx, tx, tenantID, cardID, *u.RoleScopes); err != nil {
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

func updateDecisionTx(ctx context.Context, tx pgx.Tx, cardID uuid.UUID, u DecisionUpdates) error {
	var sets []string
	var args []any
	argN := 1 // $1 = card_id (WHERE)
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
		query := fmt.Sprintf(`UPDATE app_card_decisions SET %s WHERE card_id = $1`, strings.Join(sets, ", "))
		args = append([]any{cardID}, args...)
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("updating decision: %w", err)
		}
	}

	// Full replacement of options when caller supplied them.
	if u.Options != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM app_card_decision_options WHERE card_id = $1`, cardID); err != nil {
			return fmt.Errorf("clearing options: %w", err)
		}
		for i, opt := range *u.Options {
			if _, err := tx.Exec(ctx, `
				INSERT INTO app_card_decision_options (card_id, option_id, sort_order, label, prompt)
				VALUES ($1, $2, $3, $4, $5)`,
				cardID, opt.OptionID, i, opt.Label, nilIfEmpty(opt.Prompt),
			); err != nil {
				return fmt.Errorf("inserting option %q: %w", opt.OptionID, err)
			}
		}
	}
	return nil
}

// beginResolveDecision locks the card and its chosen option, returning them
// for the caller to use inside the transaction it began. The caller is
// responsible for committing. Returns (*Card, *DecisionOption, error).
// Returns ErrAlreadyTerminal if the card is not in pending state.
func beginResolveDecision(ctx context.Context, tx pgx.Tx, tenantID, cardID uuid.UUID, optionID string) (*Card, *DecisionOption, error) {
	// Lock the card row with FOR UPDATE to serialize concurrent resolves.
	var c Card
	var priority DecisionPriority
	var recommendedOptionID, resolvedOptionID *string
	var resolvedTaskID *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT
			c.id, c.tenant_id, c.kind, c.title, c.body, c.state,
			c.created_at, c.updated_at,
			d.priority, d.recommended_option_id, d.resolved_option_id, d.resolved_task_id
		FROM app_cards c
		JOIN app_card_decisions d ON d.card_id = c.id
		WHERE c.tenant_id = $1 AND c.id = $2
		FOR UPDATE`,
		tenantID, cardID,
	).Scan(
		&c.ID, &c.TenantID, &c.Kind, &c.Title, &c.Body, &c.State,
		&c.CreatedAt, &c.UpdatedAt,
		&priority, &recommendedOptionID, &resolvedOptionID, &resolvedTaskID,
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
	c.Decision = &DecisionData{Priority: priority}
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
	opt, err := getDecisionOption(ctx, tx, cardID, pickID)
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
func finishResolveDecision(ctx context.Context, tx pgx.Tx, cardID, resolvedBy uuid.UUID, optionID string, taskID *uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE app_card_decisions
		SET resolved_option_id = $1, resolved_task_id = $2
		WHERE card_id = $3`,
		optionID, taskID, cardID,
	); err != nil {
		return fmt.Errorf("writing resolved decision fields: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_cards
		SET state = $1, terminal_at = now(), terminal_by = $2, updated_at = now()
		WHERE id = $3`,
		CardStateResolved, resolvedBy, cardID,
	); err != nil {
		return fmt.Errorf("flipping card state: %w", err)
	}
	return nil
}

// ackBriefing transitions a briefing card to its ack terminal state.
// Returns ErrAlreadyTerminal if the card is not pending.
func ackBriefing(ctx context.Context, pool *pgxpool.Pool, tenantID, cardID, ackedBy uuid.UUID, kind BriefingAckKind) (*Card, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var state CardState
	var cardKind CardKind
	if err := tx.QueryRow(ctx, `
		SELECT state, kind FROM app_cards WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
		tenantID, cardID,
	).Scan(&state, &cardKind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCardNotFound
		}
		return nil, fmt.Errorf("locking briefing: %w", err)
	}
	if cardKind != CardKindBriefing {
		return nil, fmt.Errorf("cannot ack %s card as briefing", cardKind)
	}
	if state != CardStatePending {
		return nil, ErrAlreadyTerminal
	}

	if _, err := tx.Exec(ctx, `
		UPDATE app_cards
		SET state = $1, terminal_at = now(), terminal_by = $2, updated_at = now()
		WHERE id = $3`,
		kind.State(), ackedBy, cardID,
	); err != nil {
		return nil, fmt.Errorf("flipping card state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	return getCard(ctx, pool, tenantID, cardID)
}

// Module-level errors returned from the write layer.
var (
	ErrCardNotFound    = errors.New("card not found")
	ErrAlreadyTerminal = errors.New("card is not pending")
	ErrNoOptionPicked  = errors.New("no option_id supplied and no recommended option")
	ErrOptionNotFound  = errors.New("option not found on this decision")
)
