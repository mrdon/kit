package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

func registerRuleTools(r *Registry, isAdmin bool) {
	if !isAdmin {
		return
	}

	r.Register(Def{
		Name: "list_rules", Description: "List all rules.",
		Schema: props(map[string]any{}), AdminOnly: true,
		Handler: func(ec *ExecContext, _ json.RawMessage) (string, error) {
			rules, err := models.ListRules(ec.Ctx, ec.Pool, ec.Tenant.ID)
			if err != nil {
				return "", err
			}
			if len(rules) == 0 {
				return "No rules defined yet.", nil
			}
			var b strings.Builder
			b.WriteString("Rules:\n")
			for _, rule := range rules {
				fmt.Fprintf(&b, "- [%s] (priority %d) %s\n", rule.ID, rule.Priority, rule.Content)
			}
			return b.String(), nil
		},
	})

	r.Register(Def{
		Name: "create_rule", Description: "Create a behavioral rule for Kit.",
		Schema: propsReq(map[string]any{
			"content":     field("string", "The rule content"),
			"scope":       field("string", "Scope: 'tenant', 'role', or 'task_type'"),
			"scope_value": field("string", "Value: '*' for tenant-wide, role name, or task type"),
			"priority":    map[string]any{"type": "integer", "description": "Priority (higher = more important)"},
		}, "content"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Content    string `json:"content"`
				Scope      string `json:"scope"`
				ScopeValue string `json:"scope_value"`
				Priority   int    `json:"priority"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			scope := inp.Scope
			if scope == "" {
				scope = "tenant"
			}
			scopeValue := inp.ScopeValue
			if scopeValue == "" {
				scopeValue = "*"
			}
			rule, err := models.CreateRule(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Content, inp.Priority, scope, scopeValue)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Rule created (ID: %s).", rule.ID), nil
		},
	})

	r.Register(Def{
		Name: "update_rule", Description: "Update a rule.",
		Schema: propsReq(map[string]any{
			"rule_id": field("string", "The rule UUID"),
			"content": field("string", "New content"),
		}, "rule_id", "content"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				RuleID  string `json:"rule_id"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			ruleID, err := uuid.Parse(inp.RuleID)
			if err != nil {
				return "Invalid rule ID.", nil
			}
			if err := models.UpdateRule(ec.Ctx, ec.Pool, ec.Tenant.ID, ruleID, inp.Content); err != nil {
				return "", err
			}
			return "Rule updated.", nil
		},
	})

	r.Register(Def{
		Name: "delete_rule", Description: "Delete a rule.",
		Schema: propsReq(map[string]any{"rule_id": field("string", "The rule UUID")}, "rule_id"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				RuleID string `json:"rule_id"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			ruleID, err := uuid.Parse(inp.RuleID)
			if err != nil {
				return "Invalid rule ID.", nil
			}
			if err := models.DeleteRule(ec.Ctx, ec.Pool, ec.Tenant.ID, ruleID); err != nil {
				return "", err
			}
			return "Rule deleted.", nil
		},
	})
}
