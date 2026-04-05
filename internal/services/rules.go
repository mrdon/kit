package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// RuleTools defines the shared tool metadata for rule operations.
var RuleTools = []ToolMeta{
	{Name: "list_rules", Description: "List all rules.", Schema: props(map[string]any{}), AdminOnly: true},
	{Name: "create_rule", Description: "Create a behavioral rule for Kit.", Schema: propsReq(map[string]any{
		"content":     field("string", "The rule content"),
		"scope":       field("string", "Scope: 'tenant', 'role', or 'task_type'"),
		"scope_value": field("string", "Value: '*' for tenant-wide, role name, or task type"),
		"priority":    map[string]any{"type": "integer", "description": "Priority (higher = more important)"},
	}, "content"), AdminOnly: true},
	{Name: "update_rule", Description: "Update a rule.", Schema: propsReq(map[string]any{
		"rule_id": field("string", "The rule UUID"), "content": field("string", "New content"),
	}, "rule_id", "content"), AdminOnly: true},
	{Name: "delete_rule", Description: "Delete a rule.", Schema: propsReq(map[string]any{"rule_id": field("string", "The rule UUID")}, "rule_id"), AdminOnly: true},
}

// RuleService handles rule operations with authorization.
type RuleService struct {
	pool *pgxpool.Pool
}

// List returns all rules in the tenant. Admin only.
func (s *RuleService) List(ctx context.Context, c *Caller) ([]models.Rule, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.ListRules(ctx, s.pool, c.TenantID)
}

// Create creates a rule. Admin only.
func (s *RuleService) Create(ctx context.Context, c *Caller, content string, priority int, scopeType, scopeValue string) (*models.Rule, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	if scopeType == "" {
		scopeType = "tenant"
	}
	if scopeValue == "" {
		scopeValue = "*"
	}
	return models.CreateRule(ctx, s.pool, c.TenantID, content, priority, scopeType, scopeValue)
}

// Update updates a rule. Admin only.
func (s *RuleService) Update(ctx context.Context, c *Caller, ruleID uuid.UUID, content string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	rule, err := models.GetRule(ctx, s.pool, c.TenantID, ruleID)
	if err != nil {
		return fmt.Errorf("getting rule: %w", err)
	}
	if rule == nil {
		return ErrNotFound
	}
	return models.UpdateRule(ctx, s.pool, c.TenantID, ruleID, content)
}

// Delete deletes a rule. Admin only.
func (s *RuleService) Delete(ctx context.Context, c *Caller, ruleID uuid.UUID) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	return models.DeleteRule(ctx, s.pool, c.TenantID, ruleID)
}
