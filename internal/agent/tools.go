package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// ExecContext holds everything a tool needs to execute.
type ExecContext struct {
	Ctx       context.Context
	Pool      *pgxpool.Pool
	Slack     *kitslack.Client
	Tenant    *models.Tenant
	User      *models.User
	Session   *models.Session
	Channel   string
	ThreadTS  string
}

// ToolFunc is a function that executes a tool and returns a string result.
type ToolFunc func(ec *ExecContext, input json.RawMessage) (string, error)

// ToolRegistry maps tool names to their implementations and definitions.
type ToolRegistry struct {
	tools map[string]ToolFunc
	defs  []anthropic.Tool
}

// NewToolRegistry creates a registry with the core tools.
func NewToolRegistry() *ToolRegistry {
	reg := &ToolRegistry{
		tools: make(map[string]ToolFunc),
	}
	reg.register("send_slack_message", "Send a message to the user in the current Slack thread. This is the ONLY way to respond to the user. You MUST call this tool to reply.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The message text to send (supports Slack markdown)",
			},
		},
		"required": []string{"text"},
	}, toolSendSlackMessage)
	return reg
}

// Definitions returns the tool definitions for the Claude API.
func (r *ToolRegistry) Definitions() []anthropic.Tool {
	return r.defs
}

// Execute runs a tool by name and returns the result string.
func (r *ToolRegistry) Execute(ec *ExecContext, name string, input json.RawMessage) (string, error) {
	fn, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return fn(ec, input)
}

func (r *ToolRegistry) register(name, description string, schema map[string]any, fn ToolFunc) {
	r.tools[name] = fn
	r.defs = append(r.defs, anthropic.Tool{
		Name:        name,
		Description: description,
		InputSchema: schema,
	})
}

// Terminal tools — when called, the agent loop should stop.
var terminalTools = map[string]bool{
	"send_slack_message": true,
}

// IsTerminal returns true if calling this tool should end the agent loop.
func IsTerminal(toolName string) bool {
	return terminalTools[toolName]
}

// --- Tool implementations ---

type sendMessageInput struct {
	Text string `json:"text"`
}

func toolSendSlackMessage(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp sendMessageInput
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	if inp.Text == "" {
		return "error: text is required", nil
	}

	err := ec.Slack.PostMessage(ec.Ctx, ec.Channel, ec.ThreadTS, inp.Text)
	if err != nil {
		return "", fmt.Errorf("posting message: %w", err)
	}

	// Log the sent message
	_ = models.AppendSessionEvent(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.Session.ID, "message_sent", map[string]any{
		"channel":   ec.Channel,
		"thread_ts": ec.ThreadTS,
		"text":      inp.Text,
	})

	return "Message sent successfully.", nil
}

