package agent

import (
	"context"
	"fmt"
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
}

// BuildSystemPrompt assembles the system prompt from platform rules, tenant info,
// user context, matching rules, skill catalog, and relevant memories.
func BuildSystemPrompt(ctx context.Context, pool *pgxpool.Pool, tenant *models.Tenant, user *models.User, taskCtx *TaskContext) string {
	var parts []string

	// Platform identity and behavior
	parts = append(parts, fmt.Sprintf(`You are Kit, an AI assistant for %s.

Communication style:
- Be concise. Short confirmations, not paragraphs. "Created skill *tap-room-policies*." not a 5-line recap.
- Never repeat back what the user just told you. They know what they said.
- Never invent features or capabilities you don't have. Only describe what your tools actually do.
- Don't ask "anything else?" after every response. Just answer and stop.
- Only ask follow-up questions during onboarding or when you genuinely need clarification.`, tenant.Name))

	// Communication constraint (Slack-specific)
	parts = append(parts, `IMPORTANT: You MUST respond to the user via a messaging tool — never output a final text response. Pick the tool that matches the target:

- reply_in_thread(text) — answer the user where they're talking to you (the current thread or DM). Use this for live conversations.
- post_to_channel(channel, text) — post to a named Slack channel. Use this when a task or instruction names a channel (e.g. "#tmp", "the ops channel").
- dm_user(user_id, text) — send a private DM to a specific Slack user id. Use for user-directed notifications that aren't the live conversation.

In task or decision-resolve contexts, reply_in_thread is not available — choose post_to_channel or dm_user. Never leave a run with no tool call.

Format messages using Slack mrkdwn (NOT standard markdown). Key differences:
- Bold: *bold* (single asterisks, not double)
- Italic: _italic_ (underscores)
- Strikethrough: ~strikethrough~
- Code: `+"`code`"+` (backticks) or `+"```code block```"+`
- Links: <https://url|link text>
- Lists: use bullet character • or dash - (no markdown-style headers)
- DO NOT use ## headers or **double asterisks** — Slack renders them literally`)

	// User display info (Slack-specific)
	displayName := user.SlackUserID
	if user.DisplayName != nil {
		displayName = *user.DisplayName
	}
	userTZ := user.Timezone
	if userTZ == "" {
		userTZ = tenant.Timezone
	}
	parts = append(parts, fmt.Sprintf("Current user: %s (admin: %v, timezone: %s)", displayName, user.IsAdmin, userTZ))

	// Setup status
	if !tenant.SetupComplete {
		if user.IsAdmin {
			parts = append(parts, platformOnboardingRules())
		} else {
			parts = append(parts, "This organization is still being set up. Let the user know to contact their admin for setup help.")
		}
	}

	// Shared knowledge context (rules, skills, memories)
	roleNames, _ := models.GetUserRoleNames(ctx, pool, tenant.ID, user.ID, tenant.DefaultRoleID)
	roleIDs, _ := models.GetUserRoleIDs(ctx, pool, tenant.ID, user.ID, tenant.DefaultRoleID)
	caller := &services.Caller{
		TenantID: tenant.ID,
		UserID:   user.ID,
		Identity: user.SlackUserID,
		Roles:    roleNames,
		RoleIDs:  roleIDs,
		IsAdmin:  user.IsAdmin,
		Timezone: services.ResolveTimezone(user.Timezone, tenant.Timezone),
	}
	parts = append(parts, services.BuildKnowledgeContext(ctx, pool, caller, tenant))

	// Task scheduling guidance (Slack-specific)
	parts = append(parts, taskSchedulingGuidance())

	// App system prompts
	if appPrompts := apps.SystemPrompts(); appPrompts != "" {
		parts = append(parts, appPrompts)
	}

	// Scheduled task context
	if taskCtx != nil {
		parts = append(parts, taskExecutionGuidance(taskCtx))
	}

	return strings.Join(parts, "\n\n")
}

func taskSchedulingGuidance() string {
	return `## Scheduled Tasks
You can create recurring or one-time tasks using create_task.

For recurring tasks, use cron_expr:
- Cron format: minute hour day-of-month month day-of-week
- "every morning at 9am" = 0 9 * * *
- "weekdays at 5pm" = 0 17 * * 1-5
- "every Monday at 10am" = 0 10 * * 1

For one-time tasks, use run_at with an ISO 8601 datetime:
- "in 5 minutes" = calculate the time and use run_at
- "at 9:20pm today" = run_at with today's date and 21:20
- "tomorrow at 8am" = run_at with tomorrow's date and 08:00

The run_at time is interpreted in the user's timezone. Use EITHER cron_expr OR run_at, not both.

The task description should be a clear instruction of what to do, as it will be run through the full agent each time. Omit channel_id to use the current channel. For DMs, omit it — the task will run in the same DM.

Tasks run in the user's timezone. Use list_tasks and update_task to manage existing tasks.`
}

func taskExecutionGuidance(tc *TaskContext) string {
	return fmt.Sprintf(`## Scheduled Task Execution
You are executing a scheduled task, not responding to a live conversation.
Task: %s
Created by: %s (<@%s>)

reply_in_thread is not available in this context. Pick the target
explicitly for every message:
- When the task names a channel (e.g. "#tmp"), call post_to_channel with
  that channel and the output text.
- When the task is about notifying the author or a specific user, call
  dm_user with the Slack user id.
- The author's id is %q — use it with dm_user if you need to reach them.`, tc.Description, tc.AuthorName, tc.AuthorSlackID, tc.AuthorSlackID)
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
