package vault

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// seedActiveVault inserts a fully-active vault_users row for (tenant,user)
// as the bootstrap initiator (granted_by_user_id NULL). At most one such
// row per tenant is allowed by idx_app_vault_first_user — for additional
// teammates use seedGrantedVault.
func seedActiveVault(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) {
	t.Helper()
	if err := models.RegisterVaultUser(ctx, pool, models.VaultRegisterParams{
		TenantID:                 tenantID,
		UserID:                   userID,
		KDFParams:                json.RawMessage(`{"algo":"argon2id","v":19,"m":65536,"t":3,"p":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:                 randHash(t),
		UserPublicKey:            genRSAPubKey(t),
		UserPrivateKeyCiphertext: fakePrivCT(),
		UserPrivateKeyNonce:      make([]byte, 12),
		WrappedVaultKey:          []byte("wrapped"),
	}); err != nil {
		t.Fatalf("seed register: %v", err)
	}
	if err := models.MarkVaultUserActive(ctx, pool, tenantID, userID); err != nil {
		t.Fatalf("seed activate: %v", err)
	}
}

// seedGrantedVault seeds an active vault_users row for a teammate who was
// granted access by another user. Bypasses RegisterVaultUser's bootstrap
// path so multiple seeded users coexist without tripping the partial-
// unique idx_app_vault_first_user index.
func seedGrantedVault(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID, granterID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO app_vault_users
			(tenant_id, user_id, kdf_params, auth_hash, user_public_key,
			 user_private_key_ciphertext, user_private_key_nonce,
			 wrapped_vault_key, granted_by_user_id, granted_at, pending)
		VALUES ($1, $2, '{}'::jsonb, $3, $4, $5, $6, $7, $8, now(), FALSE)
	`, tenantID, userID, randHash(t), genRSAPubKey(t),
		fakePrivCT(), make([]byte, 12), []byte("wrapped"), granterID)
	if err != nil {
		t.Fatalf("seed granted vault user: %v", err)
	}
}

func TestAdminResetRequiresAdmin(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	target, err := models.GetOrCreateUser(ctx, pool, tenantID, "U_target_"+uuid.NewString()[:8], "Target", "")
	if err != nil {
		t.Fatalf("creating target user: %v", err)
	}

	nonAdmin := &services.Caller{TenantID: tenantID, UserID: adminID, IsAdmin: false}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	err = svc.AdminResetVaultUser(ctx, nonAdmin, target.ID, svc.AuditFromRequest(nonAdmin, r))
	if !errors.Is(err, services.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-admin, got %v", err)
	}
}

func TestAdminResetForbidsSelf(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)

	c := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	err := svc.AdminResetVaultUser(ctx, c, adminID, svc.AuditFromRequest(c, r))
	if err == nil {
		t.Fatal("expected error when admin resets own vault")
	}
}

func TestAdminResetMissingTarget(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)

	c := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	err := svc.AdminResetVaultUser(ctx, c, uuid.New(), svc.AuditFromRequest(c, r))
	if !errors.Is(err, models.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing row, got %v", err)
	}
}

