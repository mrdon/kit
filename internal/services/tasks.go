package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// TaskTools defines the shared tool metadata for task operations.
var TaskTools = []ToolMeta{
	{Name: "create_task", Description: "Schedule a recurring or one-time task. Kit runs the description through the full agent at the scheduled time. For non-trivial tasks — especially those needing argument pinning, tool allow-lists, or forced approval gates — load the `creating-tasks` builtin skill before calling.", Schema: propsReq(map[string]any{
		"description": field("string", "What to do when the task runs"),
		"cron_expr":   field("string", "Cron expression for recurring tasks: minute hour day-of-month month day-of-week"),
		"run_at":      field("string", "ISO 8601 datetime for one-time tasks (e.g. '2026-04-05T21:20:00'). Use this OR cron_expr, not both."),
		"channel_id":  field("string", "Slack channel ID where output should be posted"),
		"scope":       field("string", "Scope: 'user' (default), 'tenant' (admin only), or a role name"),
		"policy":      policyField(),
	}, "description")},
	{Name: "list_tasks", Description: "List scheduled tasks visible to the current user.", Schema: props(map[string]any{})},
	{Name: "update_task", Description: "Update or delete a scheduled task. Provide description to change it, policy to replace its capability manifest, or set delete=true to remove the task. See the `creating-tasks` skill for policy shape.", Schema: propsReq(map[string]any{
		"id":          field("string", "The task UUID"),
		"description": field("string", "New task description (optional)"),
		"policy":      policyField(),
		"delete":      field("boolean", "Set to true to delete the task (optional)"),
	}, "id")},
}

// policyField returns the JSON-schema fragment describing the optional
// task policy object. Kept as a nested object schema so MCP clients and
// the agent's tool catalogue see the field exists, but the full
// design guidance (when to allow-list vs force-gate vs pin) lives in
// the `creating-tasks` builtin skill to avoid bloating every agent
// turn's system prompt.
func policyField() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Optional capability manifest constraining the scheduled agent. See the `creating-tasks` skill for full design guidance, examples, and gotchas.",
		"properties": map[string]any{
			"allowed_tools": map[string]any{
				"type":        "array",
				"description": "If present, only these tool names plus agent-infrastructure (load_skill, etc.) may run. Omit for no restriction; [] for infrastructure-only.",
				"items":       map[string]any{"type": "string"},
			},
			"force_gate": map[string]any{
				"type":        "array",
				"description": "Tool names that always route through an approval card at fire time, even if the agent omitted require_approval.",
				"items":       map[string]any{"type": "string"},
			},
			"pinned_args": map[string]any{
				"type":                 "object",
				"description":          "Map of tool_name → {arg_key: fixed_value}. Pinned values override whatever the agent supplied before the gate check.",
				"additionalProperties": map[string]any{"type": "object"},
			},
		},
	}
}

// TaskService handles task operations with authorization.
type TaskService struct {
	pool *pgxpool.Pool
}

// CreateInput bundles the arguments for TaskService.Create. Policy is
// optional; nil means "no capability restrictions," matching today's
// behaviour for every task that predates the policy feature.
type CreateInput struct {
	Description string
	CronExpr    string
	Timezone    string
	ChannelID   string
	Scope       string
	Model       string
	RunOnce     bool
	RunAt       *time.Time
	Policy      *models.Policy
}

// Create creates a scheduled task with scope resolution.
// in.Scope: "user" (default), "tenant" (admin only), or a role name.
// in.Model is the tier name ("haiku" | "sonnet") picked by the
// classifier in the tool layer; empty defaults to Haiku at the DB level.
func (s *TaskService) Create(ctx context.Context, c *Caller, in CreateInput) (*models.Task, error) {
	scope := in.Scope
	if scope == "" {
		scope = string(models.ScopeTypeUser)
	}
	var roleID, userID *uuid.UUID
	switch scope {
	case string(models.ScopeTypeUser):
		userID = &c.UserID
	case string(models.ScopeTypeTenant):
		if !c.IsAdmin {
			return nil, ErrForbidden
		}
		// roleID and userID stay nil → tenant-wide
	default:
		if !c.IsAdmin && !hasRole(c, scope) {
			return nil, ErrForbidden
		}
		rid, err := ResolveRoleID(ctx, s.pool, c.TenantID, scope)
		if err != nil {
			return nil, err
		}
		roleID = &rid
	}
	task, err := models.CreateTask(ctx, s.pool, c.TenantID, c.UserID, in.Description, in.CronExpr, in.Timezone, in.ChannelID, in.Model, in.RunOnce, in.RunAt, roleID, userID)
	if err != nil {
		return nil, err
	}
	if in.Policy != nil {
		if err := models.UpdateTaskPolicy(ctx, s.pool, c.TenantID, task.ID, in.Policy); err != nil {
			return nil, fmt.Errorf("writing task policy: %w", err)
		}
		task.Config, _ = models.SetConfigPolicy(task.Config, in.Policy)
	}
	return task, nil
}

