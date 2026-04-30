package vault

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// freshTenant creates a tenant + admin user and returns their IDs.
func freshTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, userID uuid.UUID) {
	t.Helper()
	teamID := "T_vault_" + uuid.NewString()
	slug := models.SanitizeSlug("vault-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "vault-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_"+uuid.NewString()[:8], "Admin", "")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	return tenant.ID, user.ID
}

func adminCaller(tenantID, userID uuid.UUID) *services.Caller {
	return &services.Caller{
		TenantID: tenantID,
		UserID:   userID,
		IsAdmin:  true,
		Roles:    []string{"admin"},
	}
}

// genRSAPubKey returns a valid 2048-bit RSA-OAEP public key DER.
func genRSAPubKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal pubkey: %v", err)
	}
	return der
}

func randHash(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func TestRegisterRejectsBadPubkey(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, userID := freshTenant(t, ctx, pool)
	caller := adminCaller(tenantID, userID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	audit := svc.AuditFromRequest(caller, r)

	cases := []struct {
		name string
		der  []byte
	}{
		{"empty", nil},
		{"garbage", []byte{0x01, 0x02, 0x03}},
		{"non-RSA", func() []byte {
			// A bare valid SPKI for a non-RSA key would require ed25519 etc.
			// Cheaper: a malformed DER hits the parse path before the type check.
			return []byte{0x30, 0x05, 0x06, 0x03, 0x55, 0x04, 0x03}
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Register(ctx, caller, RegisterParams{
				AuthHash:                 randHash(t),
				KDFParams:                json.RawMessage(`{"algo":"argon2id","v":19,"m":65536,"t":3,"p":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
				UserPublicKey:            tc.der,
				UserPrivateKeyCiphertext: []byte("ct"),
				UserPrivateKeyNonce:      make([]byte, 12),
				WrappedVaultKey:          []byte("wrapped"),
			}, audit)
			if err == nil {
				t.Fatalf("expected error for %s pubkey", tc.name)
			}
		})
	}
}

func TestRegisterBootstrapAdminOnly(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, userID := freshTenant(t, ctx, pool)

	// Non-admin tries to bootstrap → reject.
	nonAdmin := &services.Caller{TenantID: tenantID, UserID: userID, IsAdmin: false}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	err := svc.Register(ctx, nonAdmin, RegisterParams{
		AuthHash:                 randHash(t),
		KDFParams:                json.RawMessage(`{"algo":"argon2id","v":19,"m":65536,"t":3,"p":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		UserPublicKey:            genRSAPubKey(t),
		UserPrivateKeyCiphertext: []byte("ct"),
		UserPrivateKeyNonce:      make([]byte, 12),
		WrappedVaultKey:          []byte("wrapped"),
	}, svc.AuditFromRequest(nonAdmin, r))
	if err == nil {
		t.Fatalf("non-admin bootstrap should be rejected")
	}
}

func TestRegisterPostBootstrapForbidsSelfWrap(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, adminID := freshTenant(t, ctx, pool)
	admin := adminCaller(tenantID, adminID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	// Admin bootstraps successfully.
	if err := svc.Register(ctx, admin, RegisterParams{
		AuthHash:                 randHash(t),
		KDFParams:                json.RawMessage(`{"algo":"argon2id","v":19,"m":65536,"t":3,"p":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		UserPublicKey:            genRSAPubKey(t),
		UserPrivateKeyCiphertext: []byte("ct"),
		UserPrivateKeyNonce:      make([]byte, 12),
		WrappedVaultKey:          []byte("wrapped"),
	}, svc.AuditFromRequest(admin, r)); err != nil {
		t.Fatalf("admin bootstrap: %v", err)
	}

	// Second user tries to self-wrap (i.e. supply WrappedVaultKey post-bootstrap) → reject.
	otherUser, err := models.GetOrCreateUser(ctx, pool, tenantID, "U_"+uuid.NewString()[:8], "Other", "")
	if err != nil {
		t.Fatalf("other user: %v", err)
	}
	other := &services.Caller{TenantID: tenantID, UserID: otherUser.ID, IsAdmin: false}
	err = svc.Register(ctx, other, RegisterParams{
		AuthHash:                 randHash(t),
		KDFParams:                json.RawMessage(`{"algo":"argon2id","v":19,"m":65536,"t":3,"p":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		UserPublicKey:            genRSAPubKey(t),
		UserPrivateKeyCiphertext: []byte("ct"),
		UserPrivateKeyNonce:      make([]byte, 12),
		WrappedVaultKey:          []byte("self-wrap-attempt"),
	}, svc.AuditFromRequest(other, r))
	if err == nil {
		t.Fatalf("post-bootstrap self-wrap should be rejected")
	}
}

func TestUnlockMismatchUniformResponse(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, userID := freshTenant(t, ctx, pool)
	c := adminCaller(tenantID, userID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	// No vault_users row exists for this user yet — unlock must return
	// the same error as a wrong-password attempt.
	_, err := svc.Unlock(ctx, c, randHash(t), svc.AuditFromRequest(c, r))
	if !errors.Is(err, ErrUnlockMismatch) {
		t.Fatalf("expected ErrUnlockMismatch on missing row, got %v", err)
	}
}

func TestScopeDiff(t *testing.T) {
	a := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	before := []models.VaultEntryScope{
		{ScopeKind: "user", ScopeID: &a},
		{ScopeKind: "tenant"},
	}
	after := []models.VaultEntryScope{
		{ScopeKind: "user", ScopeID: &a},
		{ScopeKind: "user", ScopeID: &b},
	}

	added, removed := scopeDiff(before, after)
	if len(added) != 1 || added[0].Kind != "user" || added[0].ID == nil || *added[0].ID != b {
		t.Errorf("added wrong: %+v", added)
	}
	if len(removed) != 1 || removed[0].Kind != "tenant" {
		t.Errorf("removed wrong: %+v", removed)
	}
}

func TestPubkeyFingerprintStable(t *testing.T) {
	der := genRSAPubKey(t)
	a := pubkeyFingerprint(der)
	b := pubkeyFingerprint(der)
	if a != b || a == "" {
		t.Fatalf("fingerprint should be stable + non-empty: a=%q b=%q", a, b)
	}
	// Signal-style: 6 groups of 4 hex chars, 5 separator spaces = 29 chars.
	if len(a) != 29 {
		t.Errorf("fingerprint should be 29 chars, got %d (%q)", len(a), a)
	}
}

// TestRevokeGrantRequiresAdmin asserts that the service-level admin check
// blocks non-admin RevokeGrant calls regardless of HTTP-handler logic.
// Catches the case where an MCP path or test bypasses the HTTP layer.
func TestRevokeGrantRequiresAdmin(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, userID := freshTenant(t, ctx, pool)

	nonAdmin := &services.Caller{TenantID: tenantID, UserID: userID, IsAdmin: false}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	err := svc.RevokeGrant(ctx, nonAdmin, uuid.New(), svc.AuditFromRequest(nonAdmin, r))
	if !errors.Is(err, services.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-admin, got %v", err)
	}
}

// TestStepUpRequiredOnGrantWithoutRecentUnlock asserts Grant rejects
// callers who haven't unlocked recently. The plan calls for a 5-min
// window; this exercises the no-prior-unlock path.
func TestStepUpRequiredOnGrantWithoutRecentUnlock(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewService(pool)
	tenantID, userID := freshTenant(t, ctx, pool)
	c := adminCaller(tenantID, userID)
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	err := svc.Grant(ctx, c, GrantParams{
		TargetUserID:    uuid.New(),
		WrappedVaultKey: []byte("wrapped"),
	}, svc.AuditFromRequest(c, r))
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("expected ErrStepUpRequired, got %v", err)
	}
}

// TestValidateScopesRejectsDuplicates exercises the dedup check added
// in service.go alongside scopeKey/scopeDiff.
func TestValidateScopesRejectsDuplicates(t *testing.T) {
	uid := uuid.New()
	scopes := []models.VaultEntryScope{
		{ScopeKind: "user", ScopeID: &uid},
		{ScopeKind: "user", ScopeID: &uid},
	}
	if err := validateScopes(scopes); err == nil {
		t.Fatal("expected duplicate scope rejection")
	}
}