func TestAdminResetWipesRow(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	rec := &recordingCardSurface{}
	svc.cards = rec
	tenantID, adminID := freshTenant(t, ctx, pool)

	seedActiveVault(t, ctx, pool, tenantID, adminID)
	target, err := models.GetOrCreateUser(ctx, pool, tenantID, "U_target_"+uuid.NewString()[:8], "Target Name", "")
	if err != nil {
		t.Fatalf("creating target user: %v", err)
	}
	seedGrantedVault(t, ctx, pool, tenantID, target.ID, adminID)

	c := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := svc.AdminResetVaultUser(ctx, c, target.ID, svc.AuditFromRequest(c, r)); err != nil {
		t.Fatalf("AdminResetVaultUser: %v", err)
	}

	// Row gone.
	v, err := models.GetVaultUser(ctx, pool, tenantID, target.ID)
	if err != nil {
		t.Fatalf("GetVaultUser: %v", err)
	}
	if v != nil {
		t.Fatal("expected vault_users row to be deleted")
	}

	// Briefing fired on target's stack with Urgent + UserScopes.
	if rec.briefings != 1 {
		t.Fatalf("expected 1 briefing, got %d", rec.briefings)
	}
	b := rec.briefingInputs[0]
	if !b.Urgent {
		t.Errorf("briefing should be Urgent")
	}
	if !slices.Contains(b.UserScopes, target.ID) {
		t.Errorf("briefing UserScopes %v missing target %s", b.UserScopes, target.ID)
	}
	if b.Briefing == nil || b.Briefing.Severity != "important" {
		t.Errorf("briefing severity = %v, want important", b.Briefing)
	}

	// Audit row exists with vault.admin_reset action and admin as actor.
	var actionCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events
		 WHERE tenant_id = $1 AND actor_user_id = $2 AND action = 'vault.admin_reset' AND target_id = $3`,
		tenantID, adminID, target.ID,
	).Scan(&actionCount); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if actionCount != 1 {
		t.Errorf("expected 1 vault.admin_reset audit row, got %d", actionCount)
	}
}

// TestRegisterFromScratchAfterAdminReset is the regression test proving the
// existing fresh-register path handles the post-reset state — no Replace=true
// plumbing is involved because the row is gone.
func TestRegisterFromScratchAfterAdminReset(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	rec := &recordingCardSurface{}
	svc.cards = rec
	tenantID, adminID := freshTenant(t, ctx, pool)

	// Seed admin's own vault row first so the tenant is "initialized" —
	// otherwise the post-reset re-register lands in the bootstrap branch
	// that requires admin (the target is non-admin in this test).
	seedActiveVault(t, ctx, pool, tenantID, adminID)

	target, err := models.GetOrCreateUser(ctx, pool, tenantID, "U_target_"+uuid.NewString()[:8], "Target", "")
	if err != nil {
		t.Fatalf("creating target user: %v", err)
	}
	seedGrantedVault(t, ctx, pool, tenantID, target.ID, adminID)

	admin := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := svc.AdminResetVaultUser(ctx, admin, target.ID, svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("admin reset: %v", err)
	}

	// Now the target re-registers from scratch (no Replace=true).
	targetCaller := &services.Caller{TenantID: tenantID, UserID: target.ID, IsAdmin: false}
	if err := svc.Register(ctx, targetCaller, RegisterParams{
		AuthHash:                 randHash(t),
		KDFParams:                json.RawMessage(`{"algo":"argon2id","v":19,"m":65536,"t":3,"p":1,"salt":"BBBBBBBBBBBBBBBBBBBBBB=="}`),
		UserPublicKey:            genRSAPubKey(t),
		UserPrivateKeyCiphertext: fakePrivCT(),
		UserPrivateKeyNonce:      make([]byte, 12),
		// No WrappedVaultKey: target is non-admin, waits for grant.
	}, svc.AuditFromRequest(targetCaller, r)); err != nil {
		t.Fatalf("re-register after admin reset: %v", err)
	}

	v, err := models.GetVaultUser(ctx, pool, tenantID, target.ID)
	if err != nil || v == nil {
		t.Fatalf("expected re-registered row, got %v err=%v", v, err)
	}
	if !v.Pending {
		t.Errorf("re-registered row should be pending=true")
	}
	if v.WrappedVaultKey != nil {
		t.Errorf("re-registered row should have no wrapped_vault_key (waiting for grant)")
	}
}

// TestRequestVaultResetCreatesAdminCard asserts the request flow mints a
// decision card scoped to the admin role with the gated reset_vault_user
// tool on its approve option.
func TestRequestVaultResetCreatesAdminCard(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	rec := &recordingCardSurface{}
	svc.cards = rec
	tenantID, userID := freshTenant(t, ctx, pool)
	seedActiveVault(t, ctx, pool, tenantID, userID)

	c := &services.Caller{TenantID: tenantID, UserID: userID, IsAdmin: false}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := svc.RequestVaultReset(ctx, c, svc.AuditFromRequest(c, r)); err != nil {
		t.Fatalf("RequestVaultReset: %v", err)
	}

	if rec.decisions != 1 {
		t.Fatalf("expected 1 decision card, got %d", rec.decisions)
	}
	in := rec.decisionInputs[0]
	if !slices.Contains(in.RoleScopes, "admin") {
		t.Errorf("RoleScopes should include 'admin', got %v", in.RoleScopes)
	}
	if in.Decision == nil || len(in.Decision.Options) != 2 {
		t.Fatalf("expected 2 decision options, got %#v", in.Decision)
	}
	approve := in.Decision.Options[0]
	if approve.OptionID != "approve" {
		t.Errorf("first option should be 'approve', got %q", approve.OptionID)
	}
	if approve.ToolName != "reset_vault_user" {
		t.Errorf("approve option ToolName should be reset_vault_user, got %q", approve.ToolName)
	}
	var args struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(approve.Arguments, &args); err != nil {
		t.Fatalf("approve.Arguments JSON: %v (raw: %s)", err, approve.Arguments)
	}
	if args.UserID != userID.String() {
		t.Errorf("approve arg user_id = %q, want %q", args.UserID, userID)
	}

	// Audit row for the request.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events
		 WHERE tenant_id = $1 AND actor_user_id = $2 AND action = 'vault.reset_requested'`,
		tenantID, userID,
	).Scan(&n); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 vault.reset_requested audit row, got %d", n)
	}
}

func TestRequestVaultResetFailsWhenNotRegistered(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	svc.cards = &recordingCardSurface{}
	tenantID, userID := freshTenant(t, ctx, pool)

	c := &services.Caller{TenantID: tenantID, UserID: userID, IsAdmin: false}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	err := svc.RequestVaultReset(ctx, c, svc.AuditFromRequest(c, r))
	if err == nil {
		t.Fatal("expected error when caller has no vault registration")
	}
}
