package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// freshTenant creates a tenant + admin user and returns their IDs.
// Also seeds the tenant's default ('member') role so vault tests that
// scope to "everyone in tenant" can resolve a real role uuid — the
// production OAuth path does this in slack/oauth.go.
func freshTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, userID uuid.UUID) {
	t.Helper()
	teamID := "T_vault_" + uuid.NewString()
	slug := models.SanitizeSlug("vault-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "vault-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	memberRole, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleMember, "")
	if err != nil {
		t.Fatalf("creating member role: %v", err)
	}
	if err := models.SetDefaultRole(ctx, pool, tenant.ID, &memberRole.ID); err != nil {
		t.Fatalf("setting default role: %v", err)
	}
	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_"+uuid.NewString()[:8], "Admin", "")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	return tenant.ID, user.ID
}

func adminCaller(tenantID, userID uuid.UUID, roleIDs ...uuid.UUID) *services.Caller {
	return &services.Caller{
		TenantID: tenantID,
		UserID:   userID,
		IsAdmin:  true,
		Roles:    []string{"admin"},
		RoleIDs:  roleIDs,
	}
}

// memberRoleID returns the tenant's default ('member') role uuid —
// the canonical "everyone in tenant" target for vault-entry scoping.
func memberRoleID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	tenant, err := models.GetTenantByID(ctx, pool, tenantID)
	if err != nil || tenant == nil || tenant.DefaultRoleID == nil {
		t.Fatalf("loading tenant default role: %v", err)
	}
	return *tenant.DefaultRoleID
}

