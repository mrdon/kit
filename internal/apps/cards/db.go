package cards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// createCardTx inserts the parent row, the kind-specific child row, option
// rows (for decisions), and scope rows — all in one transaction.
func createCardTx(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, in CardCreateInput) (*Card, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx)

	cardID := uuid.New()
	card := &Card{
		ID:       cardID,
		TenantID: tenantID,
		Kind:     in.Kind,
		Title:    in.Title,
		Body:     in.Body,
		State:    CardStatePending,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO app_cards (id, tenant_id, kind, title, body, state)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`,
		cardID, tenantID, in.Kind, in.Title, in.Body, CardStatePending,
	).Scan(&card.CreatedAt, &card.UpdatedAt); err != nil {
		return nil, fmt.Errorf("inserting card: %w", err)
	}

	switch in.Kind {
	case CardKindDecision:
		if in.Decision == nil {
			return nil, errors.New("decision input required for decision card")
		}
		d := in.Decision
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_card_decisions (tenant_id, card_id, priority, recommended_option_id, origin_task_id, origin_session_id, is_gate_artifact)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			tenantID, cardID, d.Priority, nilIfEmpty(d.RecommendedOptionID), d.OriginTaskID, d.OriginSessionID, d.IsGateArtifact,
		); err != nil {
			return nil, fmt.Errorf("inserting decision: %w", err)
		}
		for i, opt := range d.Options {
			if _, err := tx.Exec(ctx, `
				INSERT INTO app_card_decision_options (tenant_id, card_id, option_id, sort_order, label, prompt, tool_name, tool_arguments)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				tenantID, cardID, opt.OptionID, i, opt.Label, nilIfEmpty(opt.Prompt), nilIfEmpty(opt.ToolName), nilIfEmptyBytes(opt.ToolArguments),
			); err != nil {
				return nil, fmt.Errorf("inserting option %q: %w", opt.OptionID, err)
			}
		}
		card.Decision = &DecisionData{
			Priority:            d.Priority,
			RecommendedOptionID: d.RecommendedOptionID,
			OriginTaskID:        d.OriginTaskID,
			OriginSessionID:     d.OriginSessionID,
			IsGateArtifact:      d.IsGateArtifact,
			Options:             append([]DecisionOption(nil), d.Options...),
		}
	case CardKindBriefing:
		if in.Briefing == nil {
			return nil, errors.New("briefing input required for briefing card")
		}
		b := in.Briefing
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_card_briefings (tenant_id, card_id, severity)
			VALUES ($1, $2, $3)`,
			tenantID, cardID, b.Severity,
		); err != nil {
			return nil, fmt.Errorf("inserting briefing: %w", err)
		}
		card.Briefing = &BriefingData{Severity: b.Severity}
	default:
		return nil, fmt.Errorf("unknown card kind %q", in.Kind)
	}

	if err := writeScopesTx(ctx, tx, tenantID, cardID, in.RoleScopes, in.UserScopes); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	return card, nil
}

// writeScopesTx replaces the card's scope rows. With both roleScopes and
// userScopes empty, defaults to tenant-wide. With either populated, the
// card is visible to the union of named principals (no implicit
// tenant-wide). Existing scope rows are deleted first so this is safe to
// call on update too.
//
// Each user_id in userScopes must belong to the caller's tenant. The
// ScopeFilterIDs visibility filter joins on caller's tenant_id so
// cross-tenant principals would never match — but untenanted ids would
// still pollute the scopes table. Validate up front; refuse the whole
// transaction on the first miss.
func writeScopesTx(ctx context.Context, tx pgx.Tx, tenantID, cardID uuid.UUID, roleScopes []string, userScopes []uuid.UUID) error {
	if err := validateUserScopesTenancy(ctx, tx, tenantID, userScopes); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM app_card_scopes WHERE tenant_id = $1 AND card_id = $2`, tenantID, cardID); err != nil {
		return fmt.Errorf("clearing scopes: %w", err)
	}

	insertScope := func(roleID, userID *uuid.UUID) error {
		scopeID, err := models.GetOrCreateScopeTx(ctx, tx, tenantID, roleID, userID)
		if err != nil {
			return fmt.Errorf("get-or-create scope: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_card_scopes (tenant_id, card_id, scope_id)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`,
			tenantID, cardID, scopeID,
		); err != nil {
			return fmt.Errorf("inserting card scope: %w", err)
		}
		return nil
	}

	if len(roleScopes) == 0 && len(userScopes) == 0 {
		// Default: tenant-wide.
		return insertScope(nil, nil)
	}

	for _, role := range roleScopes {
		var roleID uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT id FROM roles WHERE tenant_id = $1 AND name = $2`,
			tenantID, role).Scan(&roleID)
		if err != nil {
			return fmt.Errorf("looking up role %q: %w", role, err)
		}
		if err := insertScope(&roleID, nil); err != nil {
			return err
		}
	}
	for i := range userScopes {
		uid := userScopes[i]
		if err := insertScope(nil, &uid); err != nil {
			return err
		}
	}
	return nil
}

// validateUserScopesTenancy confirms every user id in the slice belongs
// to the given tenant. The scopes table FKs to users(id) but doesn't
// constrain the user's tenant, so without this check an admin could
// pass a foreign-tenant uuid that lands in the scopes table forever
// (never matches any visibility filter, just clutter).
func validateUserScopesTenancy(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, userIDs []uuid.UUID) error {
	if len(userIDs) == 0 {
		return nil
	}
	// Dedup first so the count comparison stays meaningful when the
	// caller passes the same uuid twice.
	seen := make(map[uuid.UUID]struct{}, len(userIDs))
	uniq := make([]uuid.UUID, 0, len(userIDs))
	for _, id := range userIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	var found int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE tenant_id = $1 AND id = ANY($2)`,
		tenantID, uniq,
	).Scan(&found); err != nil {
		return fmt.Errorf("validate user scopes tenancy: %w", err)
	}
	if found != len(uniq) {
		return errors.New("scope references a user not in this tenant")
	}
	return nil
}

