// Integration tests for the action_builtins bridge against a real Postgres.
//
// Slack calls (send_slack_message / dm_user) are exercised at the host-
// function boundary rather than end-to-end — the tests either skip the
// network path entirely (no slack client wired in) or construct a
// DryRunClient so post-side effects stay out of Slack while reads still
// work against captured state.
//
// Each test gets its own tenant + builder_app fixture so parallel runs
// don't collide. Cleanup cascades through the tenants ON DELETE CASCADE.
package builder

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/apps/task"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// actionFixture extends itemFixture with the services bundle and
// ActionBuiltins wired up, so tests can drive both direct Go dispatch and
// script-level Monty runs.
type actionFixture struct {
	pool     *pgxpool.Pool
	tenant   *models.Tenant
	appID    uuid.UUID
	userID   uuid.UUID
	user     *models.User
	svc      *services.Services
	actions  *ActionBuiltins
	callerCb func() *services.Caller
}

// newActionFixture creates a tenant/user/builder_app, a Services bundle,
// and an ActionBuiltins instance (without a Slack client so messaging
// tests can pick up a dry-run one as needed).
func newActionFixture(t *testing.T) *actionFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_builder_act_" + uuid.NewString()
	slug := models.SanitizeSlug("builder-act-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "builder-action-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_act_"+uuid.NewString()[:8], "Action Admin", "")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleAdmin, "admin"); err != nil {
		t.Fatalf("creating admin role: %v", err)
	}
	memberRole, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleMember, "member")
	if err != nil {
		t.Fatalf("creating member role: %v", err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, user.ID, models.RoleAdmin); err != nil {
		t.Fatalf("assigning admin role: %v", err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, user.ID, models.RoleMember); err != nil {
		t.Fatalf("assigning member role: %v", err)
	}
	// Set the user's primary role to member so create_task without
	// role_scope routes there. The legacy tests pre-date the role-only
	// model and didn't pass role_scope; adding a primary keeps them
	// working without sprinkling role_scope through every assertion.
	if err := models.SetUserPrimaryRoleID(ctx, pool, tenant.ID, user.ID, &memberRole.ID); err != nil {
		t.Fatalf("setting primary role: %v", err)
	}

	var appID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO builder_apps (tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, tenant.ID, "test-act-"+uuid.NewString()[:8], "action builtins test", user.ID).Scan(&appID)
	if err != nil {
		t.Fatalf("creating builder_app: %v", err)
	}

	// Encryptor with a dummy 32-byte key (64 hex chars); memories/Tasks
	// don't touch it directly, but services.New asks for one.
	enc, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	svc := services.New(pool, enc)

	actions := BuildActionBuiltins(pool, svc, nil, tenant.ID, appID, user.ID, nil)

	return &actionFixture{
		pool:    pool,
		tenant:  tenant,
		appID:   appID,
		userID:  user.ID,
		user:    user,
		svc:     svc,
		actions: actions,
		callerCb: func() *services.Caller {
			return &services.Caller{
				TenantID: tenant.ID,
				UserID:   user.ID,
				Identity: user.SlackUserID,
				IsAdmin:  true,
				Timezone: "UTC",
			}
		},
	}
}

// dispatchCall invokes a host function the same way the Monty runtime
// would: by calling the Handler with a FunctionCall.Name + Args map.
// Keeps tests focused on the Go-side behavior without going through a
// WASM round-trip.
func (f *actionFixture) dispatchCall(t *testing.T, name string, args map[string]any) (any, error) {
	t.Helper()
	return f.actions.Handler(context.Background(), &runtime.FunctionCall{
		Name: name,
		Args: args,
	})
}

func TestActionBuiltins_CreateTodo_HappyPath(t *testing.T) {
	f := newActionFixture(t)
	before := f.actions.MutationSummary()["inserts"]

	result, err := f.dispatchCall(t, FnCreateTask, map[string]any{
		"title":    "Prep garnishes",
		"priority": "high",
	})
	if err != nil {
		t.Fatalf("create_task: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want map", result)
	}
	if _, err := uuid.Parse(m["id"].(string)); err != nil {
		t.Errorf("id is not a UUID: %v", err)
	}
	if m["title"] != "Prep garnishes" {
		t.Errorf("title = %v", m["title"])
	}
	if m["priority"] != "high" {
		t.Errorf("priority = %v", m["priority"])
	}

	after := f.actions.MutationSummary()["inserts"]
	if after != before+1 {
		t.Errorf("inserts = %d, want %d", after, before+1)
	}
}

func TestActionBuiltins_UpdateTodo_BumpsUpdateCounter(t *testing.T) {
	f := newActionFixture(t)

	created, err := f.dispatchCall(t, FnCreateTask, map[string]any{"title": "Old"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	todoID := created.(map[string]any)["id"].(string)

	beforeUpdates := f.actions.MutationSummary()["updates"]
	_, err = f.dispatchCall(t, FnUpdateTask, map[string]any{
		"task_id":  todoID,
		"priority": "urgent",
	})
	if err != nil {
		t.Fatalf("update_task: %v", err)
	}
	afterUpdates := f.actions.MutationSummary()["updates"]
	if afterUpdates != beforeUpdates+1 {
		t.Errorf("updates = %d, want %d", afterUpdates, beforeUpdates+1)
	}

	// Verify it actually landed.
	parsed, _ := uuid.Parse(todoID)
	tv, _, err := task.NewService(f.pool).Get(context.Background(), f.callerCb(), parsed)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if tv.Priority != "urgent" {
		t.Errorf("priority = %v, want urgent", tv.Priority)
	}
}

func TestActionBuiltins_CompleteTodo_StatusDone(t *testing.T) {
	f := newActionFixture(t)

	created, err := f.dispatchCall(t, FnCreateTask, map[string]any{"title": "Finish dishes"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	todoID := created.(map[string]any)["id"].(string)

	result, err := f.dispatchCall(t, FnCompleteTask, map[string]any{
		"task_id": todoID,
		"note":    "all clean",
	})
	if err != nil {
		t.Fatalf("complete_task: %v", err)
	}
	m := result.(map[string]any)
	if m["status"] != "done" {
		t.Errorf("status = %v, want done", m["status"])
	}
	if m["id"] != todoID {
		t.Errorf("id mismatch: %v vs %v", m["id"], todoID)
	}
}

func TestActionBuiltins_AddTodoComment(t *testing.T) {
	f := newActionFixture(t)

	created, err := f.dispatchCall(t, FnCreateTask, map[string]any{"title": "Do stuff"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	todoID := created.(map[string]any)["id"].(string)

	result, err := f.dispatchCall(t, FnAddTaskComment, map[string]any{
		"task_id": todoID,
		"content": "Working on it now",
	})
	if err != nil {
		t.Fatalf("add_comment: %v", err)
	}
	if result.(map[string]any)["id"] != todoID {
		t.Errorf("comment id didn't echo task_id")
	}

	// Confirm a comment row landed in the activity log.
	parsedTodoID, _ := uuid.Parse(todoID)
	var n int
	err = f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM app_task_events WHERE tenant_id = $1 AND task_id = $2 AND event_type = 'comment' AND content = 'Working on it now'`,
		f.tenant.ID, parsedTodoID).Scan(&n)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Errorf("comment rows = %d, want 1", n)
	}
}

func TestActionBuiltins_CreateDecision_TwoOptions(t *testing.T) {
	f := newActionFixture(t)

	result, err := f.dispatchCall(t, FnCreateDecision, map[string]any{
		"title": "Swap kegs?",
		"body":  "The IPA is running low.",
		"options": []any{
			map[string]any{"label": "Swap now", "prompt": "Replace the keg."},
			map[string]any{"label": "Wait", "prompt": ""},
		},
		"priority": "high",
	})
	if err != nil {
		t.Fatalf("create_decision: %v", err)
	}
	m := result.(map[string]any)
	if _, err := uuid.Parse(m["id"].(string)); err != nil {
		t.Errorf("id is not a UUID: %v", err)
	}
	if m["kind"] != "decision" {
		t.Errorf("kind = %v", m["kind"])
	}
	if m["priority"] != "high" {
		t.Errorf("priority = %v", m["priority"])
	}
	opts, ok := m["options"].([]any)
	if !ok || len(opts) != 2 {
		t.Fatalf("options = %v", m["options"])
	}
	opt0 := opts[0].(map[string]any)
	if opt0["label"] != "Swap now" || opt0["option_id"] != "opt-1" {
		t.Errorf("opt0 = %v", opt0)
	}
}

func TestActionBuiltins_CreateBriefing_Severity(t *testing.T) {
	f := newActionFixture(t)

	result, err := f.dispatchCall(t, FnCreateBriefing, map[string]any{
		"title":    "Nightly summary",
		"body":     "The bar pulled $1,200 last night.",
		"severity": "notable",
	})
	if err != nil {
		t.Fatalf("create_briefing: %v", err)
	}
	m := result.(map[string]any)
	if m["kind"] != "briefing" {
		t.Errorf("kind = %v", m["kind"])
	}
	if m["severity"] != "notable" {
		t.Errorf("severity = %v", m["severity"])
	}
}

func TestActionBuiltins_CreateTask(t *testing.T) {
	f := newActionFixture(t)

	result, err := f.dispatchCall(t, FnCreateJob, map[string]any{
		"description": "Remind me to restock",
		"cron":        "0 9 * * 1",
	})
	if err != nil {
		t.Fatalf("create_task: %v", err)
	}
	m := result.(map[string]any)
	if _, err := uuid.Parse(m["id"].(string)); err != nil {
		t.Errorf("task id not a uuid: %v", err)
	}
	if m["cron_expr"] != "0 9 * * 1" {
		t.Errorf("cron_expr = %v", m["cron_expr"])
	}
}

func TestActionBuiltins_AddMemory_Tenant(t *testing.T) {
	f := newActionFixture(t)

	_, err := f.dispatchCall(t, FnAddMemory, map[string]any{
		"content":    "Jane prefers oat milk",
		"scope_type": "tenant",
	})
	if err != nil {
		t.Fatalf("add_memory: %v", err)
	}

	// Verify a tenant-scoped row landed. After the scopes refactor, tenant-
	// wide scope means the joined scopes row has role_id IS NULL AND
	// user_id IS NULL.
	var n int
	err = f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM memories m
		   JOIN scopes s ON s.id = m.scope_id
		  WHERE m.tenant_id = $1
		    AND s.role_id IS NULL AND s.user_id IS NULL
		    AND m.content = 'Jane prefers oat milk'`,
		f.tenant.ID).Scan(&n)
	if err != nil {
		t.Fatalf("count memories: %v", err)
	}
	if n != 1 {
		t.Errorf("memory rows = %d, want 1", n)
	}
}

func TestActionBuiltins_FindUser_ByDisplayName(t *testing.T) {
	f := newActionFixture(t)

	// Seed a second user with a unique display name.
	_, err := models.GetOrCreateUser(context.Background(), f.pool, f.tenant.ID,
		"U_jane_"+uuid.NewString()[:6], "Jane Cooper", "")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	result, err := f.dispatchCall(t, FnFindUser, map[string]any{
		"name_or_mention": "Jane Cooper",
	})
	if err != nil {
		t.Fatalf("find_user: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %T, want map", result)
	}
	if m["display_name"] != "Jane Cooper" {
		t.Errorf("display_name = %v", m["display_name"])
	}
	if _, err := uuid.Parse(m["id"].(string)); err != nil {
		t.Errorf("id not a uuid: %v", err)
	}
}

func TestActionBuiltins_FindUser_UnknownReturnsNil(t *testing.T) {
	f := newActionFixture(t)

	result, err := f.dispatchCall(t, FnFindUser, map[string]any{
		"name_or_mention": "nobody-matches-this-" + uuid.NewString()[:6],
	})
	if err != nil {
		t.Fatalf("find_user: %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestActionBuiltins_TenantIsolation(t *testing.T) {
	fA := newActionFixture(t)
	fB := newActionFixture(t)

	// A creates a todo.
	createdA, err := fA.dispatchCall(t, FnCreateTask, map[string]any{"title": "A's todo"})
	if err != nil {
		t.Fatalf("A create: %v", err)
	}
	todoAID, _ := uuid.Parse(createdA.(map[string]any)["id"].(string))

	// B's caller should not be able to see / update A's todo.
	_, err = fB.dispatchCall(t, FnUpdateTask, map[string]any{
		"task_id":  todoAID.String(),
		"priority": "urgent",
	})
	if err == nil {
		t.Fatal("B was able to update A's todo; tenant isolation broken")
	}
}

func TestActionBuiltins_ScriptEndToEnd_FindUserThenCreateTodo(t *testing.T) {
	f := newActionFixture(t)

	// Seed a user to find.
	_, err := models.GetOrCreateUser(context.Background(), f.pool, f.tenant.ID,
		"U_jane_"+uuid.NewString()[:6], "Jane E2E", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx := context.Background()
	src := `
def main():
    uid = find_user("Jane E2E")
    if uid is None:
        raise Exception("find_user returned None")
    todo = create_task(title="Hand-off to Jane", assignee=uid["id"])
    return todo["id"]
`
	mod, err := testEngine.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	caps := &runtime.Capabilities{
		BuiltIns:      f.actions.BuiltIns,
		BuiltInParams: f.actions.Params,
		RunID:         uuid.New(),
	}
	result, _, err := testEngine.Run(ctx, mod, "main", nil, caps)
	if err != nil {
		t.Fatalf("script run: %v", err)
	}
	idStr, ok := result.(string)
	if !ok {
		t.Fatalf("result = %T, want string", result)
	}
	if _, err := uuid.Parse(idStr); err != nil {
		t.Errorf("returned id is not a uuid: %v", err)
	}
}