// RegisterAdminTools adds admin-only tools to the registry.
func (r *ToolRegistry) RegisterAdminTools() {
	r.register("list_roles", "List all roles for this organization.", map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}, toolListRoles)

	r.register("create_role", "Create a new role in the organization.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "The role name (e.g., 'bartender', 'manager')"},
			"description": map[string]any{"type": "string", "description": "A brief description of what this role does"},
		},
		"required": []string{"name"},
	}, toolCreateRole)

	r.register("assign_role", "Assign a role to a Slack user.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slack_user_id": map[string]any{"type": "string", "description": "The Slack user ID (e.g., 'U1234567890')"},
			"role_name":     map[string]any{"type": "string", "description": "The name of the role to assign"},
		},
		"required": []string{"slack_user_id", "role_name"},
	}, toolAssignRole)

	r.register("unassign_role", "Remove a role from a user.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slack_user_id": map[string]any{"type": "string", "description": "The Slack user ID"},
			"role_name":     map[string]any{"type": "string", "description": "The name of the role to remove"},
		},
		"required": []string{"slack_user_id", "role_name"},
	}, toolUnassignRole)

	r.register("update_role", "Update a role's description.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "The role name to update"},
			"description": map[string]any{"type": "string", "description": "The new description"},
		},
		"required": []string{"name", "description"},
	}, toolUpdateRole)

	r.register("delete_role", "Delete a role from the organization.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "The role name to delete"},
		},
		"required": []string{"name"},
	}, toolDeleteRole)

	r.register("list_skills", "List all skills (all scopes, not just current role).", map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}, toolListSkills)

	r.register("create_skill", "Create a new skill (knowledge article).", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "Skill name"},
			"description": map[string]any{"type": "string", "description": "Brief description of what this skill covers"},
			"content":     map[string]any{"type": "string", "description": "The full content of the skill (markdown)"},
			"scope":       map[string]any{"type": "string", "description": "Scope: 'tenant' for everyone, or a role name to restrict access", "default": "tenant"},
		},
		"required": []string{"name", "description", "content"},
	}, toolCreateSkill)

	r.register("update_skill", "Update an existing skill's content.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill_id":    map[string]any{"type": "string", "description": "The skill UUID to update"},
			"name":        map[string]any{"type": "string", "description": "New name (optional)"},
			"description": map[string]any{"type": "string", "description": "New description (optional)"},
			"content":     map[string]any{"type": "string", "description": "New content (optional)"},
		},
		"required": []string{"skill_id"},
	}, toolUpdateSkill)

	r.register("delete_skill", "Delete a skill.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill_id": map[string]any{"type": "string", "description": "The skill UUID to delete"},
		},
		"required": []string{"skill_id"},
	}, toolDeleteSkill)

	r.register("list_rules", "List all rules (all scopes).", map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}, toolListRules)

	r.register("create_rule", "Create a behavioral rule for Kit.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content":  map[string]any{"type": "string", "description": "The rule content"},
			"scope":    map[string]any{"type": "string", "description": "Scope type: 'tenant', 'role', or 'task_type'", "default": "tenant"},
			"scope_value": map[string]any{"type": "string", "description": "Scope value: '*' for tenant-wide, role name, or task type", "default": "*"},
			"priority": map[string]any{"type": "integer", "description": "Priority (higher = more important)", "default": 0},
		},
		"required": []string{"content"},
	}, toolCreateRule)

	r.register("update_rule", "Update a rule.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"rule_id": map[string]any{"type": "string", "description": "The rule UUID to update"},
			"content": map[string]any{"type": "string", "description": "New content"},
		},
		"required": []string{"rule_id", "content"},
	}, toolUpdateRule)

	r.register("delete_rule", "Delete a rule.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"rule_id": map[string]any{"type": "string", "description": "The rule UUID to delete"},
		},
		"required": []string{"rule_id"},
	}, toolDeleteRule)

	r.register("forget_memory", "Delete a specific memory.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"memory_id": map[string]any{"type": "string", "description": "The memory UUID to delete"},
		},
		"required": []string{"memory_id"},
	}, toolForgetMemory)

	r.register("update_tenant", "Update the organization's business info and mark setup as complete.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"business_type": map[string]any{"type": "string", "description": "Type of business (e.g., 'brewery', 'nonprofit', 'marching band')"},
			"timezone":      map[string]any{"type": "string", "description": "IANA timezone (e.g., 'America/Denver')", "default": "UTC"},
			"setup_complete": map[string]any{"type": "boolean", "description": "Whether to mark initial setup as complete", "default": false},
		},
		"required": []string{"business_type"},
	}, toolUpdateTenant)
}

// RegisterUserTools adds tools available to all users.
func (r *ToolRegistry) RegisterUserTools() {
	r.register("search_skills", "Search knowledge base for relevant skills using full-text search.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query"},
		},
		"required": []string{"query"},
	}, toolSearchSkills)

	r.register("load_skill", "Load the full content of a specific skill by ID.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill_id": map[string]any{"type": "string", "description": "The skill UUID to load"},
		},
		"required": []string{"skill_id"},
	}, toolLoadSkill)

	r.register("load_reference", "Load a skill reference file by ID.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reference_id": map[string]any{"type": "string", "description": "The reference UUID to load"},
		},
		"required": []string{"reference_id"},
	}, toolLoadReference)

	r.register("save_memory", "Save a fact for future conversations.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{"type": "string", "description": "The fact to remember"},
			"scope":   map[string]any{"type": "string", "description": "Scope: 'user' (default, private), 'tenant' (shared with everyone), or a role name", "default": "user"},
		},
		"required": []string{"content"},
	}, toolSaveMemory)

	r.register("search_memories", "Search saved memories for relevant facts.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query"},
		},
		"required": []string{"query"},
	}, toolSearchMemories)
}

