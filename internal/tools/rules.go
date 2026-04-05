package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

func registerRuleTools(r *Registry, isAdmin bool) {
	for _, meta := range services.RuleTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     ruleHandler(meta.Name),
		})
	}
}

func ruleHandler(name string) HandlerFunc {
	switch name {
	case "list_rules":
		return handleListRules
	case "create_rule":
		return handleCreateRule
	case "update_rule":
		return handleUpdateRule
	case "delete_rule":
		return handleDeleteRule
	default:
		return func(_ *ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown rule tool: %s", name)
		}
	}
}

func handleListRules(ec *ExecContext, _ json.RawMessage) (string, error) {
	rules, err := ec.Svc.Rules.List(ec.Ctx, ec.Caller())
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
}

func handleCreateRule(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Content    string `json:"content"`
		Scope      string `json:"scope"`
		ScopeValue string `json:"scope_value"`
		Priority   int    `json:"priority"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	rule, err := ec.Svc.Rules.Create(ec.Ctx, ec.Caller(), inp.Content, inp.Priority, inp.Scope, inp.ScopeValue)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Rule created (ID: %s).", rule.ID), nil
}

func handleUpdateRule(ec *ExecContext, input json.RawMessage) (string, error) {
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
	if err := ec.Svc.Rules.Update(ec.Ctx, ec.Caller(), ruleID, inp.Content); err != nil {
		return "", err
	}
	return "Rule updated.", nil
}

func handleDeleteRule(ec *ExecContext, input json.RawMessage) (string, error) {
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
	if err := ec.Svc.Rules.Delete(ec.Ctx, ec.Caller(), ruleID); err != nil {
		return "", err
	}
	return "Rule deleted.", nil
}
