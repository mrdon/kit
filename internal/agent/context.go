package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// TaskContext provides metadata when the agent is running a scheduled task.
type TaskContext struct {
	ID            uuid.UUID
	Description   string
	AuthorSlackID string
	AuthorName    string

	// Policy, when non-nil, is the task's capability manifest. The agent
	// copies it onto the ExecContext at build time so the registry
	// enforces allow-list / force-gate / pinned_args on every tool call
	// in this run. Nil means "no restrictions" — today's behaviour.
	Policy *models.Policy
}

// BuildSystemPrompt assembles the system prompt from platform rules, tenant info,
// user context, matching rules, skill catalog, and relevant memories.
func BuildSystemPrompt(ctx context.Context, pool *pgxpool.Pool, tenant *models.Tenant, user *models.User, taskCtx *TaskContext) string {
	parts := []string{
		mustRender("system_platform_identity.tmpl", map[string]any{"TenantName": tenant.Name}),
		mustRender("system_slack_output.tmpl", nil),
		mustRender("system_gated_tools.tmpl", nil),
		mustRender("system_require_approval.tmpl", nil),
	}

	// User display info (Slack-specific)
	displayName := user.SlackUserID
	if user.DisplayName != nil {
		displayName = *user.DisplayName
	}
	userTZ := user.Timezone
	if userTZ == "" {
		userTZ = tenant.Timezone
	}

	roleNames, _ := models.GetUserRoleNames(ctx, pool, tenant.ID, user.ID, tenant.DefaultRoleID)
	roleIDs, _ := models.GetUserRoleIDs(ctx, pool, tenant.ID, user.ID, tenant.DefaultRoleID)
	isAdmin := slices.Contains(roleNames, models.RoleAdmin)

	parts = append(parts, fmt.Sprintf("Current user: %s (admin: %v, timezone: %s)", displayName, isAdmin, userTZ))

	// Setup status
	if !tenant.SetupComplete {
		if isAdmin {
			parts = append(parts, mustRender("system_onboarding_rules.tmpl", nil))
		} else {
			parts = append(parts, "This organization is still being set up. Let the user know to contact their admin for setup help.")
		}
	}

	// Shared knowledge context (rules, skills, memories)
	caller := &services.Caller{
		TenantID: tenant.ID,
		UserID:   user.ID,
		Identity: user.SlackUserID,
		Roles:    roleNames,
		RoleIDs:  roleIDs,
		IsAdmin:  isAdmin,
		Timezone: services.ResolveTimezone(user.Timezone, tenant.Timezone),
	}
	parts = append(parts, services.BuildKnowledgeContext(ctx, pool, caller, tenant))

	// Task scheduling guidance (Slack-specific)
	parts = append(parts, mustRender("system_scheduling_guidance.tmpl", nil))

	// App system prompts
	if appPrompts := apps.SystemPrompts(); appPrompts != "" {
		parts = append(parts, appPrompts)
	}

	// Scheduled task context
	if taskCtx != nil {
		parts = append(parts, mustRender("system_task_execution.tmpl", map[string]any{
			"Description":   taskCtx.Description,
			"AuthorName":    taskCtx.AuthorName,
			"AuthorSlackID": taskCtx.AuthorSlackID,
		}))
	}

	return strings.Join(parts, "\n\n")
}
