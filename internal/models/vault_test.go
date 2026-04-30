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

func TestVaultEntryCreateAndOwnerOnlyAuthz(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)
	otherID := mustOtherUser(t, ctx, pool, tenantID)

	id, err := CreateVaultEntry(ctx, pool, VaultEntry{
		TenantID:        tenantID,
		OwnerUserID:     ownerID,
		Title:           "GitHub work",
		ValueCiphertext: []byte("ciphertext"),
		ValueNonce:      randBytes(t, 12),
	}, nil)
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}

	// Owner can read.
	got, err := GetVaultEntry(ctx, pool, tenantID, id, ownerID, nil)
	if err != nil || got == nil {
		t.Fatalf("owner read: got=%v err=%v", got, err)
	}

	// Default-deny: other user gets 404 (modeled as ErrNotFound).
	if _, err := GetVaultEntry(ctx, pool, tenantID, id, otherID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user should be denied; got err=%v", err)
	}
}

func TestVaultEntryUserScope(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)
	otherID := mustOtherUser(t, ctx, pool, tenantID)

	id, err := CreateVaultEntry(ctx, pool, VaultEntry{
		TenantID:        tenantID,
		OwnerUserID:     ownerID,
		Title:           "Shared",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      randBytes(t, 12),
	}, []VaultEntryScope{
		{ScopeKind: "user", ScopeID: &otherID},
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}

	// Other user (named in scope) can now read.
	got, err := GetVaultEntry(ctx, pool, tenantID, id, otherID, nil)
	if err != nil || got == nil {
		t.Fatalf("scoped user read: got=%v err=%v", got, err)
	}
}

func TestVaultEntryTenantScope(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, ownerID := testTenantUser(t, ctx, pool)
	otherID := mustOtherUser(t, ctx, pool, tenantID)

	id, err := CreateVaultEntry(ctx, pool, VaultEntry{
		TenantID:        tenantID,
		OwnerUserID:     ownerID,
		Title:           "Shop wifi",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      randBytes(t, 12),
	}, []VaultEntryScope{
		{ScopeKind: "tenant"},
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}

	got, err := GetVaultEntry(ctx, pool, tenantID, id, otherID, nil)
	if err != nil || got == nil {
		t.Fatalf("tenant-scoped read: got=%v err=%v", got, err)
	}
}

func TestVaultTenantIsolation(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenantA, ownerA := testTenantUser(t, ctx, pool)
	tenantB, ownerB := testTenantUser(t, ctx, pool)

	// Create an entry in A scoped to tenant — visible to all of A.
	id, err := CreateVaultEntry(ctx, pool, VaultEntry{
		TenantID:        tenantA,
		OwnerUserID:     ownerA,
		Title:           "Tenant A only",
		ValueCiphertext: []byte("ct"),
		ValueNonce:      randBytes(t, 12),
	}, []VaultEntryScope{{ScopeKind: "tenant"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Tenant B's owner must not see it under any input combination.
	_, err = GetVaultEntry(ctx, pool, tenantB, id, ownerB, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read should fail; got %v", err)
	}

	// List from tenant B must not include tenant A entries.
	rows, err := ListVaultEntries(ctx, pool, tenantB, ownerB, nil, "", "", 100)
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

	for _, title := range []string{"GitHub work", "GitHub personal", "AWS prod"} {
		_, err := CreateVaultEntry(ctx, pool, VaultEntry{
			TenantID:        tenantID,
			OwnerUserID:     ownerID,
			Title:           title,
			ValueCiphertext: []byte("ct"),
			ValueNonce:      randBytes(t, 12),
		}, nil)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	rows, err := ListVaultEntries(ctx, pool, tenantID, ownerID, nil, "github", "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 GitHub matches, got %d", len(rows))
	}
}

func TestVaultBootstrapPartialUniqueIndex(t *testing.T) {
	// The first user in a tenant is identified by (wrapped_vault_key
	// IS NOT NULL AND granted_by_user_id IS NULL). The partial unique
	// index ensures no two users can simultaneously claim that role.
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userA := testTenantUser(t, ctx, pool)
	userB := mustOtherUser(t, ctx, pool, tenantID)

	// First registration as initiator: succeeds.
	if err := RegisterVaultUser(ctx, pool, VaultRegisterParams{
		TenantID:                 tenantID,
		UserID:                   userA,
		KDFParams:                []byte(`{"algo":"argon2id","v":19,"m":65536,"t":3,"p":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:                 randBytes(t, 32),
		UserPublicKey:            []byte("pk-a"),
		UserPrivateKeyCiphertext: []byte("pkct-a"),
		UserPrivateKeyNonce:      randBytes(t, 12),
		WrappedVaultKey:          []byte("wrapped-a"),
	}); err != nil {
		t.Fatalf("first register: %v", err)
	}

	// Second registration as initiator: should fail due to unique index.
	err := RegisterVaultUser(ctx, pool, VaultRegisterParams{
		TenantID:                 tenantID,
		UserID:                   userB,
		KDFParams:                []byte(`{"algo":"argon2id","v":19,"m":65536,"t":3,"p":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA=="}`),
		AuthHash:                 randBytes(t, 32),
		UserPublicKey:            []byte("pk-b"),
		UserPrivateKeyCiphertext: []byte("pkct-b"),
		UserPrivateKeyNonce:      randBytes(t, 12),
		WrappedVaultKey:          []byte("wrapped-b"),
	})
	if err == nil {
		t.Fatalf("second initiator registration should fail")
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
