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

// JobTools defines the shared tool metadata for job operations.
var JobTools = []ToolMeta{
	{Name: "create_job", Description: "Schedule a recurring or one-time job. Kit runs the description through the full agent at the scheduled time. To run a specific skill instead of a free-form prompt, pass skill_name — the scheduled agent will load and execute that skill. For non-trivial jobs — especially those needing argument pinning, tool allow-lists, or forced approval gates — load the `creating-jobs` builtin skill before calling.", Schema: propsReq(map[string]any{
		"description": field("string", "Short human-readable label for the job. When skill_name is omitted, this text is also the agent prompt."),
		"skill_name":  field("string", "Slug name of the skill to load and execute at fire time (e.g. 'daily-standup'). Omit to run description as a free-form prompt."),
		"cron_expr":   field("string", "Cron expression for recurring jobs: minute hour day-of-month month day-of-week"),
		"run_at":      field("string", "ISO 8601 datetime for one-time jobs (e.g. '2026-04-05T21:20:00'). Use this OR cron_expr, not both."),
		"channel_id":  field("string", "Slack channel ID where output should be posted"),
		"scope":       field("string", "Scope: 'user' (default), 'tenant' (admin only), or a role name"),
		"policy":      policyField(),
	}, "description")},
	{Name: "list_jobs", Description: "List scheduled jobs visible to the current user.", Schema: props(map[string]any{})},
	{Name: "update_job", Description: "Update or delete a scheduled job. Provide description to change it, skill_name to change which skill runs (empty string to clear and fall back to the description prompt), policy to replace its capability manifest, or set delete=true to remove the job. See the `creating-jobs` skill for policy shape.", Schema: propsReq(map[string]any{
		"id":          field("string", "The job UUID"),
		"description": field("string", "New job description (optional)"),
		"skill_name":  field("string", "New skill slug to run, or empty string to clear (optional)"),
		"policy":      policyField(),
		"delete":      field("boolean", "Set to true to delete the job (optional)"),
	}, "id")},
}

// policyField returns the JSON-schema fragment describing the optional
// job policy object. Kept as a nested object schema so MCP clients and
// the agent's tool catalogue see the field exists, but the full
// design guidance (when to allow-list vs force-gate vs pin) lives in
// the `creating-jobs` builtin skill to avoid bloating every agent
// turn's system prompt.
func policyField() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Optional capability manifest constraining the scheduled agent. See the `creating-jobs` skill for full design guidance, examples, and gotchas.",
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

// JobService handles job operations with authorization.
type JobService struct {
	pool *pgxpool.Pool
}

// CreateInput bundles the arguments for JobService.Create. Policy is
// optional; nil means "no capability restrictions," matching today's
// behaviour for every job that predates the policy feature.
type CreateInput struct {
	Description string
	CronExpr    string
	Timezone    string
	ChannelID   string
	Scope       string
	Model       string
	// SkillName, when set, makes the scheduler load this skill (by its
	// per-tenant unique slug name) and execute it instead of running
	// Description as a free-form prompt. Description is still required
	// as the human-readable label.
	SkillName string
	RunOnce   bool
	RunAt     *time.Time
	Policy    *models.Policy
}

// Create creates a scheduled job with scope resolution.
// in.Scope: "user" (default), "tenant" (admin only), or a role name.
// in.Model is the tier name ("haiku" | "sonnet") picked by the
// classifier in the tool layer; empty defaults to Haiku at the DB level.
func (s *JobService) Create(ctx context.Context, c *Caller, in CreateInput) (*models.Job, error) {
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
	var skillID *uuid.UUID
	if in.SkillName != "" {
		skill, serr := models.GetSkillByName(ctx, s.pool, c.TenantID, in.SkillName)
		if serr != nil {
			return nil, fmt.Errorf("looking up skill %q: %w", in.SkillName, serr)
		}
		if skill == nil {
			return nil, ErrNotFound
		}
		id := skill.ID
		skillID = &id
	}
	job, err := models.CreateJob(ctx, s.pool, c.TenantID, c.UserID, in.Description, in.CronExpr, in.Timezone, in.ChannelID, in.Model, skillID, in.RunOnce, in.RunAt, roleID, userID)
	if err != nil {
		return nil, err
	}
	if in.Policy != nil {
		if err := models.UpdateJobPolicy(ctx, s.pool, c.TenantID, job.ID, in.Policy); err != nil {
			return nil, fmt.Errorf("writing job policy: %w", err)
		}
		job.Config, _ = models.SetConfigPolicy(job.Config, in.Policy)
	}
	return job, nil
}