// --- Stub implementations for tools that will be filled in later steps ---

func toolListRoles(ec *ExecContext, input json.RawMessage) (string, error) {
	roles, err := models.ListRoles(ec.Ctx, ec.Pool, ec.Tenant.ID)
	if err != nil {
		return "", err
	}
	if len(roles) == 0 {
		return "No roles defined yet.", nil
	}
	result := "Roles:\n"
	for _, r := range roles {
		desc := ""
		if r.Description != nil {
			desc = " — " + *r.Description
		}
		result += fmt.Sprintf("- %s%s\n", r.Name, desc)
	}
	return result, nil
}

func toolCreateRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	role, err := models.CreateRole(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Name, inp.Description)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' created (ID: %s).", role.Name, role.ID), nil
}

func toolAssignRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SlackUserID string `json:"slack_user_id"`
		RoleName    string `json:"role_name"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}

	// Get or create the user
	user, err := models.GetOrCreateUser(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.SlackUserID, "", false)
	if err != nil {
		return "", err
	}

	err = models.AssignRole(ec.Ctx, ec.Pool, ec.Tenant.ID, user.ID, inp.RoleName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' assigned to user %s.", inp.RoleName, inp.SlackUserID), nil
}

func toolUnassignRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SlackUserID string `json:"slack_user_id"`
		RoleName    string `json:"role_name"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	user, err := models.GetUserBySlackID(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.SlackUserID)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "User not found.", nil
	}
	err = models.UnassignRole(ec.Ctx, ec.Pool, ec.Tenant.ID, user.ID, inp.RoleName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' removed from user %s.", inp.RoleName, inp.SlackUserID), nil
}

func toolUpdateRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	err := models.UpdateRole(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Name, inp.Description)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' updated.", inp.Name), nil
}

func toolDeleteRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	err := models.DeleteRole(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' deleted.", inp.Name), nil
}

func toolUpdateTenant(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		BusinessType  string `json:"business_type"`
		Timezone      string `json:"timezone"`
		SetupComplete bool   `json:"setup_complete"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	tz := inp.Timezone
	if tz == "" {
		tz = "UTC"
	}
	if inp.SetupComplete {
		err := models.UpdateTenantSetup(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.BusinessType, tz)
		if err != nil {
			return "", err
		}
		return "Organization info saved and setup marked as complete!", nil
	}
	err := models.UpdateTenantSetup(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.BusinessType, tz)
	if err != nil {
		return "", err
	}
	return "Organization info updated.", nil
}

// Placeholder tools — will be implemented in their respective steps

func toolListSkills(ec *ExecContext, _ json.RawMessage) (string, error) {
	skills, err := models.ListSkills(ec.Ctx, ec.Pool, ec.Tenant.ID)
	if err != nil {
		return "", err
	}
	if len(skills) == 0 {
		return "No skills defined yet.", nil
	}
	result := "Skills:\n"
	for _, s := range skills {
		result += fmt.Sprintf("- [%s] %s — %s\n", s.ID, s.Name, s.Description)
	}
	return result, nil
}

func toolCreateSkill(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	scope := inp.Scope
	if scope == "" {
		scope = "tenant"
	}
	skill, err := models.CreateSkill(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Name, inp.Description, inp.Content, "chat", scope)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Skill '%s' created (ID: %s).", skill.Name, skill.ID), nil
}

func toolUpdateSkill(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SkillID     string  `json:"skill_id"`
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Content     *string `json:"content"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skillID, err := uuid.Parse(inp.SkillID)
	if err != nil {
		return "Invalid skill ID.", nil
	}
	err = models.UpdateSkill(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID, inp.Name, inp.Description, inp.Content)
	if err != nil {
		return "", err
	}
	return "Skill updated.", nil
}

