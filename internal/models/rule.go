package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Rule struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Content   string
	Priority  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

func ListRules(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Rule, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, content, priority, created_at, updated_at
		FROM rules WHERE tenant_id = $1 ORDER BY priority DESC, created_at
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing rules: %w", err)
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Content, &r.Priority, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// GetRulesForContext returns rules matching the user's roles, ordered by priority.
func GetRulesForContext(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID) ([]Rule, error) {
	scopeSQL, scopeArgs := ScopeFilterIDs("sc", 2, userID, roleIDs)
	args := append([]any{tenantID}, scopeArgs...)
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT r.id, r.tenant_id, r.content, r.priority, r.created_at, r.updated_at
		FROM rules r
		JOIN rule_scopes rs ON rs.rule_id = r.id AND rs.tenant_id = r.tenant_id
		JOIN scopes sc ON sc.id = rs.scope_id
		WHERE r.tenant_id = $1
		AND (`+scopeSQL+`)
		ORDER BY r.priority DESC, r.created_at
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("getting rules for context: %w", err)
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Content, &r.Priority, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// CreateRule creates a rule with a single scope row. Pass roleID for a role
// scope, userID for a user scope, or both nil for tenant-wide.
func CreateRule(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, content string, priority int, roleID, userID *uuid.UUID) (*Rule, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	rule := &Rule{}
	ruleID := uuid.New()
	err = tx.QueryRow(ctx, `
		INSERT INTO rules (id, tenant_id, content, priority)
		VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, content, priority, created_at, updated_at
	`, ruleID, tenantID, content, priority).Scan(
		&rule.ID, &rule.TenantID, &rule.Content, &rule.Priority, &rule.CreatedAt, &rule.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating rule: %w", err)
	}

	scopeID, err := getOrCreateScopeTx(ctx, tx, tenantID, roleID, userID)
	if err != nil {
		return nil, fmt.Errorf("get-or-create scope: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO rule_scopes (tenant_id, rule_id, scope_id)
		VALUES ($1, $2, $3)
	`, tenantID, ruleID, scopeID)
	if err != nil {
		return nil, fmt.Errorf("creating rule scope: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	return rule, nil
}

func UpdateRule(ctx context.Context, pool *pgxpool.Pool, tenantID, ruleID uuid.UUID, content string) error {
	_, err := pool.Exec(ctx, `
		UPDATE rules SET content = $3, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, ruleID, content)
	if err != nil {
		return fmt.Errorf("updating rule: %w", err)
	}
	return nil
}

// GetRule returns a single rule by ID.
func GetRule(ctx context.Context, pool *pgxpool.Pool, tenantID, ruleID uuid.UUID) (*Rule, error) {
	r := &Rule{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, content, priority, created_at, updated_at
		FROM rules WHERE tenant_id = $1 AND id = $2
	`, tenantID, ruleID).Scan(&r.ID, &r.TenantID, &r.Content, &r.Priority, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting rule: %w", err)
	}
	return r, nil
}

func DeleteRule(ctx context.Context, pool *pgxpool.Pool, tenantID, ruleID uuid.UUID) error {
	_, err := pool.Exec(ctx, `DELETE FROM rules WHERE tenant_id = $1 AND id = $2`, tenantID, ruleID)
	if err != nil {
		return fmt.Errorf("deleting rule: %w", err)
	}
	return nil
}
