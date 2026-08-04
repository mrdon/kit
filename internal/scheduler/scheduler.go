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

// ExecPolicy is how one lane is executed. Lanes exist because the reason
// scheduled work is serialized applies to only some of it: every tenant's
// agent shares one Anthropic org-wide rate limit, so two agent runs racing
// in the same minute can mutually 429 each other on requests that would
// individually fit. A calendar sync has no such constraint and should never
// have been queued behind one.
//
// Policy hangs off the lane rather than the JobRunner because lane is a
// property of the row, not of job_type: a builtin registration that calls
// the LLM has to be serialized alongside agent runs even though its handler
// is native Go.
type ExecPolicy struct {
	Lane         models.JobLane
	MaxParallel  int
	PollInterval time.Duration
}

// lanePolicies is the execution plan, one entry per lane.
//
// Both lanes are held at MaxParallel 1 for now. The agent lane stays there
// until there is a rate limiter in internal/anthropic — today the only thing
// bounding LLM concurrency is this number, and it does not cover interactive
// Slack or card-chat traffic hitting the same org limit.
var lanePolicies = []ExecPolicy{
	{Lane: models.JobLaneAgent, MaxParallel: 1, PollInterval: 60 * time.Second},
	{Lane: models.JobLaneFunction, MaxParallel: 1, PollInterval: 60 * time.Second},
}

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

// Scheduler runs due jobs and syncs user profiles on a schedule.
type Scheduler struct {
	pool  *pgxpool.Pool
	enc   *crypto.Encryptor
	agent *agent.Agent
	// kickChans has one channel per lane. A kick has to reach every lane:
	// the caller (decision resolution) knows a job became due, not which
	// pool it will be claimed from.
	kickChans []chan struct{}
}

// New creates a new Scheduler.
func New(pool *pgxpool.Pool, enc *crypto.Encryptor, a *agent.Agent) *Scheduler {
	// Buffered so Kick never blocks; if a kick is already pending, extra
	// kicks coalesce into that one run.
	kicks := make([]chan struct{}, len(lanePolicies))
	for i := range kicks {
		kicks[i] = make(chan struct{}, 1)
	}
	s := &Scheduler{pool: pool, enc: enc, agent: a, kickChans: kicks}
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
	for _, ch := range s.kickChans {
		select {
		case ch <- struct{}{}:
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
	for i, policy := range lanePolicies {
		slog.Info("starting job lane", "lane", policy.Lane,
			"max_parallel", policy.MaxParallel, "poll", policy.PollInterval)
		go s.runLaneLoop(ctx, policy, s.kickChans[i])
	}
	go s.runSweepLoop(ctx)
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

// runLaneLoop claims and dispatches one lane's due work until ctx ends.
// One goroutine per lane, each with its own claim query, so a saturated
// lane is invisible to the others.
func (s *Scheduler) runLaneLoop(ctx context.Context, policy ExecPolicy, kick <-chan struct{}) {
	ticker := time.NewTicker(policy.PollInterval)
	defer ticker.Stop()

	process := func() {
		s.processDueTasks(ctx, policy)
		// Reset so a kick both runs now AND pushes the next natural
		// tick a full interval out, guaranteeing ≥ PollInterval between
		// runs (no redundant back-to-back scans).
		ticker.Reset(policy.PollInterval)
	}

	process()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		case <-kick:
			process()
		}
	}
}

// runSweepLoop drives the legacy per-tick sweep hook on its own timer.
//
// It used to piggyback on the single job loop's tick. Now that lanes poll at
// their own rates, giving it a dedicated ticker keeps its cadence fixed
// instead of quietly following whatever the function lane is set to. Goes
// away once the remaining sweeps become registered tasks.
func (s *Scheduler) runSweepLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	s.runPeriodicSweeps(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runPeriodicSweeps(ctx)
		}
	}
}

// runPeriodicSweeps invokes every registered periodic sweep, logging
// errors and continuing. Isolated from processDueTasks so a sweep
// failure can't poison job execution. Called once per tick.
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

// ProcessDueTasksForTest drives one iteration of every lane from outside
// the package. Production uses per-lane ticker loops; tests need a
// deterministic single-shot tick that doesn't care which lane a row is in.
//
// Not part of the package's external API — do not call from non-test
// code.
func (s *Scheduler) ProcessDueTasksForTest(ctx context.Context) {
	for _, policy := range lanePolicies {
		s.processDueTasks(ctx, policy)
	}
}

// ProcessDueTasksForTenantForTest is a tenant-scoped single-tick variant
// so fixtures running in parallel against the shared Postgres don't claim
// each other's rows.
//
// Not part of the package's external API — do not call from non-test
// code.
func (s *Scheduler) ProcessDueTasksForTenantForTest(ctx context.Context, tenantID uuid.UUID) {
	for _, policy := range lanePolicies {
		s.processDueTasksForTenant(ctx, tenantID, policy)
	}
}

func (s *Scheduler) processDueTasks(ctx context.Context, policy ExecPolicy) {
	// ClaimDueTasks atomically flips status to 'running' under SKIP LOCKED,
	// so concurrent schedulers (e.g. during a rolling deploy) never run the
	// same job twice.
	jobs, err := models.ClaimDueTasks(ctx, s.pool, policy.Lane, policy.MaxParallel)
	if err != nil {
		slog.Error("claiming due jobs", "lane", policy.Lane, "error", err)
		return
	}
	s.fanOutClaimed(ctx, jobs, policy.MaxParallel)
}

// processDueTasksForTenant is the tenant-scoped claim variant used by
// tests. Production code always calls processDueTasks.
func (s *Scheduler) processDueTasksForTenant(ctx context.Context, tenantID uuid.UUID, policy ExecPolicy) {
	jobs, err := models.ClaimDueTasksForTenant(ctx, s.pool, tenantID, policy.Lane, policy.MaxParallel)
	if err != nil {
		slog.Error("claiming due jobs for tenant", "lane", policy.Lane, "error", err)
		return
	}
	s.fanOutClaimed(ctx, jobs, policy.MaxParallel)
}

// fanOutClaimed dispatches each claimed job through the runner registry,
// bounded by the lane's parallelism.
func (s *Scheduler) fanOutClaimed(ctx context.Context, jobs []models.Job, maxParallel int) {
	if len(jobs) == 0 {
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)
	for i := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(t models.Job) {
			defer wg.Done()
			defer func() { <-sem }()
			s.dispatchTask(ctx, &t)
		}(jobs[i])
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
