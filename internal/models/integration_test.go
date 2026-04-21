package models

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/testdb"
)

// testTenantUser creates a throwaway tenant + user for an integration test.
// The tenant is cleaned up via t.Cleanup (cascades wipe the user).
func testTenantUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, userID uuid.UUID) {
	t.Helper()
	teamID := "T_int_" + uuid.NewString()
	slug := SanitizeSlug("int-"+uuid.NewString(), teamID)
	tenant, err := UpsertTenant(ctx, pool, teamID, "int-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})
	user, err := GetOrCreateUser(ctx, pool, tenant.ID, "U_"+uuid.NewString()[:8], "Int Tester", false)
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	return tenant.ID, user.ID
}

func TestIntegrationLifecycle(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userID := testTenantUser(t, ctx, pool)

	// User-scoped pending.
	p, err := CreatePendingIntegration(ctx, pool, tenantID, userID, "test", "api_key", &userID, 5*time.Minute)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if p.Status != PendingStatusPending {
		t.Fatalf("new pending should be 'pending', got %s", p.Status)
	}

	// Complete with a primary_token + username + one config field.
	user := "alice@example.com"
	primary := "hex-ciphertext-stub"
	cfg := map[string]any{"workspace_url": "https://example.com"}
	integID, err := CompletePendingIntegration(ctx, pool, tenantID, p.ID, &user, &primary, nil, cfg)
	if err != nil {
		t.Fatalf("complete pending: %v", err)
	}

	// Status should flip to consumed.
	p2, err := GetPendingIntegration(ctx, pool, tenantID, p.ID)
	if err != nil {
		t.Fatalf("reload pending: %v", err)
	}
	if p2 == nil {
		t.Fatalf("pending gone after complete (expired?)")
	}
	if p2.Status != PendingStatusConsumed {
		t.Errorf("expected consumed, got %s", p2.Status)
	}

	// Integration row exists with the right shape.
	integ, err := GetIntegration(ctx, pool, tenantID, "test", "api_key", &userID)
	if err != nil {
		t.Fatalf("get integration: %v", err)
	}
	if integ == nil {
		t.Fatalf("integration missing after complete")
	}
	if integ.ID != integID {
		t.Errorf("id mismatch: %s vs %s", integ.ID, integID)
	}
	if integ.Username != user {
		t.Errorf("username = %q, want %q", integ.Username, user)
	}
	if integ.Config["workspace_url"] != "https://example.com" {
		t.Errorf("config.workspace_url = %v", integ.Config["workspace_url"])
	}

	// Tokens accessor returns the stored ciphertext.
	gotPrimary, gotSecondary, err := GetIntegrationTokens(ctx, pool, tenantID, integ.ID)
	if err != nil {
		t.Fatalf("get tokens: %v", err)
	}
	if gotPrimary != primary {
		t.Errorf("primary token mismatch: %q vs %q", gotPrimary, primary)
	}
	if gotSecondary != "" {
		t.Errorf("secondary should be empty, got %q", gotSecondary)
	}

	// Second complete of the same pending is rejected.
	_, err = CompletePendingIntegration(ctx, pool, tenantID, p.ID, &user, &primary, nil, cfg)
	if !errors.Is(err, ErrPendingNotPending) {
		t.Errorf("second complete should return ErrPendingNotPending, got %v", err)
	}
}

func TestIntegrationUpsertOnReconfigure(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userID := testTenantUser(t, ctx, pool)

	// First config.
	p1, err := CreatePendingIntegration(ctx, pool, tenantID, userID, "test", "api_key", &userID, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cfg1 := map[string]any{"version": "v1"}
	primary1 := "secret1"
	id1, err := CompletePendingIntegration(ctx, pool, tenantID, p1.ID, nil, &primary1, nil, cfg1)
	if err != nil {
		t.Fatal(err)
	}

	// Re-configure the same (provider, auth_type, user).
	p2, err := CreatePendingIntegration(ctx, pool, tenantID, userID, "test", "api_key", &userID, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cfg2 := map[string]any{"version": "v2"}
	primary2 := "secret2"
	id2, err := CompletePendingIntegration(ctx, pool, tenantID, p2.ID, nil, &primary2, nil, cfg2)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("re-config should return the same integration id via ON CONFLICT; got %s vs %s", id1, id2)
	}

	integ, err := GetIntegration(ctx, pool, tenantID, "test", "api_key", &userID)
	if err != nil {
		t.Fatal(err)
	}
	if integ.Config["version"] != "v2" {
		t.Errorf("expected updated config, got %v", integ.Config)
	}
	gotPrimary, _, err := GetIntegrationTokens(ctx, pool, tenantID, integ.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotPrimary != primary2 {
		t.Errorf("token should be updated to %q, got %q", primary2, gotPrimary)
	}
}

// TestIntegrationUpdateKeepsExistingToken covers the edit-without-
// re-entering-password path: re-completing with nil primary_token
// leaves the stored ciphertext untouched while still updating config.
func TestIntegrationUpdateKeepsExistingToken(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userID := testTenantUser(t, ctx, pool)

	p1, err := CreatePendingIntegration(ctx, pool, tenantID, userID, "test", "api_key", &userID, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	original := "original-ciphertext"
	user := "alice@example.com"
	if _, err := CompletePendingIntegration(ctx, pool, tenantID, p1.ID, &user, &original, nil, map[string]any{"sig": "old"}); err != nil {
		t.Fatal(err)
	}

	// Edit: pass nil for primary_token, change the config.
	p2, err := CreatePendingIntegration(ctx, pool, tenantID, userID, "test", "api_key", &userID, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompletePendingIntegration(ctx, pool, tenantID, p2.ID, nil, nil, nil, map[string]any{"sig": "new"}); err != nil {
		t.Fatal(err)
	}

	integ, err := GetIntegration(ctx, pool, tenantID, "test", "api_key", &userID)
	if err != nil {
		t.Fatal(err)
	}
	if integ.Config["sig"] != "new" {
		t.Errorf("config not updated: %v", integ.Config)
	}
	if integ.Username != user {
		t.Errorf("username should be preserved, got %q", integ.Username)
	}
	gotPrimary, _, err := GetIntegrationTokens(ctx, pool, tenantID, integ.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotPrimary != original {
		t.Errorf("primary token should be preserved, got %q want %q", gotPrimary, original)
	}
}

func TestIntegrationTenantVsUserScope(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userID := testTenantUser(t, ctx, pool)

	// Tenant-scoped: target_user_id = nil.
	pTenant, err := CreatePendingIntegration(ctx, pool, tenantID, userID, "tshared", "api_key", nil, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tenantSecret := "tenant-secret"
	if _, err := CompletePendingIntegration(ctx, pool, tenantID, pTenant.ID, nil, &tenantSecret, nil, nil); err != nil {
		t.Fatal(err)
	}

	// User-scoped.
	pUser, err := CreatePendingIntegration(ctx, pool, tenantID, userID, "tuser", "api_key", &userID, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	userSecret := "user-secret"
	if _, err := CompletePendingIntegration(ctx, pool, tenantID, pUser.ID, nil, &userSecret, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Tenant row has NULL user_id.
	tRow, err := GetIntegration(ctx, pool, tenantID, "tshared", "api_key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tRow == nil {
		t.Fatal("tenant integration missing")
	}
	if tRow.UserID != nil {
		t.Errorf("tenant row should have nil user_id, got %v", tRow.UserID)
	}

	// User row's user_id matches caller.
	uRow, err := GetIntegration(ctx, pool, tenantID, "tuser", "api_key", &userID)
	if err != nil {
		t.Fatal(err)
	}
	if uRow == nil {
		t.Fatal("user integration missing")
	}
	if uRow.UserID == nil || *uRow.UserID != userID {
		t.Errorf("user row user_id = %v, want %s", uRow.UserID, userID)
	}

	// List (non-admin, forUser): returns both (tenant-scoped + own user-scoped).
	list, err := ListIntegrations(ctx, pool, tenantID, &userID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 integrations (tenant + user), got %d", len(list))
	}
}

func TestIntegrationListHidesOtherUsers(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userA := testTenantUser(t, ctx, pool)

	// Create a second user in the same tenant.
	userBRow, err := GetOrCreateUser(ctx, pool, tenantID, "U_other_"+uuid.NewString()[:8], "Bob", false)
	if err != nil {
		t.Fatal(err)
	}
	userB := userBRow.ID

	// Each user configures their own integration.
	for _, uid := range []uuid.UUID{userA, userB} {
		p, err := CreatePendingIntegration(ctx, pool, tenantID, uid, "test", "api_key", &uid, 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		secret := "secret-for-" + uid.String()
		if _, err := CompletePendingIntegration(ctx, pool, tenantID, p.ID, nil, &secret, nil, nil); err != nil {
			t.Fatal(err)
		}
	}

	// User A should only see their own row.
	list, err := ListIntegrations(ctx, pool, tenantID, &userA, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("user A should see 1 row, got %d", len(list))
	}
	if list[0].UserID == nil || *list[0].UserID != userA {
		t.Errorf("user A saw wrong row: %v", list[0].UserID)
	}

	// Admin (includeAll = true) sees both.
	listAll, err := ListIntegrations(ctx, pool, tenantID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listAll) != 2 {
		t.Errorf("admin should see 2 rows, got %d", len(listAll))
	}
}

func TestIntegrationDeleteForbidden(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userA := testTenantUser(t, ctx, pool)
	userBRow, err := GetOrCreateUser(ctx, pool, tenantID, "U_other_"+uuid.NewString()[:8], "Bob", false)
	if err != nil {
		t.Fatal(err)
	}
	userB := userBRow.ID

	// User B creates an integration.
	p, err := CreatePendingIntegration(ctx, pool, tenantID, userB, "test", "api_key", &userB, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secret := "b-secret"
	integID, err := CompletePendingIntegration(ctx, pool, tenantID, p.ID, nil, &secret, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// User A (non-admin) tries to delete user B's integration.
	err = DeleteIntegration(ctx, pool, tenantID, integID, userA, false)
	if !errors.Is(err, ErrIntegrationForbidden) {
		t.Errorf("expected ErrIntegrationForbidden, got %v", err)
	}

	// Admin can delete it.
	if err := DeleteIntegration(ctx, pool, tenantID, integID, userA, true); err != nil {
		t.Errorf("admin delete failed: %v", err)
	}

	// Row is gone.
	integ, err := GetIntegrationByID(ctx, pool, tenantID, integID)
	if err != nil {
		t.Fatal(err)
	}
	if integ != nil {
		t.Errorf("row should be gone after admin delete")
	}
}
