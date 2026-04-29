// Tests for the per-session exposed-tool plumbing. Each test installs
// a fresh exposedRegistry backed by a stub sessionToolMutator so the
// MCP server doesn't need to be constructed; only the DB-loading path
// (loadTenantTools / loadToolByName) requires a real pool, so we open
// one via testdb and seed minimal rows directly.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// mockSession is a minimal ClientSession + SessionWithTools used by the
// register-hook tests. Captures the tool map the hook installs so tests
// can assert on it.
type mockSession struct {
	id    string
	mu    sync.Mutex
	init  bool
	tools map[string]mcpserver.ServerTool
}

func (s *mockSession) SessionID() string                                   { return s.id }
func (s *mockSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (s *mockSession) Initialize()                                         { s.init = true }
func (s *mockSession) Initialized() bool                                   { return s.init }
func (s *mockSession) GetSessionTools() map[string]mcpserver.ServerTool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tools
}
func (s *mockSession) SetSessionTools(tools map[string]mcpserver.ServerTool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = tools
}

var _ mcpserver.ClientSession = (*mockSession)(nil)
var _ mcpserver.SessionWithTools = (*mockSession)(nil)

// stubMutator captures AddSessionTools / DeleteSessionTools calls so
// tests can assert the publish/revoke fan-out hit the expected session IDs.
type stubMutator struct {
	mu      sync.Mutex
	adds    map[string][]string // sessionID -> tool names added
	deletes map[string][]string // sessionID -> tool names deleted
}

func newStubMutator() *stubMutator {
	return &stubMutator{
		adds:    make(map[string][]string),
		deletes: make(map[string][]string),
	}
}

func (s *stubMutator) AddSessionTools(sessionID string, tools ...mcpserver.ServerTool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tools {
		s.adds[sessionID] = append(s.adds[sessionID], t.Tool.Name)
	}
	return nil
}

func (s *stubMutator) DeleteSessionTools(sessionID string, names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes[sessionID] = append(s.deletes[sessionID], names...)
	return nil
}

// seedExposed creates tenant + builder_app + script + revision + exposed_tools
// rows directly via SQL. Returns the tenant and admin caller. t.Cleanup
// deletes the tenant (cascades).
type exposedFixture struct {
	pool     *pgxpool.Pool
	tenant   *models.Tenant
	caller   *services.Caller
	nonAdmin *services.Caller
}

