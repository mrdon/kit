package models

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/testdb"
)

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func TestVaultEntryRoleScopeIsolation(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)
	otherID := mustOtherUser(t, ctx, pool, tenantID)

	// Create a role and assign only the owner to it. The entry scoped
	// to that role should be visible to the owner, hidden from the
	// non-member (otherID).
	role, err := GetOrCreateRole(ctx, pool, tenantID, "engineering", "")
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := AssignRole(ctx, pool, tenantID, ownerID, "engineering"); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	id, err := CreateVaultEntry(ctx, pool, VaultEntry{
		TenantID:        tenantID,
		OwnerUserID:     ownerID,
		RoleID:          &role.ID,
		Title:           "Eng AWS prod",
		ValueCiphertext: []byte("ciphertext"),
		ValueNonce:      randBytes(t, 12),
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}

	// Owner can read (member of role; also implicit owner).
	got, err := GetVaultEntry(ctx, pool, tenantID, id, ownerID, []uuid.UUID{role.ID})
	if err != nil || got == nil {
		t.Fatalf("owner read: got=%v err=%v", got, err)
	}

	// Non-member of the role gets 404.
	if _, err := GetVaultEntry(ctx, pool, tenantID, id, otherID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member read should fail; got %v", err)
	}
}

func TestVaultEntryTenantScope(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)
	otherID := mustOtherUser(t, ctx, pool, tenantID)

	// Tenant-wide visibility = scope to the tenant's default ('member')
	// role. Every user is implicitly a member, so the otherID below
	// should pass authz when we pass that role in their RoleIDs.
	tenant, err := GetTenantByID(ctx, pool, tenantID)
	if err != nil || tenant == nil || tenant.DefaultRoleID == nil {
		t.Fatalf("loading default role: %v", err)
	}
	memberID := *tenant.DefaultRoleID

	id, err := CreateVaultEntry(ctx, pool, VaultEntry{
		TenantID:        tenantID,
		OwnerUserID:     ownerID,
		RoleID:          &memberID,
		Title:           "Shop wifi",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      randBytes(t, 12),
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}

	got, err := GetVaultEntry(ctx, pool, tenantID, id, otherID, []uuid.UUID{memberID})
	if err != nil || got == nil {
		t.Fatalf("tenant-scoped read: got=%v err=%v", got, err)
	}
}

