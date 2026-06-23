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

// NewJobService constructs a JobService. Mirrors NewRoleService so
// surfaces outside the services package (e.g. the web console) can build
// one without reaching the unexported pool field.
func NewJobService(pool *pgxpool.Pool) *JobService {
	return &JobService{pool: pool}
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
		skill, serr := models.GetSkillByName(ctx, s.pool, c.TenantID, in.SkillName, false)
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

// List returns jobs the caller may manage. Admins are superusers: they see
// every job in the tenant (CRUD over a job's config leaks none of the
// owner's private data — that lives in run-time identity and session
// traces, which stay scoped elsewhere). Non-admins see only jobs in their
// scope: their own personal jobs, role jobs for roles they hold, and
// tenant-wide jobs.
func (s *JobService) List(ctx context.Context, c *Caller) ([]models.Job, error) {
	if c.IsAdmin {
		return models.ListAllJobs(ctx, s.pool, c.TenantID)
	}
	return models.ListJobsForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
}

// canManage reports whether the caller may view/edit/delete the given job,
// returning the job when allowed. This is the single authorization gate for
// per-job operations: admins are authorized against the whole tenant;
// non-admins must have the job in their visible scope. Returns ErrNotFound
// when the job doesn't exist (in tenant) or isn't visible to the caller —
// the two are deliberately indistinguishable so a non-admin can't probe for
// the existence of other users' jobs.
func (s *JobService) canManage(ctx context.Context, c *Caller, jobID uuid.UUID) (*models.Job, error) {
	if c.IsAdmin {
		job, err := models.GetJob(ctx, s.pool, c.TenantID, jobID)
		if err != nil {
			return nil, fmt.Errorf("getting job: %w", err)
		}
		if job == nil {
			return nil, ErrNotFound
		}
		return job, nil
	}
	visible, err := models.ListJobsForContext(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs)
	if err != nil {
		return nil, fmt.Errorf("listing visible jobs: %w", err)
	}
	for i := range visible {
		if visible[i].ID == jobID {
			return &visible[i], nil
		}
	}
	return nil, ErrNotFound
}

// Get returns a single job the caller may manage, enriched for display.
func (s *JobService) Get(ctx context.Context, c *Caller, jobID uuid.UUID) (*JobView, error) {
	job, err := s.canManage(ctx, c, jobID)
	if err != nil {
		return nil, err
	}
	views, err := s.enrich(ctx, c.TenantID, []models.Job{*job})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// ListViews returns the caller's manageable jobs enriched for display.
func (s *JobService) ListViews(ctx context.Context, c *Caller) ([]JobView, error) {
	jobs, err := s.List(ctx, c)
	if err != nil {
		return nil, err
	}
	return s.enrich(ctx, c.TenantID, jobs)
}

// JobView is the display-ready projection of a job for the web console: the
// raw fields plus a resolved skill name, a human schedule label, the parsed
// policy (for editing) and its compact summary (for lists). Built in the
// service so every surface renders jobs identically.
type JobView struct {
	ID            uuid.UUID        `json:"id"`
	Description   string           `json:"description"`
	JobType       models.JobType   `json:"job_type"`
	Status        models.JobStatus `json:"status"`
	Schedule      string           `json:"schedule"`
	CronExpr      string           `json:"cron_expr"`
	RunOnce       bool             `json:"run_once"`
	Timezone      string           `json:"timezone"`
	ChannelID     string           `json:"channel_id"`
	NextRunAt     time.Time        `json:"next_run_at"`
	LastRunAt     *time.Time       `json:"last_run_at"`
	LastError     *string          `json:"last_error"`
	Model         string           `json:"model"`
	SkillID       *uuid.UUID       `json:"skill_id"`
	SkillName     string           `json:"skill_name"`
	Policy        *models.Policy   `json:"policy"`
	PolicySummary string           `json:"policy_summary"`
	Editable      bool             `json:"editable"`
	CreatedAt     time.Time        `json:"created_at"`
}

// enrich projects jobs into JobViews, resolving each linked skill's current
// slug name. Builtin jobs are marked non-editable (the model layer refuses
// to mutate them).
func (s *JobService) enrich(ctx context.Context, tenantID uuid.UUID, jobs []models.Job) ([]JobView, error) {
	views := make([]JobView, 0, len(jobs))
	for i := range jobs {
		j := jobs[i]
		v := JobView{
			ID:            j.ID,
			Description:   j.Description,
			JobType:       j.JobType,
			Status:        j.Status,
			Schedule:      scheduleLabel(j),
			CronExpr:      j.CronExpr,
			RunOnce:       j.RunOnce,
			Timezone:      j.Timezone,
			ChannelID:     j.ChannelID,
			NextRunAt:     j.NextRunAt,
			LastRunAt:     j.LastRunAt,
			LastError:     j.LastError,
			Model:         j.Model,
			SkillID:       j.SkillID,
			PolicySummary: FormatTaskPolicySummary(j.Config),
			Editable:      j.JobType != models.JobTypeBuiltin,
			CreatedAt:     j.CreatedAt,
		}
		if policy, err := models.ParseConfigPolicy(j.Config); err == nil {
			v.Policy = policy
		}
		if j.SkillID != nil {
			skill, err := models.GetSkill(ctx, s.pool, tenantID, *j.SkillID, false)
			if err != nil {
				return nil, fmt.Errorf("resolving job skill: %w", err)
			}
			if skill != nil {
				v.SkillName = skill.Name
			}
		}
		views = append(views, v)
	}
	return views, nil
}

// scheduleLabel renders a job's cadence for display: a one-time job shows
// its run time, a recurring job shows its cron expression.
func scheduleLabel(j models.Job) string {
	if j.RunOnce {
		return "once: " + j.NextRunAt.Format("Mon Jan 2 3:04 PM")
	}
	return "cron: " + j.CronExpr
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

// Update updates a job's description, linked skill and/or policy. The
// caller must be allowed to manage the job (canManage): admins tenant-wide,
// non-admins only within their visible scope. Fields with nil pointers are
// left untouched. A non-nil Policy replaces the job's policy wholesale.
func (s *JobService) Update(ctx context.Context, c *Caller, jobID uuid.UUID, in UpdateInput) error {
	if _, err := s.canManage(ctx, c, jobID); err != nil {
		return err
	}
	if in.Description != nil {
		if err := models.UpdateJobDescription(ctx, s.pool, c.TenantID, jobID, *in.Description); err != nil {
			return err
		}
	}
	if in.SkillName != nil {
		var skillID *uuid.UUID
		if *in.SkillName != "" {
			skill, serr := models.GetSkillByName(ctx, s.pool, c.TenantID, *in.SkillName, false)
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

// Delete deletes a job the caller is allowed to manage (canManage): admins
// tenant-wide, non-admins only within their visible scope.
func (s *JobService) Delete(ctx context.Context, c *Caller, jobID uuid.UUID) error {
	if _, err := s.canManage(ctx, c, jobID); err != nil {
		return err
	}
	return models.DeleteJob(ctx, s.pool, c.TenantID, jobID)
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
