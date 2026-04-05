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

// Start launches the task runner and nightly profile sync goroutines.
func (s *Scheduler) Start(ctx context.Context) {
	go s.runTaskLoop(ctx)
	go s.runProfileSync(ctx)
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
	slog.Info("executing scheduled task", "task_id", task.ID, "description", task.Description)

	tenant, err := models.GetTenantByID(ctx, s.pool, task.TenantID)
	if err != nil || tenant == nil {
		slog.Error("looking up tenant for task", "task_id", task.ID, "error", err)
		s.recordTaskError(ctx, task, "looking up tenant")
		return
	}

	botToken, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token for task", "task_id", task.ID, "error", err)
		s.recordTaskError(ctx, task, "decrypting bot token")
		return
	}
	slack := kitslack.NewClient(botToken)

	user, err := models.GetUserByID(ctx, s.pool, tenant.ID, task.CreatedBy)
	if err != nil || user == nil {
		slog.Error("looking up user for task", "task_id", task.ID, "error", err)
		s.recordTaskError(ctx, task, "looking up user")
		return
	}

	// Each task run gets its own session using the task ID + timestamp as thread_ts
	threadTS := fmt.Sprintf("task-%s-%d", task.ID, time.Now().UnixMilli())
	session, err := models.CreateSession(ctx, s.pool, tenant.ID, task.ChannelID, threadTS, user.ID)
	if err != nil {
		slog.Error("creating session for task", "task_id", task.ID, "error", err)
		s.recordTaskError(ctx, task, "creating session")
		return
	}

	var lastError *string
	if err := s.agent.Run(ctx, slack, tenant, user, session, task.ChannelID, "", task.Description); err != nil {
		slog.Error("task agent run failed", "task_id", task.ID, "error", err)
		errStr := err.Error()
		lastError = &errStr
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

func (s *Scheduler) recordTaskError(ctx context.Context, task models.Task, msg string) {
	nextRun, err := models.NextCronRun(task.CronExpr, task.Timezone, time.Now())
	if err != nil {
		slog.Error("computing next run for error recording", "task_id", task.ID, "error", err)
		return
	}
	_ = models.UpdateTaskAfterRun(ctx, s.pool, task.TenantID, task.ID, nextRun, &msg)
}

func (s *Scheduler) runProfileSync(ctx context.Context) {
	now := time.Now().UTC()
	next3am := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	if now.After(next3am) {
		next3am = next3am.Add(24 * time.Hour)
	}

	timer := time.NewTimer(time.Until(next3am))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.syncAllProfiles(ctx)
			// Recalculate next 3am to avoid drift
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 3, 0, 0, 0, time.UTC)
			timer.Reset(time.Until(next))
		}
	}
}

func (s *Scheduler) syncAllProfiles(ctx context.Context) {
	slog.Info("starting nightly profile sync")

	tenants, err := models.ListAllTenants(ctx, s.pool)
	if err != nil {
		slog.Error("listing tenants for profile sync", "error", err)
		return
	}

	for _, tenant := range tenants {
		s.syncTenantProfiles(ctx, tenant)
	}

	slog.Info("nightly profile sync complete")
}

func (s *Scheduler) syncTenantProfiles(ctx context.Context, tenant models.Tenant) {
	botToken, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token for sync", "tenant_id", tenant.ID, "error", err)
		return
	}
	slack := kitslack.NewClient(botToken)

	users, err := models.ListUsersByTenant(ctx, s.pool, tenant.ID)
	if err != nil {
		slog.Error("listing users for sync", "tenant_id", tenant.ID, "error", err)
		return
	}

	for _, user := range users {
		info, err := slack.GetUserInfo(ctx, user.SlackUserID)
		if err != nil {
			slog.Warn("fetching slack profile", "user_id", user.ID, "error", err)
			continue
		}

		if err := models.UpdateUserProfile(ctx, s.pool, tenant.ID, user.ID, info.DisplayName, info.Timezone); err != nil {
			slog.Warn("updating user profile", "user_id", user.ID, "error", err)
		}

		// Rate limit: 50ms between API calls (~20 req/sec, well within Slack's tier 2 limit)
		time.Sleep(50 * time.Millisecond)
	}
}
