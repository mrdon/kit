// Tests for the cards scope-write path (RoleScopes + UserScopes union),
// including the tenant-membership check on user_id values.
package cards_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// TestUserScopeFromForeignTenantRejected ensures cards.writeScopesTx
// refuses a UserScopes uuid that doesn't belong to the caller's tenant.
// Without the check, a malicious admin could pollute the scopes table
// with cross-tenant references that never match any visibility filter.
func TestUserScopeFromForeignTenantRejected(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	// Two distinct tenants + a user in each.
	teamA := "T_a_" + uuid.NewString()
	slugA := models.SanitizeSlug("a-"+uuid.NewString(), teamA)
	tenantA, err := models.UpsertTenant(ctx, pool, teamA, "tA", "encrypted", slugA, nil, nil)
	if err != nil {
		t.Fatalf("tenantA: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenantA.ID) })

	teamB := "T_b_" + uuid.NewString()
	slugB := models.SanitizeSlug("b-"+uuid.NewString(), teamB)
	tenantB, err := models.UpsertTenant(ctx, pool, teamB, "tB", "encrypted", slugB, nil, nil)
	if err != nil {
		t.Fatalf("tenantB: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenantB.ID) })

	userA, err := models.GetOrCreateUser(ctx, pool, tenantA.ID, "U_a_"+uuid.NewString()[:8], "A admin", "")
	if err != nil {
		t.Fatalf("userA: %v", err)
	}
	userB, err := models.GetOrCreateUser(ctx, pool, tenantB.ID, "U_b_"+uuid.NewString()[:8], "B user", "")
	if err != nil {
		t.Fatalf("userB: %v", err)
	}

	svc := cards.NewService(pool)
	adminA := &services.Caller{
		TenantID: tenantA.ID, UserID: userA.ID, IsAdmin: true,
		Roles: []string{"admin"},
	}

	// Try to create a tenant-A briefing scoped to userB (foreign tenant).
	_, err = svc.CreateBriefing(ctx, adminA, cards.CardCreateInput{
		Kind:       cards.CardKindBriefing,
		Title:      "should fail",
		Body:       "x",
		UserScopes: []uuid.UUID{userB.ID},
		Briefing:   &cards.BriefingCreateInput{Severity: cards.BriefingSeverityInfo},
	})
	if err == nil {
		t.Fatal("expected cross-tenant UserScopes to be rejected")
	}
	if !strings.Contains(err.Error(), "scope references a user not in this tenant") {
		t.Errorf("expected tenant-validation error, got %v", err)
	}
}

// TestUserScopeWithinTenantAccepted is the happy-path counterpart —
// confirms the validation doesn't reject legitimate per-user scoping.
func TestUserScopeWithinTenantAccepted(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_ok_" + uuid.NewString()
	slug := models.SanitizeSlug("ok-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "t", "encrypted", slug, nil, nil)
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	admin, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_admin_"+uuid.NewString()[:8], "admin", "")
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	target, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_target_"+uuid.NewString()[:8], "target", "")
	if err != nil {
		t.Fatalf("target: %v", err)
	}

	svc := cards.NewService(pool)
	c := &services.Caller{
		TenantID: tenant.ID, UserID: admin.ID, IsAdmin: true,
		Roles: []string{"admin"},
	}

	card, err := svc.CreateBriefing(ctx, c, cards.CardCreateInput{
		Kind:       cards.CardKindBriefing,
		Title:      "hello target",
		Body:       "x",
		UserScopes: []uuid.UUID{target.ID},
		Briefing:   &cards.BriefingCreateInput{Severity: cards.BriefingSeverityInfo},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if card == nil {
		t.Fatal("expected card, got nil")
	}
}

// TestCreateSystemDecisionBypassesScopeAccess regression-tests the
// system-caller path. The vault's grant-request card scopes to the
// admin role on behalf of a non-admin registering user, which would
// fail enforceScopeAccess on the regular CreateDecision path. The
// system path must accept this (no caller, no scope-membership check).
func TestCreateSystemDecisionBypassesScopeAccess(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_sys_" + uuid.NewString()
	slug := models.SanitizeSlug("sys-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "t", "encrypted", slug, nil, nil)
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, "admin", "tenant admin"); err != nil {
		t.Fatalf("creating admin role: %v", err)
	}

	svc := cards.NewService(pool)
	card, err := svc.CreateSystemDecision(ctx, tenant.ID, cards.CardCreateInput{
		Kind:       cards.CardKindDecision,
		Title:      "Grant vault access",
		Body:       "Admin must grant this user access.",
		RoleScopes: []string{"admin"},
		Decision: &cards.DecisionCreateInput{
			Priority: cards.DecisionPriorityHigh,
			Options: []cards.DecisionOption{
				{OptionID: "grant", Label: "Grant"},
				{OptionID: "decline", Label: "Decline"},
			},
		},
	})
	if err != nil {
		t.Fatalf("system create: %v", err)
	}
	if card == nil {
		t.Fatal("expected card, got nil")
	}
}