func randHash(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func okSetupParams(t *testing.T) SetupParams {
	t.Helper()
	return SetupParams{
		KDFParams:            json.RawMessage(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:             randHash(t),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
	}
}

// seedVaultTenant sets up a tenant vault with a known auth_hash. Returns
// the auth_hash so callers can unlock against it without going through
// the full SetupVault path.
func seedVaultTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, adminID uuid.UUID) []byte {
	t.Helper()
	knownHash := randHash(t)
	if err := models.InitVaultTenant(ctx, pool, models.VaultSetupParams{
		TenantID:             tenantID,
		KDFParams:            json.RawMessage(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:             knownHash,
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
		SetupByUserID:        adminID,
	}); err != nil {
		t.Fatalf("seed vault tenant: %v", err)
	}
	return knownHash
}

// ===== Setup =====

func TestSetupVaultAdminOnly(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, userID := freshTenant(t, ctx, pool)
	nonAdmin := &services.Caller{TenantID: tenantID, UserID: userID, IsAdmin: false}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	err := svc.SetupVault(ctx, nonAdmin, okSetupParams(t), svc.AuditFromRequest(nonAdmin, r))
	if !errors.Is(err, services.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestSetupVaultRefusesDuplicate(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	admin := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := svc.SetupVault(ctx, admin, okSetupParams(t), svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	err := svc.SetupVault(ctx, admin, okSetupParams(t), svc.AuditFromRequest(admin, r))
	if !errors.Is(err, models.ErrVaultAlreadySetup) {
		t.Fatalf("expected ErrVaultAlreadySetup, got %v", err)
	}
}

func TestSetupVaultRejectsWeakKDF(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	admin := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	p := okSetupParams(t)
	p.KDFParams = json.RawMessage(`{"algo":"pbkdf2-sha256","iterations":100,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`)
	err := svc.SetupVault(ctx, admin, p, svc.AuditFromRequest(admin, r))
	if err == nil {
		t.Fatal("expected KDF-too-weak rejection")
	}
}

// ===== Unlock =====

func TestUnlockBeforeSetupReturnsNotSetUp(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, userID := freshTenant(t, ctx, pool)
	c := &services.Caller{TenantID: tenantID, UserID: userID}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	_, err := svc.Unlock(ctx, c, randHash(t), svc.AuditFromRequest(c, r))
	if !errors.Is(err, ErrUnlockNotSetUp) {
		t.Fatalf("expected ErrUnlockNotSetUp, got %v", err)
	}
}

func TestUnlockSucceedsAfterSetup(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	knownHash := seedVaultTenant(t, ctx, pool, tenantID, adminID)

	c := &services.Caller{TenantID: tenantID, UserID: adminID}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	res, err := svc.Unlock(ctx, c, knownHash, svc.AuditFromRequest(c, r))
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if res.VaultGeneration != 1 {
		t.Errorf("vault_generation = %d, want 1", res.VaultGeneration)
	}
	if len(res.WrappedVaultKey) != 48 {
		t.Errorf("wrapped_vault_key length = %d, want 48", len(res.WrappedVaultKey))
	}
	if res.TenantIDBytes == "" {
		t.Error("tenant_id_bytes should be set")
	}
}

func TestUnlockWrongPasswordReturnsMismatch(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	_ = seedVaultTenant(t, ctx, pool, tenantID, adminID)

	c := &services.Caller{TenantID: tenantID, UserID: adminID}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	_, err := svc.Unlock(ctx, c, make([]byte, 32), svc.AuditFromRequest(c, r))
	if !errors.Is(err, ErrUnlockMismatch) {
		t.Fatalf("expected ErrUnlockMismatch, got %v", err)
	}
}

// A wrong master password must surface as 403 — NEVER 401 — so the
// authenticated console client shows "incorrect password" inline instead
// of mistaking it for a lapsed web session and bouncing the user to the
// login page. (handleUnlock only runs behind requireCaller, so a genuine
// session failure is a 401 from the middleware and never reaches here.)
func TestHandleUnlockWrongPasswordReturns403(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, adminID := freshTenant(t, ctx, pool)
	_ = seedVaultTenant(t, ctx, pool, tenantID, adminID)

	a := &App{pool: pool, svc: NewService(pool)}
	c := &services.Caller{TenantID: tenantID, UserID: adminID}
	body := `{"auth_hash":"` + base64.StdEncoding.EncodeToString(make([]byte, 32)) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r = r.WithContext(auth.WithCaller(ctx, c))
	w := httptest.NewRecorder()

	a.handleUnlock(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding body %q: %v", w.Body.String(), err)
	}
	if resp.Error == "" {
		t.Errorf("expected a human-readable error message, got empty body")
	}
}

// The happy path still returns 200 with the wrapped-key payload.
func TestHandleUnlockCorrectPasswordReturns200(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, adminID := freshTenant(t, ctx, pool)
	knownHash := seedVaultTenant(t, ctx, pool, tenantID, adminID)

	a := &App{pool: pool, svc: NewService(pool)}
	c := &services.Caller{TenantID: tenantID, UserID: adminID}
	body := `{"auth_hash":"` + base64.StdEncoding.EncodeToString(knownHash) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r = r.WithContext(auth.WithCaller(ctx, c))
	w := httptest.NewRecorder()

	a.handleUnlock(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

// ===== Rotate =====

func TestRotateBumpsGenerationAndPreservesEntries(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	knownHash := seedVaultTenant(t, ctx, pool, tenantID, adminID)

	// Need to be admin and to have a recent unlock for step-up auth.
	memberID := memberRoleID(t, ctx, pool, tenantID)
	admin := adminCaller(tenantID, adminID, memberID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, err := svc.Unlock(ctx, admin, knownHash, svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	// Create an entry under the original vault.
	if _, err := svc.CreateEntry(ctx, admin, CreateEntryParams{
		Title:           "preserved across rotate",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      randBytes(t, 12),
		RoleID:          &memberID,
	}, svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("create entry: %v", err)
	}

	newGen, err := svc.RotateVaultPassword(ctx, admin, RotateParams{
		KDFParams:            json.RawMessage(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"BBBBBBBBBBBBBBBBBBBBBA=="}`),
		AuthHash:             randHash(t),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
	}, svc.AuditFromRequest(admin, r))
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newGen != 2 {
		t.Errorf("vault_generation after rotate = %d, want 2", newGen)
	}

	// Entry survives.
	rows, err := svc.ListEntries(ctx, admin, "", "", nil, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 entry after rotate, got %d", len(rows))
	}
}

func TestRotateRequiresStepUp(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	_ = seedVaultTenant(t, ctx, pool, tenantID, adminID)

	// Admin caller but no recent unlock audit row.
	admin := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	_, err := svc.RotateVaultPassword(ctx, admin, RotateParams{
		KDFParams:            json.RawMessage(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:             randHash(t),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
	}, svc.AuditFromRequest(admin, r))
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("expected ErrStepUpRequired, got %v", err)
	}
}

func TestRotateAdminOnly(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	_ = seedVaultTenant(t, ctx, pool, tenantID, adminID)

	nonAdmin := &services.Caller{TenantID: tenantID, UserID: adminID, IsAdmin: false}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	_, err := svc.RotateVaultPassword(ctx, nonAdmin, RotateParams{
		KDFParams:            json.RawMessage(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:             randHash(t),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
	}, svc.AuditFromRequest(nonAdmin, r))
	if !errors.Is(err, services.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// ===== Nuke =====

func TestNukeDestroysVaultAndEntries(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	rec := &recordingCardSurface{}
	svc.cards = rec
	tenantID, adminID := freshTenant(t, ctx, pool)
	_ = seedVaultTenant(t, ctx, pool, tenantID, adminID)

	memberID := memberRoleID(t, ctx, pool, tenantID)
	admin := adminCaller(tenantID, adminID, memberID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	for i := range 3 {
		if _, err := svc.CreateEntry(ctx, admin, CreateEntryParams{
			Title:           fmt.Sprintf("e%d", i),
			ValueCiphertext: []byte("ct"),
			ValueNonce:      randBytes(t, 12),
			RoleID:          &memberID,
		}, svc.AuditFromRequest(admin, r)); err != nil {
			t.Fatalf("create entry: %v", err)
		}
	}

	count, err := svc.NukeVault(ctx, admin, svc.AuditFromRequest(admin, r))
	if err != nil {
		t.Fatalf("nuke: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 entries destroyed, got %d", count)
	}

	// Vault tenant row is gone.
	v, _ := models.GetVaultTenant(ctx, pool, tenantID)
	if v != nil {
		t.Error("vault tenant row should be gone after nuke")
	}

	// Briefing fired.
	if rec.briefings != 1 {
		t.Errorf("expected 1 briefing after nuke, got %d", rec.briefings)
	}
}

func TestNukeAdminOnly(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	_ = seedVaultTenant(t, ctx, pool, tenantID, adminID)

	nonAdmin := &services.Caller{TenantID: tenantID, UserID: adminID, IsAdmin: false}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	_, err := svc.NukeVault(ctx, nonAdmin, svc.AuditFromRequest(nonAdmin, r))
	if !errors.Is(err, services.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// ===== Step-up =====

func TestStepUpRequiredWithoutRecentUnlock(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	_ = seedVaultTenant(t, ctx, pool, tenantID, adminID)

	admin := adminCaller(tenantID, adminID)
	err := svc.requireRecentUnlock(ctx, admin)
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("expected ErrStepUpRequired, got %v", err)
	}
}

func TestStepUpSucceedsWithRecentUnlock(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	knownHash := seedVaultTenant(t, ctx, pool, tenantID, adminID)

	admin := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, err := svc.Unlock(ctx, admin, knownHash, svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if err := svc.requireRecentUnlock(ctx, admin); err != nil {
		t.Errorf("requireRecentUnlock after unlock: %v", err)
	}
}

func TestStepUpFailsWhenTenantLocked(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	knownHash := seedVaultTenant(t, ctx, pool, tenantID, adminID)

	admin := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, err := svc.Unlock(ctx, admin, knownHash, svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	// Administratively lock the tenant.
	if _, err := pool.Exec(ctx,
		`UPDATE app_vault_tenants SET locked_until = now() + interval '1 hour' WHERE tenant_id = $1`,
		tenantID,
	); err != nil {
		t.Fatalf("setting locked_until: %v", err)
	}
	err := svc.requireRecentUnlock(ctx, admin)
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("expected ErrStepUpRequired when tenant locked, got %v", err)
	}
}

// ===== Entry CRUD invariants =====

func TestPUTMassAssignmentIgnoresOwnerUserID(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, ownerID := freshTenant(t, ctx, pool)
	memberID := memberRoleID(t, ctx, pool, tenantID)
	owner := adminCaller(tenantID, ownerID, memberID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	id, err := svc.CreateEntry(ctx, owner, CreateEntryParams{
		Title:           "test",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      make([]byte, 12),
		RoleID:          &memberID,
	}, svc.AuditFromRequest(owner, r))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := svc.UpdateEntry(ctx, owner, id, UpdateEntryParams{
		Title:           "renamed",
		ValueCiphertext: []byte("ct2"),
		ValueNonce:      make([]byte, 12),
	}, svc.AuditFromRequest(owner, r)); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := models.GetVaultEntry(ctx, pool, tenantID, id, ownerID, nil)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.OwnerUserID != ownerID {
		t.Fatalf("owner_user_id changed: got %s want %s", got.OwnerUserID, ownerID)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)

	tenantA, ownerA := freshTenant(t, ctx, pool)
	tenantB, ownerB := freshTenant(t, ctx, pool)
	rA := httptest.NewRequest(http.MethodPost, "/", nil)
	rB := httptest.NewRequest(http.MethodPost, "/", nil)

	memberA := memberRoleID(t, ctx, pool, tenantA)
	memberB := memberRoleID(t, ctx, pool, tenantB)
	callerA := adminCaller(tenantA, ownerA, memberA)
	callerB := adminCaller(tenantB, ownerB, memberB)

	idA, err := svc.CreateEntry(ctx, callerA, CreateEntryParams{
		Title:           "tenant A only",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      make([]byte, 12),
		RoleID:          &memberA,
	}, svc.AuditFromRequest(callerA, rA))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.GetEntry(ctx, callerB, idA, svc.AuditFromRequest(callerB, rB))
	if !errors.Is(err, models.ErrNotFound) {
		t.Fatalf("cross-tenant get: expected ErrNotFound, got %v", err)
	}

	err = svc.UpdateEntry(ctx, callerB, idA, UpdateEntryParams{
		Title: "hijacked", ValueCiphertext: []byte("ct"), ValueNonce: make([]byte, 12),
	}, svc.AuditFromRequest(callerB, rB))
	if !errors.Is(err, models.ErrNotFound) {
		t.Fatalf("cross-tenant update: expected ErrNotFound, got %v", err)
	}

	err = svc.DeleteEntry(ctx, callerB, idA, svc.AuditFromRequest(callerB, rB))
	if !errors.Is(err, models.ErrNotFound) {
		t.Fatalf("cross-tenant delete: expected ErrNotFound, got %v", err)
	}

	rows, err := svc.ListEntries(ctx, callerB, "", "", nil, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range rows {
		if e.ID == idA {
			t.Fatalf("tenant B saw a tenant A entry")
		}
	}
}

// TestSetEntryRoleNonMemberRejected verifies a non-admin can't re-scope
// an entry to a role they don't belong to.
func TestSetEntryRoleNonMemberRejected(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, userID := freshTenant(t, ctx, pool)
	_ = seedVaultTenant(t, ctx, pool, tenantID, userID)
	memberID := memberRoleID(t, ctx, pool, tenantID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	// A role the caller is NOT a member of.
	otherRole, err := models.GetOrCreateRole(ctx, pool, tenantID, "engineering", "")
	if err != nil {
		t.Fatalf("creating role: %v", err)
	}

	// Non-admin caller holding only the implicit member role.
	caller := &services.Caller{TenantID: tenantID, UserID: userID, IsAdmin: false, RoleIDs: []uuid.UUID{memberID}}
	id, err := svc.CreateEntry(ctx, caller, CreateEntryParams{
		Title:           "secret",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      make([]byte, 12),
		RoleID:          &memberID,
	}, svc.AuditFromRequest(caller, r))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = svc.SetEntryRole(ctx, caller, id, &otherRole.ID, svc.AuditFromRequest(caller, r))
	if !errors.Is(err, ErrCallerNotInRole) {
		t.Fatalf("expected ErrCallerNotInRole, got %v", err)
	}
}

// TestSetEntryRoleAdminBypass verifies an admin can re-scope an entry to
// a role they don't belong to. Cross-role moves still require a recent
// unlock, so the admin unlocks first.
func TestSetEntryRoleAdminBypass(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	knownHash := seedVaultTenant(t, ctx, pool, tenantID, adminID)
	memberID := memberRoleID(t, ctx, pool, tenantID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	otherRole, err := models.GetOrCreateRole(ctx, pool, tenantID, "engineering", "")
	if err != nil {
		t.Fatalf("creating role: %v", err)
	}

	// Admin holds only the implicit member role, not 'engineering'.
	admin := adminCaller(tenantID, adminID, memberID)
	id, err := svc.CreateEntry(ctx, admin, CreateEntryParams{
		Title:           "secret",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      make([]byte, 12),
		RoleID:          &memberID,
	}, svc.AuditFromRequest(admin, r))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.Unlock(ctx, admin, knownHash, svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	if err := svc.SetEntryRole(ctx, admin, id, &otherRole.ID, svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("admin re-scope to non-member role: %v", err)
	}

	got, err := models.GetVaultEntry(ctx, pool, tenantID, id, adminID, []uuid.UUID{memberID})
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.RoleID == nil || *got.RoleID != otherRole.ID {
		t.Fatalf("role not updated: got %v want %s", got.RoleID, otherRole.ID)
	}
}

func TestNonceUniquenessAcrossEntries(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, ownerID := freshTenant(t, ctx, pool)
	memberID := memberRoleID(t, ctx, pool, tenantID)
	owner := adminCaller(tenantID, ownerID, memberID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	for i := range 10 {
		nonce := make([]byte, 12)
		if _, err := rand.Read(nonce); err != nil {
			t.Fatalf("rand: %v", err)
		}
		_, err := svc.CreateEntry(ctx, owner, CreateEntryParams{
			Title:           fmt.Sprintf("e%d", i),
			ValueCiphertext: []byte("ct"),
			ValueNonce:      nonce,
			RoleID:          &memberID,
		}, svc.AuditFromRequest(owner, r))
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	var dups int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT value_nonce FROM app_vault_entries WHERE tenant_id = $1
			GROUP BY value_nonce HAVING count(*) > 1
		) d
	`, tenantID).Scan(&dups); err != nil {
		t.Fatalf("dup check: %v", err)
	}
	if dups != 0 {
		t.Fatalf("found %d duplicate nonces; should be 0", dups)
	}
}

// ===== Helpers / misc =====

// recordingCardSurface is a no-op CardSurface that counts each kind
// of card created and remembers their inputs.
type recordingCardSurface struct {
	decisions      int
	briefings      int
	decisionInputs []CardCreateInput
	briefingInputs []CardCreateInput
}

func (r *recordingCardSurface) CreateDecision(_ context.Context, _ uuid.UUID, in CardCreateInput) error {
	r.decisions++
	r.decisionInputs = append(r.decisionInputs, in)
	return nil
}

func (r *recordingCardSurface) CreateBriefing(_ context.Context, _ uuid.UUID, in CardCreateInput) error {
	r.briefings++
	r.briefingInputs = append(r.briefingInputs, in)
	return nil
}

func TestSanitizeMarkdownInlineRemovesDangerousChars(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"normal name", "normal name"},
		{"name with `backtick`", "name with ʼbacktickʼ"},
		{"name with <script>", "name with ‹script›"},
		{"with\nnewline", "with newline"},
	}
	for _, tc := range cases {
		got := sanitizeMarkdownInline(tc.in)
		if got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEncodeTenantIDForAAD(t *testing.T) {
	id := uuid.MustParse("11223344-5566-7788-99aa-bbccddeeff00")
	got := encodeTenantIDForAAD(id)
	want := "1122334455667788" + "99aabbccddeeff00"
	if got != want {
		t.Fatalf("encodeTenantIDForAAD = %q, want %q", got, want)
	}
}
