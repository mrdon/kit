package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// TestRoleService_Delete_BuiltinRoleRejected verifies that the `admin` and
// `member` builtin roles cannot be deleted via the role service. The guard
// lives in services.RoleService.Delete and is the single choke point for
// both the MCP handler and the agent tool.
func TestRoleService_Delete_BuiltinRoleRejected(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_roles_" + uuid.NewString()
	slug := models.SanitizeSlug("roles-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "roles-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleAdmin, "admin"); err != nil {
		t.Fatalf("creating admin role: %v", err)
	}
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleMember, "member"); err != nil {
		t.Fatalf("creating member role: %v", err)
	}

	svc := &RoleService{pool: pool}
	admin := &Caller{TenantID: tenant.ID, IsAdmin: true}

	for _, name := range []string{models.RoleAdmin, models.RoleMember} {
		err := svc.Delete(ctx, admin, name, true)
		if err == nil {
			t.Errorf("Delete(%q) returned nil, want error", name)
			continue
		}
		if !strings.Contains(err.Error(), "builtin role") {
			t.Errorf("Delete(%q) error = %v, want 'builtin role' substring", name, err)
		}
	}
}

// rolesTestTenant spins up a throwaway tenant with the admin role, returning
// the service + an admin caller. Shared setup for the guardrail tests below.
func rolesTestTenant(t *testing.T) (*RoleService, *Caller, *models.Tenant, context.Context) {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_roles_" + uuid.NewString()
	slug := models.SanitizeSlug("roles-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "roles-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleAdmin, "admin"); err != nil {
		t.Fatalf("creating admin role: %v", err)
	}
	return &RoleService{pool: pool}, &Caller{TenantID: tenant.ID, IsAdmin: true}, tenant, ctx
}

// TestRoleService_Assign_UnknownRole verifies a typo'd role name fails loudly
// instead of the silent no-op the raw INSERT would produce.
func TestRoleService_Assign_UnknownRole(t *testing.T) {
	svc, admin, _, ctx := rolesTestTenant(t)
	err := svc.Assign(ctx, admin, "U_ghost", "does-not-exist")
	if !errors.Is(err, ErrRoleNotFound) {
		t.Fatalf("Assign(unknown role) error = %v, want ErrRoleNotFound", err)
	}
}

// TestRoleService_Unassign_LastAdmin verifies the final admin can't be
// stripped of the admin role, but a non-final admin can.
func TestRoleService_Unassign_LastAdmin(t *testing.T) {
	svc, admin, _, ctx := rolesTestTenant(t)

	if err := svc.Assign(ctx, admin, "U_admin1", models.RoleAdmin); err != nil {
		t.Fatalf("assigning admin1: %v", err)
	}
	// Only one admin — removal must be refused.
	if err := svc.Unassign(ctx, admin, "U_admin1", models.RoleAdmin); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("Unassign(last admin) error = %v, want ErrLastAdmin", err)
	}
	// Add a second admin; now removing the first is allowed.
	if err := svc.Assign(ctx, admin, "U_admin2", models.RoleAdmin); err != nil {
		t.Fatalf("assigning admin2: %v", err)
	}
	if err := svc.Unassign(ctx, admin, "U_admin1", models.RoleAdmin); err != nil {
		t.Fatalf("Unassign(non-last admin) error = %v, want nil", err)
	}
}
