// Package scheduler: registry.go is where code-defined scheduled work is
// declared and converged into jobs rows.
//
// Before this, an app that needed periodic work had three bad options: a
// hardcoded entry in builtin.go (global to every tenant, keyed on its own
// description, uneditable), a goroutine ticker via apps.CronJob (invisible —
// no row, no last run, no audit, and re-phased to zero on every deploy), or a
// per-tick sweep hook (same, minus the schedule). Registering here gets a
// real jobs row per tenant: run history, last_error, audit, and the same
// claim/dispatch path user-created jobs already use.
//
// Registration is process-global and happens at app Init time, the same
// pattern RegisterJobRunner already uses.
package scheduler

import (
	"context"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// ScheduledTask declares one unit of recurring native work.
type ScheduledTask struct {
	// Key is the stable identity, conventionally "<app>.<what>" —
	// "events.reconcile", "system.profile_sync". It is what binds a row
	// back to this handler and must never change: renaming it orphans
	// every existing row. Description is the label and is free to change.
	Key string

	// Description is the human-readable label shown in listings. Owned by
	// the code — the reconciler overwrites it on every pass.
	Description string

	// DefaultCron is a 5-field expression evaluated in the tenant's
	// timezone. Also code-owned: change it here and every tenant's row
	// follows on the next reconcile.
	DefaultCron string

	// LLMBound marks work that calls Anthropic — directly, or by running a
	// full agent loop. Those rows are claimed into the serialized agent
	// lane so they keep sharing one rate-limit budget with agent jobs
	// instead of running wide alongside IO-bound sweeps.
	LLMBound bool

	// AppliesTo reports whether this task should exist for a tenant at
	// all: app enabled, calendar connected, build hook configured. Nil
	// means "every tenant". A tenant that stops qualifying has its row
	// retired rather than deleted, so its history survives.
	//
	// Called on every reconcile pass, so keep it to a cheap query.
	AppliesTo func(ctx context.Context, tenantID uuid.UUID) bool

	// Run executes one occurrence for one tenant. The job row carries the
	// tenant; handlers should never fan out across tenants themselves.
	Run func(ctx context.Context, job models.Job) error
}

// Lane resolves which execution pool this task's rows belong in.
func (t ScheduledTask) Lane() models.JobLane {
	if t.LLMBound {
		return models.JobLaneAgent
	}
	return models.JobLaneFunction
}

var (
	scheduledMu    sync.RWMutex
	scheduledTasks = map[string]ScheduledTask{}
)

// RegisterScheduledTask installs (or replaces) a task registration. Safe to
// call from app Init; last registration for a key wins so tests can swap one
// out without a process restart.
func RegisterScheduledTask(t ScheduledTask) {
	if t.Key == "" || t.Run == nil {
		slog.Error("ignoring scheduled task with no key or handler", "key", t.Key)
		return
	}
	scheduledMu.Lock()
	defer scheduledMu.Unlock()
	scheduledTasks[t.Key] = t
}

// ClearScheduledTasksForTest drops all registrations. Tests only.
func ClearScheduledTasksForTest() {
	scheduledMu.Lock()
	defer scheduledMu.Unlock()
	scheduledTasks = map[string]ScheduledTask{}
}

// scheduledTask returns one registration by key.
func scheduledTask(key string) (ScheduledTask, bool) {
	scheduledMu.RLock()
	defer scheduledMu.RUnlock()
	t, ok := scheduledTasks[key]
	return t, ok
}

// snapshotScheduledTasks copies the registry so a reconcile pass isn't
// holding the lock while it runs AppliesTo queries.
func snapshotScheduledTasks() []ScheduledTask {
	scheduledMu.RLock()
	defer scheduledMu.RUnlock()
	out := make([]ScheduledTask, 0, len(scheduledTasks))
	for _, t := range scheduledTasks {
		out = append(out, t)
	}
	return out
}

// Retirement reasons, written to last_error so an operator reading a parked
// row can tell "this tenant stopped qualifying" from "this code is gone".
const (
	retiredNotApplicable = "not applicable for this tenant (app disabled or prerequisite missing)"
	retiredUnregistered  = "no longer registered in this build"
)

// reconcileRegistry converges every tenant's registry rows with the code
// registry. Runs at startup and on a ticker; both are cheap enough to repeat
// because every operation is idempotent.
func (s *Scheduler) reconcileRegistry(ctx context.Context) {
	tenants, err := models.ListAllTenants(ctx, s.pool)
	if err != nil {
		slog.Error("listing tenants for job registry reconcile", "error", err)
		return
	}
	tasks := snapshotScheduledTasks()
	for _, tenant := range tenants {
		s.reconcileTenantRegistry(ctx, tenant, tasks)
	}
}

// reconcileTenantRegistry converges one tenant. Errors are logged per row and
// never abort the pass — one misbehaving task must not stop the others from
// converging.
func (s *Scheduler) reconcileTenantRegistry(ctx context.Context, tenant models.Tenant, tasks []ScheduledTask) {
	creator, err := s.registryCreator(ctx, tenant.ID)
	if err != nil || creator == uuid.Nil {
		// A tenant with no users yet (fresh install, before the first
		// Slack event) has nobody to own the rows. Next pass will catch it.
		slog.Debug("skipping job registry reconcile, no user to own rows", "tenant_id", tenant.ID)
		return
	}

	desired := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if t.AppliesTo != nil && !t.AppliesTo(ctx, tenant.ID) {
			continue
		}
		desired[t.Key] = true
		if _, err := models.RegistryUpsert(ctx, s.pool, models.RegistryTaskRow{
			TenantID:    tenant.ID,
			CreatedBy:   creator,
			Key:         t.Key,
			Description: t.Description,
			CronExpr:    t.DefaultCron,
			Timezone:    tenant.Timezone,
			Lane:        t.Lane(),
		}); err != nil {
			slog.Error("upserting registry job", "tenant_id", tenant.ID, "key", t.Key, "error", err)
		}
	}

	existing, err := models.ListRegistryJobs(ctx, s.pool, tenant.ID)
	if err != nil {
		slog.Error("listing registry jobs", "tenant_id", tenant.ID, "error", err)
		return
	}
	for _, row := range existing {
		key := *row.BuiltinKey
		if desired[key] {
			continue
		}
		reason := retiredNotApplicable
		if _, registered := scheduledTask(key); !registered {
			reason = retiredUnregistered
		}
		if err := models.RegistryRetire(ctx, s.pool, tenant.ID, key, reason); err != nil {
			slog.Error("retiring registry job", "tenant_id", tenant.ID, "key", key, "error", err)
		}
	}
}

// registryCreator picks the user a tenant's system rows are attributed to.
// Prefers an admin; falls back to any user. See the created_by note in
// models.RegistryUpsert for why this is re-evaluated on every pass.
func (s *Scheduler) registryCreator(ctx context.Context, tenantID uuid.UUID) (uuid.UUID, error) {
	admin, err := models.FindAdminUser(ctx, s.pool, tenantID)
	if err != nil {
		return uuid.Nil, err
	}
	if admin != nil {
		return admin.ID, nil
	}
	users, err := models.ListUsersByTenant(ctx, s.pool, tenantID)
	if err != nil || len(users) == 0 {
		return uuid.Nil, err
	}
	return users[0].ID, nil
}
