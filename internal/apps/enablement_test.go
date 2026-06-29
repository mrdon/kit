package apps

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

func enablementTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	teamID := "T_enable_" + uuid.NewString()
	slug := models.SanitizeSlug("enable-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "enable-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	return tenant.ID
}

// withFeatures sets the toggleable-feature set for a test (the apps package
// unit test has an empty registry, so initEnablement leaves it empty) and
// restores it afterward.
func withFeatures(t *testing.T, names ...string) {
	t.Helper()
	featureApps = make(map[string]bool, len(names))
	for _, n := range names {
		featureApps[n] = true
	}
	t.Cleanup(func() { featureApps = nil })
}

func TestIsEnabledDefaultsOn(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	initEnablement(pool)
	withFeatures(t, "vault")
	t.Cleanup(func() { enablement = nil })

	tenantID := enablementTenant(t, ctx, pool)

	// No rows yet → everything enabled (opt-out default).
	if !IsEnabled(ctx, tenantID, "vault") {
		t.Fatal("vault should be enabled by default")
	}
}

func TestSetEnabledDisableAndReenable(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	initEnablement(pool)
	withFeatures(t, "vault", "expense")
	t.Cleanup(func() { enablement = nil })

	tenantID := enablementTenant(t, ctx, pool)

	// Prime the cache, then disable — the cache must be busted on write.
	_ = IsEnabled(ctx, tenantID, "vault")
	if err := SetEnabled(ctx, tenantID, "vault", false); err != nil {
		t.Fatalf("disabling vault: %v", err)
	}
	if IsEnabled(ctx, tenantID, "vault") {
		t.Fatal("vault should be disabled after SetEnabled(false)")
	}

	// Other apps remain enabled; isolation by app name.
	if !IsEnabled(ctx, tenantID, "expense") {
		t.Fatal("expense should still be enabled")
	}

	// Re-enable.
	if err := SetEnabled(ctx, tenantID, "vault", true); err != nil {
		t.Fatalf("re-enabling vault: %v", err)
	}
	if !IsEnabled(ctx, tenantID, "vault") {
		t.Fatal("vault should be enabled again after SetEnabled(true)")
	}
}

func TestNonFeatureAppsAlwaysEnabled(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	initEnablement(pool)
	withFeatures(t, "vault") // only vault is a feature; console/attachment are core
	t.Cleanup(func() { enablement = nil })

	tenantID := enablementTenant(t, ctx, pool)

	// A non-feature (core) app cannot be toggled and is always enabled.
	if err := SetEnabled(ctx, tenantID, AppConsole, false); !errors.Is(err, ErrNotToggleable) {
		t.Fatalf("expected ErrNotToggleable disabling console, got %v", err)
	}
	if !IsEnabled(ctx, tenantID, AppConsole) {
		t.Fatal("console must always be enabled")
	}
	if !IsEnabled(ctx, tenantID, "attachment") {
		t.Fatal("attachment (core) must always be enabled")
	}
}

func TestIsEnabledZeroTenantAndUnconfigured(t *testing.T) {
	ctx := context.Background()
	withFeatures(t, "vault") // vault IS a feature, so we exercise the real paths
	// Unconfigured gate (no Init) → everything enabled so test paths work.
	enablement = nil
	if !IsEnabled(ctx, uuid.New(), "vault") {
		t.Fatal("unconfigured gate should report enabled")
	}

	pool := testdb.Open(t)
	initEnablement(pool)
	withFeatures(t, "vault") // initEnablement rebuilt the (empty) set; restore vault
	t.Cleanup(func() { enablement = nil })
	// Zero tenant (no caller) → enabled.
	if !IsEnabled(ctx, uuid.Nil, "vault") {
		t.Fatal("zero tenant should report enabled")
	}
}