// List returns tasks visible to the caller through normal scope filtering:
// tasks scoped to them personally, to roles they hold, or to the tenant.
// Admins are not granted extra visibility into other users' personal tasks —
// those run with the creator's identity (email, memories) and the admin is
// not that user.
func (s *TaskService) List(ctx context.Context, c *Caller) ([]models.Task, error) {
	return models.ListTasksForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
}

// UpdateInput bundles the optional fields a task update can change. Nil
// means "don't touch." Policy is replace-wholesale — a non-nil pointer
// overwrites the task's policy; callers that want to tweak a single
// sub-field must read the current policy and re-write the full shape.
type UpdateInput struct {
	Description *string
	Policy      *models.Policy
}

// Update updates a task's description and/or policy. The caller must
// have the task in their visible scope; admins don't bypass this for
// other users' personal tasks. Fields with nil pointers are left
// untouched. A non-nil Policy replaces the task's policy wholesale.
func (s *TaskService) Update(ctx context.Context, c *Caller, taskID uuid.UUID, in UpdateInput) error {
	visible, err := models.ListTasksForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
	if err != nil {
		return fmt.Errorf("listing visible tasks: %w", err)
	}
	found := false
	for _, t := range visible {
		if t.ID == taskID {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	if in.Description != nil {
		if err := models.UpdateTaskDescription(ctx, s.pool, c.TenantID, taskID, *in.Description); err != nil {
			return err
		}
	}
	if in.Policy != nil {
		if err := models.UpdateTaskPolicy(ctx, s.pool, c.TenantID, taskID, in.Policy); err != nil {
			return err
		}
	}
	return nil
}

// Delete deletes a task in the caller's visible scope. Admins don't bypass
// this for other users' personal tasks.
func (s *TaskService) Delete(ctx context.Context, c *Caller, taskID uuid.UUID) error {
	visible, err := models.ListTasksForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
	if err != nil {
		return fmt.Errorf("listing visible tasks: %w", err)
	}
	for _, t := range visible {
		if t.ID == taskID {
			return models.DeleteTask(ctx, s.pool, c.TenantID, taskID)
		}
	}
	return ErrNotFound
}

// FormatTaskPolicySummary renders a compact description of a task's
// policy (as persisted in task.config JSONB) for list_tasks output,
// e.g. "policy: allow-list(4), force-gate(post_to_channel), pinned(channel)".
// Returns "" when the task has no policy. Lives here so both agent-side
// and MCP-side list_tasks formatters render identically — per
// CLAUDE.md's shared-tool-parity rule.
func FormatTaskPolicySummary(cfg []byte) string {
	policy, err := models.ParseConfigPolicy(cfg)
	if err != nil || policy == nil {
		return ""
	}
	var parts []string
	if policy.AllowedTools != nil {
		parts = append(parts, fmt.Sprintf("allow-list(%d)", len(*policy.AllowedTools)))
	}
	if len(policy.ForceGate) > 0 {
		parts = append(parts, "force-gate("+strings.Join(policy.ForceGate, ",")+")")
	}
	if len(policy.PinnedArgs) > 0 {
		var keys []string
		for tool, args := range policy.PinnedArgs {
			for k := range args {
				keys = append(keys, tool+"."+k)
			}
		}
		parts = append(parts, "pinned("+strings.Join(keys, ",")+")")
	}
	if len(parts) == 0 {
		return ""
	}
	return "policy: " + strings.Join(parts, ", ")
}