// getCard loads a single card plus its kind-specific data. Returns nil if
// the card does not exist in this tenant.
func getCard(ctx context.Context, pool *pgxpool.Pool, tenantID, cardID uuid.UUID) (*Card, error) {
	card, err := scanCardRow(pool.QueryRow(ctx, baseCardQuery+` WHERE c.tenant_id = $1 AND c.id = $2`, tenantID, cardID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil
	}
	if err != nil {
		return nil, fmt.Errorf("loading card: %w", err)
	}
	if card.Kind == CardKindDecision {
		if err := loadDecisionOptions(ctx, pool, tenantID, []*Card{card}); err != nil {
			return nil, err
		}
	}
	return card, nil
}

// listCards is used by list_decisions / list_briefings — simpler than the
// stack query, filters by kind + optional state + priority/severity.
func listCards(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, kind CardKind, f CardFilters, roleIDs []uuid.UUID, isAdmin bool) ([]*Card, error) {
	var b strings.Builder
	b.WriteString(baseCardQuery)
	args := []any{tenantID}
	argN := 1

	if isAdmin {
		b.WriteString(` WHERE c.tenant_id = $1`)
	} else {
		scopeSQL, scopeArgs := models.ScopeFilterIDs("sc", 2, userID, roleIDs)
		b.WriteString(` JOIN app_card_scopes s ON s.card_id = c.id JOIN scopes sc ON sc.id = s.scope_id WHERE c.tenant_id = $1 AND (`)
		b.WriteString(scopeSQL)
		b.WriteString(`)`)
		args = append(args, scopeArgs...)
		argN = 1 + len(scopeArgs)
	}
	argN++
	fmt.Fprintf(&b, ` AND c.kind = $%d`, argN)
	args = append(args, kind)
	if f.State != "" {
		argN++
		fmt.Fprintf(&b, ` AND c.state = $%d`, argN)
		args = append(args, f.State)
	}
	if kind == CardKindDecision && f.Priority != "" {
		argN++
		fmt.Fprintf(&b, ` AND d.priority = $%d`, argN)
		args = append(args, f.Priority)
	}
	if kind == CardKindBriefing && f.Severity != "" {
		argN++
		fmt.Fprintf(&b, ` AND b.severity = $%d`, argN)
		args = append(args, f.Severity)
	}
	if isAdmin {
		b.WriteString(` ORDER BY c.created_at DESC LIMIT 100`)
	} else {
		// DISTINCT because scope joins can duplicate rows when a card has
		// multiple matching scope rows.
		b.WriteString(` ORDER BY c.created_at DESC LIMIT 100`)
	}

	query := b.String()
	// Admin path doesn't join scopes, so no DISTINCT needed. For non-admin
	// we rewrite SELECT ... to SELECT DISTINCT ON (c.id) to dedupe.
	if !isAdmin {
		query = strings.Replace(query, "SELECT ", "SELECT DISTINCT ", 1)
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing cards: %w", err)
	}
	defer rows.Close()

	var cards []*Card
	for rows.Next() {
		c, err := scanCardRow(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if kind == CardKindDecision {
		if err := loadDecisionOptions(ctx, pool, tenantID, cards); err != nil {
			return nil, err
		}
	}
	return cards, nil
}

// listStack returns pending cards for the caller, ordered per the PRD's
// interleaved priority-vs-severity rule and filtered by per-user
// briefing acks. Role-scoped briefings stay visible to role members
// until each individually acks; the LEFT JOIN + IS NULL filter
// implements that per-user exclusion.
//
// The swipe stack is a personal surface, so scope filtering applies even
// to admins — otherwise admins see every user's personal decisions and
// briefings in their feed. Admin audit of other users' cards goes through
// list_decisions / list_briefings.
func listStack(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID) ([]*Card, error) {
	var b strings.Builder
	b.WriteString(baseCardQuery)
	args := []any{tenantID, userID}
	b.WriteString(` LEFT JOIN app_card_user_acks ua ON ua.card_id = c.id AND ua.user_id = $2`)

	scopeSQL, scopeArgs := models.ScopeFilterIDs("sc", 3, userID, roleIDs)
	b.WriteString(` JOIN app_card_scopes s ON s.card_id = c.id JOIN scopes sc ON sc.id = s.scope_id WHERE c.tenant_id = $1 AND (`)
	b.WriteString(scopeSQL)
	b.WriteString(`) AND c.state = $`)
	fmt.Fprintf(&b, "%d", 3+len(scopeArgs))
	b.WriteString(` AND ua.card_id IS NULL`)
	args = append(args, scopeArgs...)
	args = append(args, CardStatePending)
	b.WriteString(`
		ORDER BY
			CASE
				WHEN c.kind = 'decision' AND d.priority = 'high'      THEN 0
				WHEN c.kind = 'decision' AND d.priority = 'medium'    THEN 1
				WHEN c.kind = 'briefing' AND b.severity = 'important' THEN 2
				WHEN c.kind = 'decision' AND d.priority = 'low'       THEN 3
				WHEN c.kind = 'briefing' AND b.severity = 'notable'   THEN 4
				WHEN c.kind = 'briefing' AND b.severity = 'info'      THEN 5
			END,
			c.created_at DESC
		LIMIT 100`)

	// DISTINCT because a card can match multiple scope rows (personal + role).
	query := strings.Replace(b.String(), "SELECT ", "SELECT DISTINCT ", 1)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("loading stack: %w", err)
	}
	defer rows.Close()

	var cards []*Card
	for rows.Next() {
		c, err := scanCardRow(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := loadDecisionOptions(ctx, pool, tenantID, cards); err != nil {
		return nil, err
	}
	return cards, nil
}

// loadDecisionOptions fetches option rows in bulk for all decision cards in
// the slice, then attaches them to each card's Decision.Options.
func loadDecisionOptions(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, cards []*Card) error {
	var ids []uuid.UUID
	byID := map[uuid.UUID]*Card{}
	for _, c := range cards {
		if c.Kind != CardKindDecision {
			continue
		}
		if c.Decision == nil {
			c.Decision = &DecisionData{}
		}
		ids = append(ids, c.ID)
		byID[c.ID] = c
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := pool.Query(ctx, `
		SELECT card_id, option_id, sort_order, label, prompt, tool_name, tool_arguments
		FROM app_card_decision_options
		WHERE tenant_id = $1 AND card_id = ANY($2)
		ORDER BY card_id, sort_order`, tenantID, ids)
	if err != nil {
		return fmt.Errorf("loading decision options: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cardID uuid.UUID
		var opt DecisionOption
		var prompt, toolName *string
		var toolArgs []byte
		if err := rows.Scan(&cardID, &opt.OptionID, &opt.SortOrder, &opt.Label, &prompt, &toolName, &toolArgs); err != nil {
			return fmt.Errorf("scanning option: %w", err)
		}
		if prompt != nil {
			opt.Prompt = *prompt
		}
		if toolName != nil {
			opt.ToolName = *toolName
		}
		if len(toolArgs) > 0 {
			opt.ToolArguments = append(json.RawMessage(nil), toolArgs...)
		}
		c := byID[cardID]
		if c == nil {
			continue
		}
		c.Decision.Options = append(c.Decision.Options, opt)
	}
	return rows.Err()
}

// getDecisionOption returns a single option for a decision card. Returns
// nil if the option doesn't exist.
func getDecisionOption(ctx context.Context, q querier, tenantID, cardID uuid.UUID, optionID string) (*DecisionOption, error) {
	var opt DecisionOption
	var prompt, toolName *string
	var toolArgs []byte
	err := q.QueryRow(ctx, `
		SELECT option_id, sort_order, label, prompt, tool_name, tool_arguments
		FROM app_card_decision_options
		WHERE tenant_id = $1 AND card_id = $2 AND option_id = $3`,
		tenantID, cardID, optionID,
	).Scan(&opt.OptionID, &opt.SortOrder, &opt.Label, &prompt, &toolName, &toolArgs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil
	}
	if err != nil {
		return nil, fmt.Errorf("loading option: %w", err)
	}
	if prompt != nil {
		opt.Prompt = *prompt
	}
	if toolName != nil {
		opt.ToolName = *toolName
	}
	if len(toolArgs) > 0 {
		opt.ToolArguments = append(json.RawMessage(nil), toolArgs...)
	}
	return &opt, nil
}

// querier lets us accept either *pgxpool.Pool or pgx.Tx.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