func toolDeleteSkill(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SkillID string `json:"skill_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skillID, err := uuid.Parse(inp.SkillID)
	if err != nil {
		return "Invalid skill ID.", nil
	}
	err = models.DeleteSkill(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID)
	if err != nil {
		return "", err
	}
	return "Skill deleted.", nil
}

func toolSearchSkills(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	userRoles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID)
	results, err := models.SearchSkills(ec.Ctx, ec.Pool, ec.Tenant.ID, userRoles, inp.Query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No matching skills found.", nil
	}
	out := "Search results:\n"
	for _, s := range results {
		out += fmt.Sprintf("- [%s] %s — %s\n", s.ID, s.Name, s.Description)
	}
	return out, nil
}

func toolLoadSkill(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SkillID string `json:"skill_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skillID, err := uuid.Parse(inp.SkillID)
	if err != nil {
		return "Invalid skill ID.", nil
	}
	skill, err := models.GetSkill(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID)
	if err != nil {
		return "", err
	}
	if skill == nil {
		return "Skill not found.", nil
	}
	return fmt.Sprintf("# %s\n\n%s", skill.Name, skill.Content), nil
}

func toolLoadReference(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		ReferenceID string `json:"reference_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	refID, err := uuid.Parse(inp.ReferenceID)
	if err != nil {
		return "Invalid reference ID.", nil
	}
	ref, err := models.GetSkillReference(ec.Ctx, ec.Pool, ec.Tenant.ID, refID)
	if err != nil {
		return "", err
	}
	if ref == nil {
		return "Reference not found.", nil
	}
	return fmt.Sprintf("# %s\n\n%s", ref.Filename, ref.Content), nil
}

func toolSaveMemory(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Content string `json:"content"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	scope := inp.Scope
	if scope == "" {
		scope = "user"
	}
	scopeType := "user"
	scopeValue := ec.User.SlackUserID
	if scope == "tenant" {
		scopeType = "tenant"
		scopeValue = "*"
	} else if scope != "user" {
		scopeType = "role"
		scopeValue = scope
	}
	err := models.CreateMemory(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Content, scopeType, scopeValue, ec.Session.ID)
	if err != nil {
		return "", err
	}
	return "Got it, I'll remember that.", nil
}

func toolSearchMemories(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	userRoles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID)
	results, err := models.SearchMemories(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.SlackUserID, userRoles, inp.Query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No relevant memories found.", nil
	}
	out := "Memories:\n"
	for _, m := range results {
		out += fmt.Sprintf("- [%s] %s\n", m.ID, m.Content)
	}
	return out, nil
}

func toolListRules(ec *ExecContext, _ json.RawMessage) (string, error) {
	rules, err := models.ListRules(ec.Ctx, ec.Pool, ec.Tenant.ID)
	if err != nil {
		return "", err
	}
	if len(rules) == 0 {
		return "No rules defined yet.", nil
	}
	result := "Rules:\n"
	for _, r := range rules {
		result += fmt.Sprintf("- [%s] (priority %d) %s\n", r.ID, r.Priority, r.Content)
	}
	return result, nil
}

func toolCreateRule(ec *ExecContext, input json.RawMessage) (string, error) {
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
}

func toolUpdateRule(ec *ExecContext, input json.RawMessage) (string, error) {
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
	err = models.UpdateRule(ec.Ctx, ec.Pool, ec.Tenant.ID, ruleID, inp.Content)
	if err != nil {
		return "", err
	}
	return "Rule updated.", nil
}

func toolDeleteRule(ec *ExecContext, input json.RawMessage) (string, error) {
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
	err = models.DeleteRule(ec.Ctx, ec.Pool, ec.Tenant.ID, ruleID)
	if err != nil {
		return "", err
	}
	return "Rule deleted.", nil
}

func toolForgetMemory(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		MemoryID string `json:"memory_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	memoryID, err := uuid.Parse(inp.MemoryID)
	if err != nil {
		return "Invalid memory ID.", nil
	}
	err = models.DeleteMemory(ec.Ctx, ec.Pool, ec.Tenant.ID, memoryID)
	if err != nil {
		return "", err
	}
	return "Memory forgotten.", nil
}
