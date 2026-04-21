package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

const maxConcurrentTasks = 5

// PeriodicSweep is a background job invoked on every poll tick.
// Intended for housekeeping like the stuck-resolving card recovery in
// the cards app; decoupled via a function pointer so the scheduler
// package doesn't need to import cards. Callers register via
// RegisterPeriodicSweep.
type PeriodicSweep func(ctx context.Context) error

var periodicSweeps []PeriodicSweep
var periodicSweepsMu sync.Mutex

// RegisterPeriodicSweep adds a job to run on every scheduler poll
// tick. Safe to call at startup (wiring) only; not safe under
// concurrent Start() calls.
func RegisterPeriodicSweep(s PeriodicSweep) {
	periodicSweepsMu.Lock()
	defer periodicSweepsMu.Unlock()
	periodicSweeps = append(periodicSweeps, s)
}

// Scheduler runs due tasks and syncs user profiles on a schedule.
type Scheduler struct {
	pool   *pgxpool.Pool
	enc    *crypto.Encryptor
	agent  *agent.Agent
	kickCh chan struct{}
}

// New creates a new Scheduler.
func New(pool *pgxpool.Pool, enc *crypto.Encryptor, a *agent.Agent) *Scheduler {
	// Buffered so Kick never blocks; if a kick is already pending, extra
	// kicks coalesce into that one run.
	s := &Scheduler{pool: pool, enc: enc, agent: a, kickCh: make(chan struct{}, 1)}
	// Register the baseline runners for agent + builtin task_types. Each
	// wraps Scheduler methods, so s must exist before registration.
	// Idempotent: repeat constructions in tests replace the runner
	// pointers but preserve the map keys.
	RegisterTaskRunner(&agentRunner{s: s})
	RegisterTaskRunner(&builtinRunner{s: s})
	return s
}

// Kick wakes the task loop immediately instead of waiting for the next
// poll tick. Used by decision-resolution so a resumed workflow advances
// within a second of the user tapping, not up to 60s later. Non-blocking
// — concurrent kicks coalesce into a single extra claim cycle.
func (s *Scheduler) Kick() {
	select {
	case s.kickCh <- struct{}{}:
	default:
	}
}

// Start launches the task runner. Builtin tasks (like profile sync) are ensured
// on startup and run via the same task loop as user-created tasks.
func (s *Scheduler) Start(ctx context.Context) {
	s.ensureBuiltinTasks(ctx)
	// Tasks left in 'running' by a previous crash get reclaimed. Use a
	// generous cutoff so we don't race a sibling scheduler that is still
	// running the task in a rolling-deploy window.
	if n, err := models.RecoverStuckTasks(ctx, s.pool, 15*time.Minute); err != nil {
		slog.Warn("recovering stuck tasks", "error", err)
	} else if n > 0 {
		slog.Info("recovered stuck tasks", "count", n)
	}
	go s.runTaskLoop(ctx)
}

func (s *Scheduler) runTaskLoop(ctx context.Context) {
	const pollInterval = 60 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	process := func() {
		s.processDueTasks(ctx)
		s.runPeriodicSweeps(ctx)
		// Reset so a kick both runs now AND pushes the next natural
		// tick a full interval out, guaranteeing ≥ pollInterval between
		// runs (no redundant back-to-back scans).
		ticker.Reset(pollInterval)
	}

	process()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		case <-s.kickCh:
			process()
		}
	}
}

// runPeriodicSweeps invokes every registered periodic sweep, logging
// errors and continuing. Isolated from processDueTasks so a sweep
// failure can't poison task execution. Called once per tick.
func (s *Scheduler) runPeriodicSweeps(ctx context.Context) {
	periodicSweepsMu.Lock()
	sweeps := append([]PeriodicSweep(nil), periodicSweeps...)
	periodicSweepsMu.Unlock()
	for _, sweep := range sweeps {
		if err := sweep(ctx); err != nil {
			slog.Warn("periodic sweep failed", "error", err)
		}
	}
}

// ProcessDueTasksForTest drives one iteration of processDueTasks from
// outside the package. Production uses the ticker loop; tests need a
// deterministic single-shot tick.
//
// Not part of the package's external API — do not call from non-test
// code.
func (s *Scheduler) ProcessDueTasksForTest(ctx context.Context) {
	s.processDueTasks(ctx)
}

// ProcessDueTasksForTenantForTest is a tenant-scoped single-tick variant
// so fixtures running in parallel against the shared Postgres don't claim
// each other's rows.
//
// Not part of the package's external API — do not call from non-test
// code.
func (s *Scheduler) ProcessDueTasksForTenantForTest(ctx context.Context, tenantID uuid.UUID) {
	s.processDueTasksForTenant(ctx, tenantID)
}

func (s *Scheduler) processDueTasks(ctx context.Context) {
	// ClaimDueTasks atomically flips status to 'running' under SKIP LOCKED,
	// so concurrent schedulers (e.g. during a rolling deploy) never run the
	// same task twice.
	tasks, err := models.ClaimDueTasks(ctx, s.pool, maxConcurrentTasks*2)
	if err != nil {
		slog.Error("claiming due tasks", "error", err)
		return
	}
	s.fanOutClaimed(ctx, tasks)
}

// processDueTasksForTenant is the tenant-scoped claim variant used by
// tests. Production code always calls processDueTasks.
func (s *Scheduler) processDueTasksForTenant(ctx context.Context, tenantID uuid.UUID) {
	tasks, err := models.ClaimDueTasksForTenant(ctx, s.pool, tenantID, maxConcurrentTasks*2)
	if err != nil {
		slog.Error("claiming due tasks for tenant", "error", err)
		return
	}
	s.fanOutClaimed(ctx, tasks)
}

// fanOutClaimed dispatches each claimed task through the runner registry,
// bounded by maxConcurrentTasks.
func (s *Scheduler) fanOutClaimed(ctx context.Context, tasks []models.Task) {
	if len(tasks) == 0 {
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentTasks)
	for i := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(t models.Task) {
			defer wg.Done()
			defer func() { <-sem }()
			s.dispatchTask(ctx, &t)
		}(tasks[i])
	}
	wg.Wait()
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
