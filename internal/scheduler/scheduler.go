package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

const maxConcurrentTasks = 5

// Scheduler runs due tasks and syncs user profiles on a schedule.
type Scheduler struct {
	pool  *pgxpool.Pool
	enc   *crypto.Encryptor
	agent *agent.Agent
}

// New creates a new Scheduler.
func New(pool *pgxpool.Pool, enc *crypto.Encryptor, a *agent.Agent) *Scheduler {
	return &Scheduler{pool: pool, enc: enc, agent: a}
}

// Start launches the task runner. Builtin tasks (like profile sync) are ensured
// on startup and run via the same task loop as user-created tasks.
func (s *Scheduler) Start(ctx context.Context) {
	s.ensureBuiltinTasks(ctx)
	go s.runTaskLoop(ctx)
}

func (s *Scheduler) runTaskLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	s.processDueTasks(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processDueTasks(ctx)
		}
	}
}

func (s *Scheduler) processDueTasks(ctx context.Context) {
	tasks, err := models.GetDueTasks(ctx, s.pool)
	if err != nil {
		slog.Error("fetching due tasks", "error", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentTasks)

	for _, task := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.executeTask(ctx, task)
		}()
	}

	wg.Wait()
}

func (s *Scheduler) executeTask(ctx context.Context, task models.Task) {
	slog.Info("executing scheduled task", "task_id", task.ID, "task_type", task.TaskType, "description", task.Description)

	// Route builtin tasks to native handlers
	if task.TaskType == "builtin" {
		s.ExecuteBuiltinTask(ctx, task)
		return
	}

	tenant, err := models.GetTenantByID(ctx, s.pool, task.TenantID)
	if err != nil || tenant == nil {
		slog.Error("looking up tenant for task", "task_id", task.ID, "error", err)
		s.recordTaskError(ctx, task, "looking up tenant", nil, nil)
		return
	}

	botToken, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token for task", "task_id", task.ID, "error", err)
		s.recordTaskError(ctx, task, "decrypting bot token", nil, nil)
		return
	}
	slack := kitslack.NewClient(botToken)

	user, err := models.GetUserByID(ctx, s.pool, tenant.ID, task.CreatedBy)
	if err != nil || user == nil {
		slog.Error("looking up user for task", "task_id", task.ID, "error", err)
		s.recordTaskError(ctx, task, "looking up user", slack, nil)
		return
	}

	// Each task run gets its own session using the task ID + timestamp as thread_ts
	threadTS := fmt.Sprintf("task-%s-%d", task.ID, time.Now().UnixMilli())
	session, err := models.CreateSession(ctx, s.pool, tenant.ID, task.ChannelID, threadTS, user.ID)
	if err != nil {
		slog.Error("creating session for task", "task_id", task.ID, "error", err)
		s.recordTaskError(ctx, task, "creating session", slack, user)
		return
	}

	authorName := user.SlackUserID
	if user.DisplayName != nil && *user.DisplayName != "" {
		authorName = *user.DisplayName
	}
	tc := &agent.TaskContext{
		Description:   task.Description,
		AuthorSlackID: user.SlackUserID,
		AuthorName:    authorName,
	}

	var lastError *string
	if err := s.agent.Run(ctx, slack, tenant, user, session, task.ChannelID, "", task.Description, tc); err != nil {
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

func (s *Scheduler) recordTaskError(ctx context.Context, task models.Task, msg string, slack *kitslack.Client, user *models.User) {
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

func (s *Scheduler) syncTenantProfiles(ctx context.Context, tenant models.Tenant) {
	slog.Info("syncing profiles", "tenant_id", tenant.ID, "tenant_name", tenant.Name, "slack_team_id", tenant.SlackTeamID)

	botToken, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token for sync", "tenant_id", tenant.ID, "tenant_name", tenant.Name, "error", err)
		return
	}
	slack := kitslack.NewClient(botToken)

	// Verify the token belongs to the expected workspace
	actualTeamID, botUserID, err := slack.AuthTest(ctx)
	if err != nil {
		slog.Error("auth.test failed for sync", "tenant_id", tenant.ID, "tenant_name", tenant.Name, "error", err)
		return
	}
	if actualTeamID != tenant.SlackTeamID {
		slog.Error("bot token team mismatch",
			"tenant_id", tenant.ID, "tenant_name", tenant.Name,
			"expected_team", tenant.SlackTeamID, "actual_team", actualTeamID)
		return
	}
	slog.Info("token verified", "tenant_id", tenant.ID, "tenant_name", tenant.Name, "bot_user_id", botUserID)

	// Fetch all workspace members in bulk (1-2 API calls vs N per-user calls)
	slackUsers, err := slack.ListAllUsers(ctx)
	if err != nil {
		slog.Error("listing slack users", "tenant_id", tenant.ID, "tenant_name", tenant.Name, "error", err)
		return
	}

	// Index by Slack user ID for fast lookup
	slackByID := make(map[string]*kitslack.UserInfo, len(slackUsers))
	for i := range slackUsers {
		slackByID[slackUsers[i].SlackUserID] = &slackUsers[i]
	}

	dbUsers, err := models.ListUsersByTenant(ctx, s.pool, tenant.ID)
	if err != nil {
		slog.Error("listing db users for sync", "tenant_id", tenant.ID, "error", err)
		return
	}

	slog.Info("syncing user profiles", "tenant_id", tenant.ID, "tenant_name", tenant.Name,
		"slack_users", len(slackUsers), "db_users", len(dbUsers))

	var synced, skipped int
	for _, user := range dbUsers {
		info, ok := slackByID[user.SlackUserID]
		if !ok {
			skipped++
			continue
		}
		if err := models.UpdateUserProfile(ctx, s.pool, tenant.ID, user.ID, info.DisplayName, info.Timezone); err != nil {
			slog.Warn("updating user profile", "user_id", user.ID, "error", err)
			continue
		}
		synced++
	}

	slog.Info("profile sync complete", "tenant_id", tenant.ID, "tenant_name", tenant.Name,
		"synced", synced, "skipped", skipped)
}