func TestVaultTenantIsolation(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenantA, ownerA := testTenantUser(t, ctx, pool)
	tenantB, ownerB := testTenantUser(t, ctx, pool)

	// Create an entry in A scoped to A's member role — visible to all of A.
	tenantAObj, err := GetTenantByID(ctx, pool, tenantA)
	if err != nil || tenantAObj == nil || tenantAObj.DefaultRoleID == nil {
		t.Fatalf("loading tenant A default role: %v", err)
	}
	memberA := *tenantAObj.DefaultRoleID
	id, err := CreateVaultEntry(ctx, pool, VaultEntry{
		TenantID:        tenantA,
		OwnerUserID:     ownerA,
		RoleID:          &memberA,
		Title:           "Tenant A only",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      randBytes(t, 12),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Tenant B's owner must not see it under any input combination.
	_, err = GetVaultEntry(ctx, pool, tenantB, id, ownerB, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read should fail; got %v", err)
	}

	// List from tenant B must not include tenant A entries.
	rows, err := ListVaultEntries(ctx, pool, tenantB, ownerB, nil, "", "", nil, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range rows {
		if e.ID == id {
			t.Fatalf("tenant B saw a tenant A entry")
		}
	}
}

func TestVaultListSearch(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)

	tenant, err := GetTenantByID(ctx, pool, tenantID)
	if err != nil || tenant == nil || tenant.DefaultRoleID == nil {
		t.Fatalf("loading default role: %v", err)
	}
	memberID := *tenant.DefaultRoleID
	for _, title := range []string{"GitHub work", "GitHub personal", "AWS prod"} {
		_, err := CreateVaultEntry(ctx, pool, VaultEntry{
			TenantID:        tenantID,
			OwnerUserID:     ownerID,
			RoleID:          &memberID,
			Title:           title,
			ValueCiphertext: []byte("ct"),
			ValueNonce:      randBytes(t, 12),
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	rows, err := ListVaultEntries(ctx, pool, tenantID, ownerID, nil, "github", "", nil, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 GitHub matches, got %d", len(rows))
	}
}

// Regression: titles like "SignupGenius" stay as a single token under the
// English FTS dictionary, so a natural-language query "signup genius" used to
// miss. The ILIKE fallback added to ListVaultEntries must match it.
func TestVaultListSearchCamelCase(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)

	tenant, err := GetTenantByID(ctx, pool, tenantID)
	if err != nil || tenant == nil || tenant.DefaultRoleID == nil {
		t.Fatalf("loading default role: %v", err)
	}
	memberID := *tenant.DefaultRoleID
	if _, err := CreateVaultEntry(ctx, pool, VaultEntry{
		TenantID:        tenantID,
		OwnerUserID:     ownerID,
		RoleID:          &memberID,
		Title:           "SignupGenius",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      randBytes(t, 12),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, q := range []string{"signup", "signup genius", "Genius"} {
		rows, err := ListVaultEntries(ctx, pool, tenantID, ownerID, nil, q, "", nil, 100)
		if err != nil {
			t.Fatalf("list %q: %v", q, err)
		}
		if len(rows) != 1 {
			t.Fatalf("query %q: expected 1 match, got %d", q, len(rows))
		}
	}
}

func TestVaultTenantInitAndRotate(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)

	// First setup succeeds.
	if err := InitVaultTenant(ctx, pool, VaultSetupParams{
		TenantID:             tenantID,
		KDFParams:            []byte(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:             randBytes(t, 32),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
		SetupByUserID:        ownerID,
	}); err != nil {
		t.Fatalf("first setup: %v", err)
	}

	// Second setup refuses — already initialized.
	err := InitVaultTenant(ctx, pool, VaultSetupParams{
		TenantID:             tenantID,
		KDFParams:            []byte(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"BBBBBBBBBBBBBBBBBBBBBA=="}`),
		AuthHash:             randBytes(t, 32),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
		SetupByUserID:        ownerID,
	})
	if !errors.Is(err, ErrVaultAlreadySetup) {
		t.Fatalf("second setup should return ErrVaultAlreadySetup, got %v", err)
	}

	// Rotate bumps generation, preserves identity.
	newGen, err := RotateVaultTenant(ctx, pool, VaultRotateParams{
		TenantID:             tenantID,
		KDFParams:            []byte(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"CCCCCCCCCCCCCCCCCCCCCA=="}`),
		AuthHash:             randBytes(t, 32),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
		RotatedByUserID:      ownerID,
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newGen != 2 {
		t.Fatalf("expected vault_generation 2 after rotate, got %d", newGen)
	}
}

func TestVaultRotateBeforeSetup(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)

	_, err := RotateVaultTenant(ctx, pool, VaultRotateParams{
		TenantID:             tenantID,
		KDFParams:            []byte(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:             randBytes(t, 32),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
		RotatedByUserID:      ownerID,
	})
	if !errors.Is(err, ErrVaultNotSetUp) {
		t.Fatalf("rotate before setup should return ErrVaultNotSetUp, got %v", err)
	}
}

func TestVaultNuke(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)

	// Set up vault and create some entries.
	if err := InitVaultTenant(ctx, pool, VaultSetupParams{
		TenantID:             tenantID,
		KDFParams:            []byte(`{"algo":"pbkdf2-sha256","iterations":600000,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:             randBytes(t, 32),
		WrappedVaultKey:      randBytes(t, 48),
		WrappedVaultKeyNonce: randBytes(t, 12),
		SetupByUserID:        ownerID,
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	tenant, _ := GetTenantByID(ctx, pool, tenantID)
	memberID := *tenant.DefaultRoleID
	for range 3 {
		if _, err := CreateVaultEntry(ctx, pool, VaultEntry{
			TenantID:        tenantID,
			OwnerUserID:     ownerID,
			RoleID:          &memberID,
			Title:           "entry",
			ValueCiphertext: randBytes(t, 32),
			ValueNonce:      randBytes(t, 12),
		}); err != nil {
			t.Fatalf("create entry: %v", err)
		}
	}

	count, err := NukeVaultTenant(ctx, pool, tenantID)
	if err != nil {
		t.Fatalf("nuke: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 entries destroyed, got %d", count)
	}
	v, _ := GetVaultTenant(ctx, pool, tenantID)
	if v != nil {
		t.Fatalf("vault row should be gone after nuke")
	}
}

// mustOtherUser provisions a second user in the same tenant for authz tests.
func mustOtherUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	user, err := GetOrCreateUser(ctx, pool, tenantID, "U_"+uuid.NewString()[:8], "Other Tester", "")
	if err != nil {
		t.Fatalf("creating other user: %v", err)
	}
	return user.ID
}
