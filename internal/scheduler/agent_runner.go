// Package scheduler: agent_runner.go implements the TaskRunner for
// task_type='agent' — the original user-created scheduled task type, where
// task.description is a Slack-style prompt that the LLM agent runs on
// behalf of the task's creator. The body of this file is the same dispatch
// the pre-refactor scheduler did inline; the extraction lets builtin +
// builder_script task_types plug into the same claim loop without branching.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// agentRunner handles task_type='agent'. It pins a reference to the parent
// Scheduler rather than re-passing pool/enc/agent everywhere so the
// pre-refactor call sites move across verbatim.
type agentRunner struct{ s *Scheduler }

func (r *agentRunner) TaskType() string { return string(models.TaskTypeAgent) }

func (r *agentRunner) Run(ctx context.Context, task *models.Task) error {
	r.s.executeAgentTask(ctx, *task)
	return nil
}

// executeAgentTask runs an agent task. Kept as a Scheduler method because
// it references s.pool/s.enc/s.agent and the old implementation lived in
// scheduler.go under the same receiver — minimal blast radius.
func (s *Scheduler) executeAgentTask(ctx context.Context, task models.Task) {
	slog.Info("executing scheduled task", "task_id", task.ID, "task_type", task.TaskType, "description", task.Description)

	tenant, err := models.GetTenantByID(ctx, s.pool, task.TenantID)
	if err != nil || tenant == nil {
		slog.Error("looking up tenant for task", "task_id", task.ID, "error", err)
		s.recordAgentTaskError(ctx, task, "looking up tenant", nil, nil)
		return
	}

	botToken, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token for task", "task_id", task.ID, "error", err)
		s.recordAgentTaskError(ctx, task, "decrypting bot token", nil, nil)
		return
	}
	slack := kitslack.NewClient(botToken)

	user, err := models.GetUserByID(ctx, s.pool, tenant.ID, task.CreatedBy)
	if err != nil || user == nil {
		slog.Error("looking up user for task", "task_id", task.ID, "error", err)
		s.recordAgentTaskError(ctx, task, "looking up user", slack, nil)
		return
	}

	// Session selection: resume an existing session when ResolveDecision
	// flagged this task (resume_session_id set), otherwise mint fresh.
	// The resume marker is consumed — a subsequent cron tick gets a fresh
	// session, not the previous workflow's context.
	var session *models.Session
	isResume := false
	if task.ResumeSessionID != nil {
		existing, err := models.GetSession(ctx, s.pool, tenant.ID, *task.ResumeSessionID)
		if err == nil && existing != nil {
			session = existing
			isResume = true
		} else if err != nil {
			slog.Warn("loading resume session for task, falling back to fresh", "task_id", task.ID, "error", err)
		}
		if err := models.ClearTaskResumeSession(ctx, s.pool, tenant.ID, task.ID); err != nil {
			slog.Warn("clearing resume_session_id", "task_id", task.ID, "error", err)
		}
	}
	if session == nil {
		threadTS := fmt.Sprintf("task-%s-%d", task.ID, time.Now().UnixMilli())
		created, err := models.CreateSession(ctx, s.pool, tenant.ID, task.ChannelID, threadTS, user.ID, true)
		if err != nil {
			slog.Error("creating session for task", "task_id", task.ID, "error", err)
			s.recordAgentTaskError(ctx, task, "creating session", slack, user)
			return
		}
		session = created
	}

	authorName := user.SlackUserID
	if user.DisplayName != nil && *user.DisplayName != "" {
		authorName = *user.DisplayName
	}
	policy, err := models.ParseConfigPolicy(task.Config)
	if err != nil {
		slog.Error("parsing task policy", "task_id", task.ID, "error", err)
		s.recordAgentTaskError(ctx, task, "parsing task policy", slack, user)
		return
	}
	tc := &agent.TaskContext{
		ID:            task.ID,
		Description:   task.Description,
		AuthorSlackID: user.SlackUserID,
		AuthorName:    authorName,
		Policy:        policy,
	}

	// On resume the full context is in session history (original prompt,
	// prior tool calls, the decision_resolved event). The fresh user
	// message only needs to nudge the agent to re-evaluate.
	userText := task.Description
	if task.SkillID != nil {
		skill, serr := models.GetSkill(ctx, s.pool, tenant.ID, *task.SkillID)
		if serr != nil || skill == nil {
			msg := fmt.Sprintf("loading skill %s", task.SkillID)
			slog.Error("loading skill for task", "task_id", task.ID, "skill_id", *task.SkillID, "error", serr)
			s.recordAgentTaskError(ctx, task, msg, slack, user)
			return
		}
		userText = fmt.Sprintf(
			"Load the skill named %q (call load_skill with skill_id=%q) and follow its instructions.",
			skill.Name, skill.Name,
		)
	}
	if isResume {
		userText = "A decision you created has been resolved. Review the updated state and continue the workflow — either create any remaining decisions, produce your final output, or wait if other decisions are still pending."
	}

	var lastError *string
	if err := s.agent.Run(ctx, agent.RunInput{
		Slack:    slack,
		Tenant:   tenant,
		User:     user,
		Session:  session,
		Channel:  task.ChannelID,
		UserText: userText,
		Task:     tc,
		Model:    task.Model,
	}); err != nil {
		slog.Error("task agent run failed", "task_id", task.ID, "error", err)
		errStr := err.Error()
		lastError = &errStr
		dmCh, dmErr := slack.OpenConversation(ctx, user.SlackUserID)
		if dmErr == nil {
			_ = slack.PostMessage(ctx, dmCh, "",
				fmt.Sprintf("⚠️ Scheduled task failed: _%s_\nError: %s", task.Description, errStr))
		}
	}

	if task.RunOnce {
		if err := models.CompleteTask(ctx, s.pool, task.TenantID, task.ID, lastError); err != nil {
			slog.Error("completing one-time task", "task_id", task.ID, "error", err)
		}
		slog.Info("one-time task completed", "task_id", task.ID)
		return
	}

	nextRun, err := models.NextCronRun(task.CronExpr, task.Timezone, time.Now())
	if err != nil {
		slog.Error("computing next run", "task_id", task.ID, "error", err)
		return
	}

	if err := models.UpdateTaskAfterRun(ctx, s.pool, task.TenantID, task.ID, nextRun, lastError); err != nil {
		slog.Error("updating task after run", "task_id", task.ID, "error", err)
	}

	slog.Info("task completed", "task_id", task.ID, "next_run", nextRun)
}

// recordAgentTaskError handles the error-path bookkeeping for an agent
// task: DM the creator if we have slack+user, mark the row completed (or
// advance next_run_at) so the scheduler doesn't spin on a broken row.
func (s *Scheduler) recordAgentTaskError(ctx context.Context, task models.Task, msg string, slack *kitslack.Client, user *models.User) {
	if slack != nil {
		errText := fmt.Sprintf("⚠️ Scheduled task failed: _%s_\nError: %s", task.Description, msg)
		if user != nil {
			if dmCh, err := slack.OpenConversation(ctx, user.SlackUserID); err == nil {
				_ = slack.PostMessage(ctx, dmCh, "", errText)
			}
		} else {
			_ = slack.PostMessage(ctx, task.ChannelID, "", errText)
		}
	}
	if task.RunOnce {
		_ = models.CompleteTask(ctx, s.pool, task.TenantID, task.ID, &msg)
		return
	}
	nextRun, err := models.NextCronRun(task.CronExpr, task.Timezone, time.Now())
	if err != nil {
		slog.Error("computing next run for error recording", "task_id", task.ID, "error", err)
		return
	}
	_ = models.UpdateTaskAfterRun(ctx, s.pool, task.TenantID, task.ID, nextRun, &msg)
}
