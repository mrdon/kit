// Package scheduler: agent_runner.go implements the JobRunner for
// job_type='agent' — the original user-created scheduled job type, where
// job.description is a Slack-style prompt that the LLM agent runs on
// behalf of the job's creator. The body of this file is the same dispatch
// the pre-refactor scheduler did inline; the extraction lets builtin +
// builder_script job_types plug into the same claim loop without branching.
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

// agentRunner handles job_type='agent'. It pins a reference to the parent
// Scheduler rather than re-passing pool/enc/agent everywhere so the
// pre-refactor call sites move across verbatim.
type agentRunner struct{ s *Scheduler }

func (r *agentRunner) JobType() string { return string(models.JobTypeAgent) }

func (r *agentRunner) Run(ctx context.Context, job *models.Job) error {
	r.s.executeAgentTask(ctx, *job)
	return nil
}

// executeAgentTask runs an agent job. Kept as a Scheduler method because
// it references s.pool/s.enc/s.agent and the old implementation lived in
// scheduler.go under the same receiver — minimal blast radius.
func (s *Scheduler) executeAgentTask(ctx context.Context, job models.Job) {
	slog.Info("executing scheduled job", "job_id", job.ID, "job_type", job.JobType, "description", job.Description)

	tenant, err := models.GetTenantByID(ctx, s.pool, job.TenantID)
	if err != nil || tenant == nil {
		slog.Error("looking up tenant for job", "job_id", job.ID, "error", err)
		s.recordAgentTaskError(ctx, job, "looking up tenant", nil, nil)
		return
	}

	botToken, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token for job", "job_id", job.ID, "error", err)
		s.recordAgentTaskError(ctx, job, "decrypting bot token", nil, nil)
		return
	}
	slack := kitslack.NewClient(botToken)

	user, err := models.GetUserByID(ctx, s.pool, tenant.ID, job.CreatedBy)
	if err != nil || user == nil {
		slog.Error("looking up user for job", "job_id", job.ID, "error", err)
		s.recordAgentTaskError(ctx, job, "looking up user", slack, nil)
		return
	}

	// Session selection: resume an existing session when ResolveDecision
	// flagged this job (resume_session_id set), otherwise mint fresh.
	// The resume marker is consumed — a subsequent cron tick gets a fresh
	// session, not the previous workflow's context.
	var session *models.Session
	isResume := false
	if job.ResumeSessionID != nil {
		existing, err := models.GetSession(ctx, s.pool, tenant.ID, *job.ResumeSessionID)
		if err == nil && existing != nil {
			session = existing
			isResume = true
		} else if err != nil {
			slog.Warn("loading resume session for job, falling back to fresh", "job_id", job.ID, "error", err)
		}
		if err := models.ClearTaskResumeSession(ctx, s.pool, tenant.ID, job.ID); err != nil {
			slog.Warn("clearing resume_session_id", "job_id", job.ID, "error", err)
		}
	}
	if session == nil {
		threadTS := fmt.Sprintf("job-%s-%d", job.ID, time.Now().UnixMilli())
		created, err := models.CreateSession(ctx, s.pool, tenant.ID, job.ChannelID, threadTS, user.ID, true)
		if err != nil {
			slog.Error("creating session for job", "job_id", job.ID, "error", err)
			s.recordAgentTaskError(ctx, job, "creating session", slack, user)
			return
		}
		session = created
	}

	authorName := user.SlackUserID
	if user.DisplayName != nil && *user.DisplayName != "" {
		authorName = *user.DisplayName
	}
	policy, err := models.ParseConfigPolicy(job.Config)
	if err != nil {
		slog.Error("parsing job policy", "job_id", job.ID, "error", err)
		s.recordAgentTaskError(ctx, job, "parsing job policy", slack, user)
		return
	}
	tc := &agent.JobContext{
		ID:            job.ID,
		Description:   job.Description,
		AuthorSlackID: user.SlackUserID,
		AuthorName:    authorName,
		Policy:        policy,
	}

	// On resume the full context is in session history (original prompt,
	// prior tool calls, the decision_resolved event). The fresh user
	// message only needs to nudge the agent to re-evaluate.
	userText := job.Description
	if job.SkillID != nil {
		skill, serr := models.GetSkill(ctx, s.pool, tenant.ID, *job.SkillID)
		if serr != nil || skill == nil {
			msg := fmt.Sprintf("loading skill %s", job.SkillID)
			slog.Error("loading skill for job", "job_id", job.ID, "skill_id", *job.SkillID, "error", serr)
			s.recordAgentTaskError(ctx, job, msg, slack, user)
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
		Channel:  job.ChannelID,
		UserText: userText,
		Job:      tc,
		Model:    job.Model,
	}); err != nil {
		slog.Error("job agent run failed", "job_id", job.ID, "error", err)
		errStr := err.Error()
		lastError = &errStr
		dmCh, dmErr := slack.OpenConversation(ctx, user.SlackUserID)
		if dmErr == nil {
			_ = slack.PostMessage(ctx, dmCh, "",
				fmt.Sprintf("⚠️ Scheduled job failed: _%s_\nError: %s", job.Description, errStr))
		}
	}

	if job.RunOnce {
		if err := models.CompleteTask(ctx, s.pool, job.TenantID, job.ID, lastError); err != nil {
			slog.Error("completing one-time job", "job_id", job.ID, "error", err)
		}
		slog.Info("one-time job completed", "job_id", job.ID)
		return
	}

	nextRun, err := models.NextCronRun(job.CronExpr, job.Timezone, time.Now())
	if err != nil {
		slog.Error("computing next run", "job_id", job.ID, "error", err)
		return
	}

	if err := models.UpdateJobAfterRun(ctx, s.pool, job.TenantID, job.ID, nextRun, lastError); err != nil {
		slog.Error("updating job after run", "job_id", job.ID, "error", err)
	}

	slog.Info("job completed", "job_id", job.ID, "next_run", nextRun)
}

// recordAgentTaskError handles the error-path bookkeeping for an agent
// job: DM the creator if we have slack+user, mark the row completed (or
// advance next_run_at) so the scheduler doesn't spin on a broken row.
func (s *Scheduler) recordAgentTaskError(ctx context.Context, job models.Job, msg string, slack *kitslack.Client, user *models.User) {
	if slack != nil {
		errText := fmt.Sprintf("⚠️ Scheduled job failed: _%s_\nError: %s", job.Description, msg)
		if user != nil {
			if dmCh, err := slack.OpenConversation(ctx, user.SlackUserID); err == nil {
				_ = slack.PostMessage(ctx, dmCh, "", errText)
			}
		} else {
			_ = slack.PostMessage(ctx, job.ChannelID, "", errText)
		}
	}
	if job.RunOnce {
		_ = models.CompleteTask(ctx, s.pool, job.TenantID, job.ID, &msg)
		return
	}
	nextRun, err := models.NextCronRun(job.CronExpr, job.Timezone, time.Now())
	if err != nil {
		slog.Error("computing next run for error recording", "job_id", job.ID, "error", err)
		return
	}
	_ = models.UpdateJobAfterRun(ctx, s.pool, job.TenantID, job.ID, nextRun, &msg)
}
