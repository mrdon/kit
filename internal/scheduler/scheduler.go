package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// Scheduler runs due jobs and syncs user profiles on a schedule.
type Scheduler struct {
	pool  *pgxpool.Pool
	enc   *crypto.Encryptor
	agent *agent.Agent
	// lanes is one runner per execution pool, built from lanePolicies.
	lanes []*laneRunner
}

// New creates a new Scheduler.
func New(pool *pgxpool.Pool, enc *crypto.Encryptor, a *agent.Agent) *Scheduler {
	// Buffered so Kick never blocks; if a kick is already pending, extra
	// kicks coalesce into that one run.
	s := &Scheduler{pool: pool, enc: enc, agent: a}
	for _, policy := range lanePolicies {
		s.lanes = append(s.lanes, newLaneRunner(s, policy))
	}
	// Register the baseline runners for agent + builtin task_types. Each
	// wraps Scheduler methods, so s must exist before registration.
	// Idempotent: repeat constructions in tests replace the runner
	// pointers but preserve the map keys.
	RegisterJobRunner(&agentRunner{s: s})
	RegisterJobRunner(&builtinRunner{s: s})
	// Must precede the first reconcile pass in Start, or the scheduler's
	// own rows would be retired as unregistered on startup.
	s.registerSystemTasks()
	return s
}

// Kick wakes the job loop immediately instead of waiting for the next
// poll tick. Used by decision-resolution so a resumed workflow advances
// within a second of the user tapping, not up to 60s later. Non-blocking
// — concurrent kicks coalesce into a single extra claim cycle.
func (s *Scheduler) Kick() {
	for _, lane := range s.lanes {
		select {
		case lane.kick <- struct{}{}:
		default:
		}
	}
}

// Start launches the job runner. Builtin jobs (like profile sync) are ensured
// on startup and run via the same job loop as user-created jobs.
func (s *Scheduler) Start(ctx context.Context) {
	s.reconcileRegistry(ctx)
	// Jobs left in 'running' by a previous crash get reclaimed. Use a
	// generous cutoff so we don't race a sibling scheduler that is still
	// running the job in a rolling-deploy window.
	if n, err := models.RecoverStuckTasks(ctx, s.pool, 15*time.Minute); err != nil {
		slog.Warn("recovering stuck jobs", "error", err)
	} else if n > 0 {
		slog.Info("recovered stuck jobs", "count", n)
	}
	for _, lane := range s.lanes {
		slog.Info("starting job lane", "lane", lane.policy.Lane,
			"max_parallel", lane.policy.MaxParallel, "poll", lane.policy.PollInterval)
		go lane.run(ctx)
	}
	go s.runRegistryReconcileLoop(ctx)
}

// registryReconcileInterval is how often the code registry is re-converged
// with the jobs table. It is what picks up a tenant installed since startup,
// an app toggled on or off, and a task whose prerequisite changed — so it
// wants to be short enough that none of those feel stuck. Every operation in
// a pass is idempotent, so repeating it costs a handful of queries.
const registryReconcileInterval = 15 * time.Minute

// runRegistryReconcileLoop re-converges the registry on a ticker.
//
// This is deliberately a plain ticker rather than a jobs row: a per-tenant
// row can only exist for a tenant the reconciler has already visited, so a
// tenant created after startup would have nothing to create its rows. It is
// also not hooked to tenant creation — internal/scheduler already imports
// internal/slack, so a hook in the OAuth handler would be an import cycle,
// and at UpsertTenant time the tenant has no users for jobs.created_by to
// reference anyway.
func (s *Scheduler) runRegistryReconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(registryReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileRegistry(ctx)
		}
	}
}

// ProcessDueTasksForTest drives one poll of every lane and waits for the
// work to finish. Production never waits; tests need a deterministic
// single-shot tick that doesn't care which lane a row landed in.
//
// Not part of the package's external API — do not call from non-test code.
func (s *Scheduler) ProcessDueTasksForTest(ctx context.Context) {
	for _, lane := range s.lanes {
		lane.claimAndDispatch(ctx, nil)
	}
	for _, lane := range s.lanes {
		lane.drain()
	}
}

// ProcessDueTasksForTenantForTest is a tenant-scoped single-tick variant
// so fixtures running in parallel against the shared Postgres don't claim
// each other's rows.
//
// Not part of the package's external API — do not call from non-test code.
func (s *Scheduler) ProcessDueTasksForTenantForTest(ctx context.Context, tenantID uuid.UUID) {
	for _, lane := range s.lanes {
		lane.claimAndDispatch(ctx, &tenantID)
	}
	for _, lane := range s.lanes {
		lane.drain()
	}
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
