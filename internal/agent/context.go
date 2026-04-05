package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// BuildSystemPrompt assembles the system prompt from platform rules, tenant info,
// user context, matching rules, skill catalog, and relevant memories.
func BuildSystemPrompt(ctx context.Context, pool *pgxpool.Pool, tenant *models.Tenant, user *models.User) string {
	var parts []string

	// Platform identity
	parts = append(parts, fmt.Sprintf(`You are Kit, an AI assistant for %s.`, tenant.Name))

	// Communication constraint
	parts = append(parts, `IMPORTANT: You MUST use the send_slack_message tool to respond to the user. Never output a final text response without calling send_slack_message. Every response to the user must go through this tool.

Format messages using Slack mrkdwn (NOT standard markdown). Key differences:
- Bold: *bold* (single asterisks, not double)
- Italic: _italic_ (underscores)
- Strikethrough: ~strikethrough~
- Code: ` + "`code`" + ` (backticks) or ` + "```code block```" + `
- Links: <https://url|link text>
- Lists: use bullet character • or dash - (no markdown-style headers)
- DO NOT use ## headers or **double asterisks** — Slack renders them literally`)

	// Business context
	if tenant.BusinessType != nil && *tenant.BusinessType != "" {
		parts = append(parts, fmt.Sprintf("Business type: %s", *tenant.BusinessType))
	}
	parts = append(parts, fmt.Sprintf("Timezone: %s", tenant.Timezone))

	// User context
	displayName := user.SlackUserID
	if user.DisplayName != nil {
		displayName = *user.DisplayName
	}
	parts = append(parts, fmt.Sprintf("Current user: %s (admin: %v)", displayName, user.IsAdmin))

	// User roles
	roleNames, _ := models.GetUserRoleNames(ctx, pool, tenant.ID, user.ID, tenant.DefaultRoleID)
	if len(roleNames) > 0 {
		parts = append(parts, fmt.Sprintf("User roles: %s", strings.Join(roleNames, ", ")))
	} else {
		parts = append(parts, "User has no assigned roles.")
	}

	// Setup status
	if !tenant.SetupComplete {
		if user.IsAdmin {
			parts = append(parts, platformOnboardingRules())
		} else {
			parts = append(parts, "This organization is still being set up. Let the user know to contact their admin for setup help.")
		}
	}

	// Tenant rules (from DB)
	rules, _ := models.GetRulesForContext(ctx, pool, tenant.ID, roleNames)
	if len(rules) > 0 {
		parts = append(parts, "\n## Rules")
		for _, r := range rules {
			parts = append(parts, fmt.Sprintf("- %s", r.Content))
		}
	}

	// Skill catalog (name + description only, scope-filtered)
	skills, _ := models.GetSkillCatalog(ctx, pool, tenant.ID, roleNames)
	if len(skills) > 0 {
		parts = append(parts, "\n## Available Knowledge (use search_skills or load_skill to access)")
		for _, s := range skills {
			parts = append(parts, fmt.Sprintf("- [%s] %s — %s", s.ID, s.Name, s.Description))
		}
	}

	// Relevant memories
	memories, _ := models.GetRecentMemories(ctx, pool, tenant.ID, user.SlackUserID, roleNames, 5)
	if len(memories) > 0 {
		parts = append(parts, "\n## Remembered Facts")
		for _, m := range memories {
			parts = append(parts, fmt.Sprintf("- %s", m.Content))
		}
	}

	return strings.Join(parts, "\n\n")
}

func platformOnboardingRules() string {
	return `## Onboarding Mode

This organization hasn't completed setup yet. You are talking to the admin who installed Kit. You already know the organization name from the system prompt above — DO NOT ask for it again.

Guide them through setup in this order:

1. Introduce yourself briefly, then ask what type of business they are and their timezone (use update_tenant to save this)
2. Help them define roles for their team (e.g., bartender, manager, board member — use create_role)
3. Help them assign Slack users to roles (ask them to @mention people — extract the user ID from <@U1234567890> format and use assign_role)
4. Prompt them to share any initial knowledge (policies, procedures, FAQs — use create_skill)
5. When they're satisfied, mark setup as complete (use update_tenant with setup_complete=true)

Be direct and efficient. Ask one thing at a time. Start with step 1 immediately.

Use the create_role, assign_role, update_tenant, create_skill, and create_rule tools as needed.`
}
