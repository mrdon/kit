package services

import (
	"context"
	"errors"
	"slices"
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

// TestRoleService_Membership_EffectiveRoles verifies the matrix reflects
// EFFECTIVE roles: a user with no explicit roles shows up in the tenant
// default role (member), while a user with an explicit role shows that.
// This is the centralized default-role fallback the console page relies on.
func TestRoleService_Membership_EffectiveRoles(t *testing.T) {
	svc, admin, tenant, ctx := rolesTestTenant(t)

	// Make 'member' the tenant default role.
	memberRole, err := models.GetOrCreateRole(ctx, svc.pool, tenant.ID, models.RoleMember, "member")
	if err != nil {
		t.Fatalf("creating member role: %v", err)
	}
	if err := models.SetDefaultRole(ctx, svc.pool, tenant.ID, &memberRole.ID); err != nil {
		t.Fatalf("setting default role: %v", err)
	}

	// Alice has no explicit roles; Bob is an explicit admin. Both must be
	// members — member is a universal catchall.
	if _, err := models.GetOrCreateUser(ctx, svc.pool, tenant.ID, "U_alice", "Alice", ""); err != nil {
		t.Fatalf("creating alice: %v", err)
	}
	if err := svc.Assign(ctx, admin, "U_bob", models.RoleAdmin); err != nil {
		t.Fatalf("assigning bob admin: %v", err)
	}

	m, err := svc.Membership(ctx, admin)
	if err != nil {
		t.Fatalf("Membership: %v", err)
	}

	rolesOf := func(slackID string) []string {
		for _, u := range m.Users {
			if u.SlackUserID == slackID {
				return u.Roles
			}
		}
		t.Fatalf("user %s not in membership", slackID)
		return nil
	}
	if got := rolesOf("U_alice"); !slices.Contains(got, models.RoleMember) || slices.Contains(got, models.RoleAdmin) {
		t.Errorf("Alice effective roles = %v, want just [member]", got)
	}
	// Bob has an explicit role AND is still a member (catchall is universal).
	if got := rolesOf("U_bob"); !slices.Contains(got, models.RoleAdmin) || !slices.Contains(got, models.RoleMember) {
		t.Errorf("Bob effective roles = %v, want both admin and member", got)
	}
}

// TestRoleService_Member_NeverStored verifies the member catchall is never
// written as a user_roles row: assigning it is a no-op, unassigning it is
// refused, and no row appears either way.
func TestRoleService_Member_NeverStored(t *testing.T) {
	svc, admin, tenant, ctx := rolesTestTenant(t)
	if _, err := models.GetOrCreateRole(ctx, svc.pool, tenant.ID, models.RoleMember, "member"); err != nil {
		t.Fatalf("creating member role: %v", err)
	}

	// Assigning member is a silent no-op.
	if err := svc.Assign(ctx, admin, "U_carol", models.RoleMember); err != nil {
		t.Fatalf("Assign(member) = %v, want nil no-op", err)
	}
	// Unassigning member is refused.
	if err := svc.Unassign(ctx, admin, "U_carol", models.RoleMember); !errors.Is(err, ErrCannotLeaveMember) {
		t.Fatalf("Unassign(member) = %v, want ErrCannotLeaveMember", err)
	}
	// No member row was written.
	var n int
	if err := svc.pool.QueryRow(ctx, `
		SELECT count(*) FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.tenant_id = $1 AND r.name = $2
	`, tenant.ID, models.RoleMember).Scan(&n); err != nil {
		t.Fatalf("counting member rows: %v", err)
	}
	if n != 0 {
		t.Errorf("found %d explicit member rows, want 0", n)
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