// List returns jobs visible to the caller through normal scope filtering:
// jobs scoped to them personally, to roles they hold, or to the tenant.
// Admins are not granted extra visibility into other users' personal jobs —
// those run with the creator's identity (email, memories) and the admin is
// not that user.
func (s *JobService) List(ctx context.Context, c *Caller) ([]models.Job, error) {
	return models.ListJobsForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
}

// UpdateInput bundles the optional fields a job update can change. Nil
// means "don't touch." Policy is replace-wholesale — a non-nil pointer
// overwrites the job's policy; callers that want to tweak a single
// sub-field must read the current policy and re-write the full shape.
// SkillName follows the same nil-means-no-change rule; a non-nil pointer
// to the empty string clears skill_id (job falls back to
// description-as-prompt), any other value re-resolves to a skill ID.
type UpdateInput struct {
	Description *string
	SkillName   *string
	Policy      *models.Policy
}

// Update updates a job's description and/or policy. The caller must
// have the job in their visible scope; admins don't bypass this for
// other users' personal jobs. Fields with nil pointers are left
// untouched. A non-nil Policy replaces the job's policy wholesale.
func (s *JobService) Update(ctx context.Context, c *Caller, jobID uuid.UUID, in UpdateInput) error {
	visible, err := models.ListJobsForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
	if err != nil {
		return fmt.Errorf("listing visible jobs: %w", err)
	}
	found := false
	for _, t := range visible {
		if t.ID == jobID {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	if in.Description != nil {
		if err := models.UpdateJobDescription(ctx, s.pool, c.TenantID, jobID, *in.Description); err != nil {
			return err
		}
	}
	if in.SkillName != nil {
		var skillID *uuid.UUID
		if *in.SkillName != "" {
			skill, serr := models.GetSkillByName(ctx, s.pool, c.TenantID, *in.SkillName)
			if serr != nil {
				return fmt.Errorf("looking up skill %q: %w", *in.SkillName, serr)
			}
			if skill == nil {
				return ErrNotFound
			}
			id := skill.ID
			skillID = &id
		}
		if err := models.UpdateJobSkillID(ctx, s.pool, c.TenantID, jobID, skillID); err != nil {
			return err
		}
	}
	if in.Policy != nil {
		if err := models.UpdateJobPolicy(ctx, s.pool, c.TenantID, jobID, in.Policy); err != nil {
			return err
		}
	}
	return nil
}

// Delete deletes a job in the caller's visible scope. Admins don't bypass
// this for other users' personal jobs.
func (s *JobService) Delete(ctx context.Context, c *Caller, jobID uuid.UUID) error {
	visible, err := models.ListJobsForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
	if err != nil {
		return fmt.Errorf("listing visible jobs: %w", err)
	}
	for _, t := range visible {
		if t.ID == jobID {
			return models.DeleteJob(ctx, s.pool, c.TenantID, jobID)
		}
	}
	return ErrNotFound
}

// FormatTaskPolicySummary renders a compact description of a job's
// policy (as persisted in job.config JSONB) for list_tasks output,
// e.g. "policy: allow-list(4), force-gate(post_to_channel), pinned(channel)".
// Returns "" when the job has no policy. Lives here so both agent-side
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