func seedExposed(t *testing.T, toolName string, visibleToRoles []string) *exposedFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_mcp_" + uuid.NewString()
	slug := models.SanitizeSlug("mcp-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "mcp-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	admin, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_admin_"+uuid.NewString()[:8], "MCP Admin", "")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleAdmin, "admin"); err != nil {
		t.Fatalf("admin role: %v", err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, admin.ID, models.RoleAdmin); err != nil {
		t.Fatalf("assign admin: %v", err)
	}
	nonAdminUser, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_user_"+uuid.NewString()[:8], "MCP User", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	appID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO builder_apps (id, tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, '', $4)
	`, appID, tenant.ID, "mcp-app-"+uuid.NewString()[:6], admin.ID); err != nil {
		t.Fatalf("create builder_app: %v", err)
	}

	scriptID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO scripts (id, tenant_id, builder_app_id, name, description, created_by)
		VALUES ($1, $2, $3, $4, '', $5)
	`, scriptID, tenant.ID, appID, "lookups", admin.ID); err != nil {
		t.Fatalf("create script: %v", err)
	}
	revID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO script_revisions (id, script_id, body, created_by)
		VALUES ($1, $2, $3, $4)
	`, revID, scriptID, "def lookup(name):\n    return {'name': name}\n", admin.ID); err != nil {
		t.Fatalf("create revision: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE scripts SET current_rev_id = $1 WHERE id = $2
	`, revID, scriptID); err != nil {
		t.Fatalf("set current_rev_id: %v", err)
	}

	schema, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}})
	roles := visibleToRoles
	if roles == nil {
		roles = []string{}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO exposed_tools (tenant_id, tool_name, script_id, fn_name, description, args_schema, visible_to_roles, is_stale, created_by)
		VALUES ($1, $2, $3, 'lookup', 'Look up a name', $4, $5, false, $6)
	`, tenant.ID, toolName, scriptID, schema, roles, admin.ID); err != nil {
		t.Fatalf("insert exposed_tools: %v", err)
	}

	return &exposedFixture{
		pool:     pool,
		tenant:   tenant,
		caller:   &services.Caller{TenantID: tenant.ID, UserID: admin.ID, IsAdmin: true, Roles: []string{"admin"}},
		nonAdmin: &services.Caller{TenantID: tenant.ID, UserID: nonAdminUser.ID, IsAdmin: false},
	}
}

func newTestRegistry(pool *pgxpool.Pool, server sessionToolMutator) *exposedRegistry {
	return &exposedRegistry{
		server: server,
		pool:   pool,
		invoke: func(ctx context.Context, c *services.Caller, name string, args map[string]any) (string, error) {
			return "{}", nil
		},
		sessionTenant:  make(map[string]uuid.UUID),
		tenantSessions: make(map[uuid.UUID]map[string]struct{}),
	}
}

// injectCallerCtx builds a ctx with the Caller installed under the
// same key auth.CallerFromContext reads.
func injectCallerCtx(caller *services.Caller) context.Context {
	return auth.WithCaller(context.Background(), caller)
}

func TestOnRegisterSession_LoadsTenantTools(t *testing.T) {
	f := seedExposed(t, "lookup", nil)
	reg := newTestRegistry(f.pool, newStubMutator())
	prev := currentExposedRegistry
	resetExposedRegistryForTest(reg)
	t.Cleanup(func() { resetExposedRegistryForTest(prev) })

	sess := &mockSession{id: "sess-a", init: true}
	onRegisterSession(injectCallerCtx(f.caller), sess)

	tools := sess.GetSessionTools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1; got %v", len(tools), tools)
	}
	if _, ok := tools["lookup"]; !ok {
		t.Fatalf("expected tool 'lookup' in map, got keys %v", toolKeys(tools))
	}

	if got := reg.sessionIDsForTenant(f.tenant.ID); len(got) != 1 || got[0] != "sess-a" {
		t.Errorf("tracked session IDs = %v, want [sess-a]", got)
	}
}

func TestOnRegisterSession_FiltersByRole(t *testing.T) {
	f := seedExposed(t, "managerOnly", []string{"manager"})
	reg := newTestRegistry(f.pool, newStubMutator())
	resetExposedRegistryForTest(reg)
	t.Cleanup(func() { resetExposedRegistryForTest(nil) })

	// Non-admin without the role: tool hidden.
	sess := &mockSession{id: "sess-plain", init: true}
	onRegisterSession(injectCallerCtx(f.nonAdmin), sess)
	if got := sess.GetSessionTools(); len(got) != 0 {
		t.Fatalf("plain user should see 0 tools, got %v", toolKeys(got))
	}

	// Non-admin with the role: tool visible.
	withRole := *f.nonAdmin
	withRole.Roles = []string{"manager"}
	sess2 := &mockSession{id: "sess-mgr", init: true}
	onRegisterSession(injectCallerCtx(&withRole), sess2)
	if got := sess2.GetSessionTools(); len(got) != 1 {
		t.Fatalf("manager should see 1 tool, got %v", toolKeys(got))
	}

	// Admin: bypass.
	sess3 := &mockSession{id: "sess-admin", init: true}
	onRegisterSession(injectCallerCtx(f.caller), sess3)
	if got := sess3.GetSessionTools(); len(got) != 1 {
		t.Fatalf("admin should see 1 tool (bypass), got %v", toolKeys(got))
	}
}

func TestPublishExposedTool_FansOutToTenantSessions(t *testing.T) {
	f := seedExposed(t, "shared", nil)
	stub := newStubMutator()
	reg := newTestRegistry(f.pool, stub)
	resetExposedRegistryForTest(reg)
	t.Cleanup(func() { resetExposedRegistryForTest(nil) })

	// Register two sessions for tenant A and one for a different tenant.
	aSess1 := &mockSession{id: "a-1", init: true}
	aSess2 := &mockSession{id: "a-2", init: true}
	onRegisterSession(injectCallerCtx(f.caller), aSess1)
	onRegisterSession(injectCallerCtx(f.caller), aSess2)

	// Tenant B: a second seed (needs its own tool row so the register
	// hook tracks the session).
	seedOther := seedExposed(t, "other-side", nil)
	otherSess := &mockSession{id: "b-1", init: true}
	onRegisterSession(injectCallerCtx(seedOther.caller), otherSess)

	if err := PublishExposedTool(context.Background(), f.caller, "shared"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if got := stub.adds["a-1"]; len(got) != 1 || got[0] != "shared" {
		t.Errorf("a-1 adds = %v, want [shared]", got)
	}
	if got := stub.adds["a-2"]; len(got) != 1 || got[0] != "shared" {
		t.Errorf("a-2 adds = %v, want [shared]", got)
	}
	if got := stub.adds["b-1"]; len(got) != 0 {
		t.Errorf("b-1 should not receive tenant A's tool, got %v", got)
	}
}

func TestRevokeExposedTool_RemovesFromTenantSessions(t *testing.T) {
	f := seedExposed(t, "to-revoke", nil)
	stub := newStubMutator()
	reg := newTestRegistry(f.pool, stub)
	resetExposedRegistryForTest(reg)
	t.Cleanup(func() { resetExposedRegistryForTest(nil) })

	sess := &mockSession{id: "only", init: true}
	onRegisterSession(injectCallerCtx(f.caller), sess)

	if err := RevokeExposedTool(context.Background(), f.caller, "to-revoke"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if got := stub.deletes["only"]; len(got) != 1 || got[0] != "to-revoke" {
		t.Errorf("deletes[only] = %v, want [to-revoke]", got)
	}
}

func TestOnUnregisterSession_ClearsFanoutMap(t *testing.T) {
	f := seedExposed(t, "cleanup", nil)
	reg := newTestRegistry(f.pool, newStubMutator())
	resetExposedRegistryForTest(reg)
	t.Cleanup(func() { resetExposedRegistryForTest(nil) })

	sess := &mockSession{id: "leaving", init: true}
	onRegisterSession(injectCallerCtx(f.caller), sess)
	if got := reg.sessionIDsForTenant(f.tenant.ID); len(got) != 1 {
		t.Fatalf("pre-unregister tracked = %d, want 1", len(got))
	}

	onUnregisterSession(context.Background(), sess)
	if got := reg.sessionIDsForTenant(f.tenant.ID); len(got) != 0 {
		t.Errorf("post-unregister tracked = %v, want []", got)
	}
}

func toolKeys(m map[string]mcpserver.ServerTool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

var _ = fmt.Sprintf
