package task

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// fixture spins up an isolated tenant with three users and two roles, returning
// the service plus everything the tests need to build callers.
type fixture struct {
	pool         *pgxpool.Pool
	svc          *TaskService
	tenant       *models.Tenant
	alice        *models.User // member only
	bob          *models.User // member + extra "founders" role
	admin        *models.User // admin
	memberRoleID uuid.UUID
	foundersID   uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_task_test_" + uuid.NewString()
	slug := models.SanitizeSlug("task-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "task-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	memberRole, err := models.CreateRole(ctx, pool, tenant.ID, models.RoleMember, "regular member")
	if err != nil {
		t.Fatalf("creating member role: %v", err)
	}
	if _, err := models.CreateRole(ctx, pool, tenant.ID, models.RoleAdmin, "admin"); err != nil {
		t.Fatalf("creating admin role: %v", err)
	}
	founders, err := models.CreateRole(ctx, pool, tenant.ID, "founders", "founders")
	if err != nil {
		t.Fatalf("creating founders role: %v", err)
	}

	alice, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_alice", "Alice", "")
	if err != nil {
		t.Fatalf("creating alice: %v", err)
	}
	bob, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_bob", "Bob", "")
	if err != nil {
		t.Fatalf("creating bob: %v", err)
	}
	admin, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_admin", "Admin", "")
	if err != nil {
		t.Fatalf("creating admin: %v", err)
	}
	for _, u := range []*models.User{alice, bob, admin} {
		if err := models.AssignRole(ctx, pool, tenant.ID, u.ID, models.RoleMember); err != nil {
			t.Fatalf("assigning member role: %v", err)
		}
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, admin.ID, models.RoleAdmin); err != nil {
		t.Fatalf("assigning admin role: %v", err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, bob.ID, "founders"); err != nil {
		t.Fatalf("assigning founders role: %v", err)
	}

	return &fixture{
		pool:         pool,
		svc:          &TaskService{pool: pool},
		tenant:       tenant,
		alice:        alice,
		bob:          bob,
		admin:        admin,
		memberRoleID: memberRole.ID,
		foundersID:   founders.ID,
	}
}

func (f *fixture) caller(t *testing.T, u *models.User) *services.Caller {
	t.Helper()
	ctx := context.Background()
	roleIDs, _ := models.GetUserRoleIDs(ctx, f.pool, f.tenant.ID, u.ID)
	roleNames, _ := models.GetUserRoleNames(ctx, f.pool, f.tenant.ID, u.ID)
	return &services.Caller{
		TenantID: f.tenant.ID,
		UserID:   u.ID,
		Identity: u.SlackUserID,
		Roles:    roleNames,
		RoleIDs:  roleIDs,
		IsAdmin:  slices.Contains(roleNames, models.RoleAdmin),
	}
}

// TestCreateAutoUsesSingleRole: a user with exactly one role gets that
// role automatically when role_scope is omitted.
func TestCreateAutoUsesSingleRole(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Alice has only `member`. Create without role_scope → uses `member`.
	t1, err := f.svc.Create(ctx, f.caller(t, f.alice), CreateInput{Title: "alice's task"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Verify the task is in the member role-scope.
	scope, err := getScopeRow(ctx, f.pool, f.tenant.ID, t1.ScopeID)
	if err != nil {
		t.Fatalf("loading scope: %v", err)
	}
	if scope.RoleID == nil || *scope.RoleID != f.memberRoleID {
		t.Fatalf("expected member role-scope, got %+v", scope)
	}
}

// TestCreateRequiresPrimaryWhenMultipleRoles: a user holding multiple
// roles without a primary set must pass role_scope explicitly.
func TestCreateRequiresPrimaryWhenMultipleRoles(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Bob has member + founders, no primary set.
	_, err := f.svc.Create(ctx, f.caller(t, f.bob), CreateInput{Title: "bob's ambiguous task"})
	if !errors.Is(err, ErrPrimaryRoleNotSet) {
		t.Fatalf("expected ErrPrimaryRoleNotSet, got %v", err)
	}

	// Setting bob's primary role makes it work.
	if err := models.SetUserPrimaryRoleID(ctx, f.pool, f.tenant.ID, f.bob.ID, &f.foundersID); err != nil {
		t.Fatalf("set primary: %v", err)
	}
	tk, err := f.svc.Create(ctx, f.caller(t, f.bob), CreateInput{Title: "bob's primary-routed task"})
	if err != nil {
		t.Fatalf("create after primary set: %v", err)
	}
	scope, err := getScopeRow(ctx, f.pool, f.tenant.ID, tk.ScopeID)
	if err != nil {
		t.Fatalf("loading scope: %v", err)
	}
	if scope.RoleID == nil || *scope.RoleID != f.foundersID {
		t.Fatalf("expected founders role-scope, got %+v", scope)
	}
}

// TestCreateExplicitRoleScope: passing role_scope overrides any default.
// Caller must hold the role unless they're admin.
func TestCreateExplicitRoleScope(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Bob is in founders → can pass role_scope=founders.
	tk, err := f.svc.Create(ctx, f.caller(t, f.bob), CreateInput{
		Title:    "founders task",
		RoleName: "founders",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	scope, _ := getScopeRow(ctx, f.pool, f.tenant.ID, tk.ScopeID)
	if scope.RoleID == nil || *scope.RoleID != f.foundersID {
		t.Fatalf("expected founders, got %+v", scope)
	}

	// Alice is NOT in founders → forbidden.
	_, err = f.svc.Create(ctx, f.caller(t, f.alice), CreateInput{
		Title:    "sneaky alice task",
		RoleName: "founders",
	})
	if !errors.Is(err, services.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// TestRoleVisibility: anyone in the role can see and edit the task,
// regardless of who's assigned. People outside the role see nothing.
func TestRoleVisibility(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Bob creates a founders task assigned to himself.
	tk, err := f.svc.Create(ctx, f.caller(t, f.bob), CreateInput{
		Title:          "founders task",
		RoleName:       "founders",
		AssigneeUserID: &f.bob.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Alice (not in founders) can't see it.
	if _, _, err := f.svc.Get(ctx, f.caller(t, f.alice), tk.ID); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("alice.Get expected ErrNotFound, got %v", err)
	}

	// Add alice to founders. Now she sees it.
	if err := models.AssignRole(ctx, f.pool, f.tenant.ID, f.alice.ID, "founders"); err != nil {
		t.Fatalf("assigning alice to founders: %v", err)
	}
	got, _, err := f.svc.Get(ctx, f.caller(t, f.alice), tk.ID)
	if err != nil {
		t.Fatalf("alice.Get after role grant: %v", err)
	}
	if got.ID != tk.ID {
		t.Fatalf("got wrong task: %v", got.ID)
	}

	// Alice can update it (assignee orthogonal — anyone in role can edit).
	newTitle := "alice edited"
	if _, err := f.svc.Update(ctx, f.caller(t, f.alice), tk.ID, UpdateInput{Title: &newTitle}); err != nil {
		t.Fatalf("alice.Update on founders task: %v", err)
	}
}

// TestUpdateAssigneeOrthogonal: changing the assignee doesn't affect
// visibility. The task stays in the same role.
func TestUpdateAssigneeOrthogonal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	tk, err := f.svc.Create(ctx, f.caller(t, f.bob), CreateInput{
		Title:    "team task",
		RoleName: "founders",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Assign to bob.
	updated, err := f.svc.Update(ctx, f.caller(t, f.bob), tk.ID, UpdateInput{
		NewAssigneeUserID: &f.bob.ID,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.AssigneeUserID == nil || *updated.AssigneeUserID != f.bob.ID {
		t.Fatalf("expected assignee=bob, got %v", updated.AssigneeUserID)
	}
	if updated.ScopeID != tk.ScopeID {
		t.Fatalf("scope changed unexpectedly: was %s now %s", tk.ScopeID, updated.ScopeID)
	}

	// Clear the assignee — back to backlog.
	cleared, err := f.svc.Update(ctx, f.caller(t, f.bob), tk.ID, UpdateInput{ClearAssignee: true})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.AssigneeUserID != nil {
		t.Fatalf("expected assignee=nil, got %v", *cleared.AssigneeUserID)
	}
}

// TestRejectRoleScopeEmpty: every task must belong to a role; explicit
// empty/"none" is an error.
func TestRejectRoleScopeEmpty(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	tk, err := f.svc.Create(ctx, f.caller(t, f.alice), CreateInput{Title: "alice task"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	none := "none"
	if _, err := f.svc.Update(ctx, f.caller(t, f.alice), tk.ID, UpdateInput{NewRoleName: &none}); err == nil {
		t.Fatalf("expected error on role_scope=none, got nil")
	}

	empty := ""
	if _, err := f.svc.Update(ctx, f.caller(t, f.alice), tk.ID, UpdateInput{NewRoleName: &empty}); err == nil {
		t.Fatalf("expected error on role_scope=\"\", got nil")
	}
}

// TestCascadeRoleDeleteRemovesTasks: deleting the role cascades to the
// tasks via the existing scopes.role_id ON DELETE CASCADE chain.
func TestCascadeRoleDeleteRemovesTasks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	tk, err := f.svc.Create(ctx, f.caller(t, f.bob), CreateInput{
		Title:    "founders task",
		RoleName: "founders",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := models.DeleteRole(ctx, f.pool, f.tenant.ID, "founders"); err != nil {
		t.Fatalf("delete role: %v", err)
	}

	// Task should have been cascaded out via the scope row.
	var n int
	if err := f.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM app_tasks WHERE id = $1", tk.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected task to cascade-delete with role, found %d", n)
	}
}

// TestSnoozeHidesFromFeed: snoozing drops a task from the swipe feed
// until snoozed_until passes.
func TestSnoozeHidesFromFeed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	caller := f.caller(t, f.bob)
	tk, err := f.svc.Create(ctx, caller, CreateInput{
		Title:          "to snooze",
		RoleName:       "founders",
		AssigneeUserID: &f.bob.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	feed, err := listStackTasks(ctx, f.pool, caller, 50, false)
	if err != nil {
		t.Fatalf("listStackTasks: %v", err)
	}
	if !containsStackTask(feed, tk.ID) {
		t.Fatalf("expected task in active feed before snooze")
	}

	until := time.Now().Add(48 * time.Hour)
	if _, err := f.svc.Snooze(ctx, caller, tk.ID, until); err != nil {
		t.Fatalf("snooze: %v", err)
	}

	feed, _ = listStackTasks(ctx, f.pool, caller, 50, false)
	if containsStackTask(feed, tk.ID) {
		t.Fatalf("snoozed task should NOT be in active feed")
	}
	pile, _ := listStackTasks(ctx, f.pool, caller, 50, true)
	if !containsStackTask(pile, tk.ID) {
		t.Fatalf("snoozed task should appear in snoozed pile")
	}
}

// TestUnassignedFeed: unassigned tasks in my role show up in my feed.
// Assigning to someone else removes them from my feed (unless that
// someone else is me).
func TestUnassignedFeed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	bobCaller := f.caller(t, f.bob)
	tk, err := f.svc.Create(ctx, bobCaller, CreateInput{
		Title:    "unassigned founders work",
		RoleName: "founders",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Bob (in founders, unassigned) sees it.
	feed, _ := listStackTasks(ctx, f.pool, bobCaller, 50, false)
	if !containsStackTask(feed, tk.ID) {
		t.Fatalf("unassigned founders task should be in bob's feed")
	}

	// Alice (not in founders) does not.
	aliceCaller := f.caller(t, f.alice)
	feed, _ = listStackTasks(ctx, f.pool, aliceCaller, 50, false)
	if containsStackTask(feed, tk.ID) {
		t.Fatalf("alice not in founders should not see the task")
	}

	// Assign to bob himself — still in his feed via the assigned-to-me branch.
	if _, err := f.svc.Update(ctx, bobCaller, tk.ID, UpdateInput{NewAssigneeUserID: &f.bob.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	feed, _ = listStackTasks(ctx, f.pool, bobCaller, 50, false)
	if !containsStackTask(feed, tk.ID) {
		t.Fatalf("assigned-to-me should still be in bob's feed")
	}
}

func containsStackTask(tasks []stackTask, id uuid.UUID) bool {
	for _, t := range tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

// TestListExcludesClosedByDefault: an unfiltered list_tasks omits done and
// cancelled rows so agent context isn't bloated with handled history. An
// explicit status, IncludeClosed, or ClosedSince brings them back.
func TestListExcludesClosedByDefault(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	caller := f.caller(t, f.bob)

	open, err := f.svc.Create(ctx, caller, CreateInput{Title: "open task", RoleName: "founders"})
	if err != nil {
		t.Fatalf("create open: %v", err)
	}
	doneTask, err := f.svc.Create(ctx, caller, CreateInput{Title: "done task", RoleName: "founders"})
	if err != nil {
		t.Fatalf("create done: %v", err)
	}
	if _, err := f.svc.Complete(ctx, caller, doneTask.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	cancelled, err := f.svc.Create(ctx, caller, CreateInput{Title: "cancelled task", RoleName: "founders"})
	if err != nil {
		t.Fatalf("create cancelled: %v", err)
	}
	cancelledStatus := "cancelled"
	if _, err := f.svc.Update(ctx, caller, cancelled.ID, UpdateInput{Status: &cancelledStatus}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// Default: only the open task is visible.
	got, err := f.svc.List(ctx, caller, TaskFilters{})
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	if !containsTask(got, open.ID) {
		t.Errorf("default list should include open task")
	}
	if containsTask(got, doneTask.ID) || containsTask(got, cancelled.ID) {
		t.Errorf("default list must not include done/cancelled tasks")
	}

	// IncludeClosed=true brings them back.
	got, err = f.svc.List(ctx, caller, TaskFilters{IncludeClosed: true})
	if err != nil {
		t.Fatalf("list include_closed: %v", err)
	}
	if !containsTask(got, open.ID) || !containsTask(got, doneTask.ID) || !containsTask(got, cancelled.ID) {
		t.Errorf("include_closed should return all three; got %d rows", len(got))
	}

	// Explicit status=done returns only the done row even with the new default.
	got, err = f.svc.List(ctx, caller, TaskFilters{Status: "done"})
	if err != nil {
		t.Fatalf("list status=done: %v", err)
	}
	if len(got) != 1 || got[0].ID != doneTask.ID {
		t.Errorf("status=done should return only the done task; got %d rows", len(got))
	}
}

func containsTask(tasks []Task, id uuid.UUID) bool {
	for _, t := range tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

// TestListTasksLimit: the limit is parameterized — callers can request more
// than the default page (the intake dedup read needs the full set), and a
// small limit truncates as asked.
func TestListTasksLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c := f.caller(t, f.alice)

	for range 3 {
		if _, err := f.svc.Create(ctx, c, CreateInput{Title: "task"}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// Default (Limit 0) returns all three.
	all, err := f.svc.List(ctx, c, TaskFilters{})
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 tasks at default limit, got %d", len(all))
	}

	// An explicit small limit truncates.
	capped, err := f.svc.List(ctx, c, TaskFilters{Limit: 2})
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if len(capped) != 2 {
		t.Fatalf("expected 2 tasks at limit=2, got %d", len(capped))
	}
}

// TestCreateDedupKey: a DedupKey makes Create idempotent. A second create with
// the same key returns the original task + ErrDuplicateTask and inserts no new
// row — and this holds even after the original is cancelled, which is the whole
// point: a duplicate the user killed must not be resurrected by the next scan.
func TestCreateDedupKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c := f.caller(t, f.alice)

	countByKey := func(key string) int {
		var n int
		if err := f.pool.QueryRow(ctx,
			"SELECT count(*) FROM app_tasks WHERE tenant_id = $1 AND dedup_key = $2",
			f.tenant.ID, key).Scan(&n); err != nil {
			t.Fatalf("counting rows: %v", err)
		}
		return n
	}

	key := "email:" + f.alice.ID.String() + ":42"

	first, err := f.svc.Create(ctx, c, CreateInput{Title: "Pay Square bill", DedupKey: key})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Second create, same key, different title → dedup hit returning the original.
	dup, err := f.svc.Create(ctx, c, CreateInput{Title: "Pay Square bill (again)", DedupKey: key})
	if !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("expected ErrDuplicateTask, got %v", err)
	}
	if dup == nil || dup.ID != first.ID {
		t.Fatalf("expected the original task back, got %+v", dup)
	}
	if dup.Title != "Pay Square bill" {
		t.Fatalf("expected original title preserved, got %q", dup.Title)
	}
	if n := countByKey(key); n != 1 {
		t.Fatalf("expected exactly 1 row for the key, got %d", n)
	}

	// Cancel the original, then try again — a cancelled duplicate must stay dead.
	if _, err := f.svc.Cancel(ctx, c, first.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	after, err := f.svc.Create(ctx, c, CreateInput{Title: "Pay Square bill (third try)", DedupKey: key})
	if !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("expected ErrDuplicateTask after cancel, got %v", err)
	}
	if after == nil || after.ID != first.ID || after.Status != "cancelled" {
		t.Fatalf("expected the cancelled original back, got %+v", after)
	}
	if n := countByKey(key); n != 1 {
		t.Fatalf("expected still exactly 1 row after cancel+recreate, got %d", n)
	}

	// Sanity: tasks without a key are never constrained — two succeed distinctly.
	a, err := f.svc.Create(ctx, c, CreateInput{Title: "keyless one"})
	if err != nil {
		t.Fatalf("keyless create a: %v", err)
	}
	b, err := f.svc.Create(ctx, c, CreateInput{Title: "keyless two"})
	if err != nil {
		t.Fatalf("keyless create b: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("keyless creates collided: %s", a.ID)
	}
}
