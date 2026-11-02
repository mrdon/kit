package services

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

func makeTestDevice(t *testing.T, ctx context.Context, svc *RoleService, tenantID uuid.UUID, label string) *models.Actor {
	t.Helper()
	a, err := models.CreateActor(ctx, svc.pool, tenantID, models.ActorKindDevice, label, nil, nil)
	if err != nil {
		t.Fatalf("creating device: %v", err)
	}
	return a
}

func makeTestRole(t *testing.T, ctx context.Context, svc *RoleService, tenantID uuid.UUID, name string) *models.Role {
	t.Helper()
	r, err := models.GetOrCreateRole(ctx, svc.pool, tenantID, name, name)
	if err != nil {
		t.Fatalf("creating role %s: %v", name, err)
	}
	return r
}

// A device holds exactly the roles assigned to it: no member catchall.
func TestDeviceRoles_AssignResolveNoCatchall(t *testing.T) {
	svc, admin, tenant, ctx := rolesTestTenant(t)
	makeTestRole(t, ctx, svc, tenant.ID, models.RoleMember)
	bartender := makeTestRole(t, ctx, svc, tenant.ID, "bartender")
	device := makeTestDevice(t, ctx, svc, tenant.ID, "Bar iPad")

	cr, err := svc.ResolveDeviceRoles(ctx, tenant.ID, device.ID)
	if err != nil {
		t.Fatalf("ResolveDeviceRoles: %v", err)
	}
	if len(cr.Names) != 0 {
		t.Fatalf("fresh device roles = %v, want none (no member catchall)", cr.Names)
	}

	if _, err := svc.AssignDevice(ctx, admin, "bar ipad", "bartender"); err != nil {
		t.Fatalf("AssignDevice by label: %v", err)
	}
	cr, err = svc.ResolveDeviceRoles(ctx, tenant.ID, device.ID)
	if err != nil {
		t.Fatalf("ResolveDeviceRoles: %v", err)
	}
	if !slices.Equal(cr.Names, []string{"bartender"}) || !slices.Equal(cr.IDs, []uuid.UUID{bartender.ID}) {
		t.Fatalf("roles = %v / %v, want bartender only", cr.Names, cr.IDs)
	}

	if _, err := svc.UnassignDevice(ctx, admin, device.ID.String(), "bartender"); err != nil {
		t.Fatalf("UnassignDevice by id: %v", err)
	}
	if cr, _ = svc.ResolveDeviceRoles(ctx, tenant.ID, device.ID); len(cr.Names) != 0 {
		t.Fatalf("after unassign roles = %v, want none", cr.Names)
	}
}

// Admin is refused on write, and a stored admin row never reaches the caller.
func TestDeviceRoles_NeverAdmin(t *testing.T) {
	svc, admin, tenant, ctx := rolesTestTenant(t)
	device := makeTestDevice(t, ctx, svc, tenant.ID, "Bar iPad")

	if _, err := svc.AssignDevice(ctx, admin, "Bar iPad", models.RoleAdmin); !errors.Is(err, ErrDeviceAdmin) {
		t.Fatalf("AssignDevice(admin) error = %v, want ErrDeviceAdmin", err)
	}
	if err := models.AssignActorRole(ctx, svc.pool, tenant.ID, device.ID, models.RoleAdmin); err != nil {
		t.Fatalf("seeding admin row: %v", err)
	}
	cr, err := svc.ResolveDeviceRoles(ctx, tenant.ID, device.ID)
	if err != nil {
		t.Fatalf("ResolveDeviceRoles: %v", err)
	}
	if slices.Contains(cr.Names, models.RoleAdmin) {
		t.Fatalf("device roles include admin: %v", cr.Names)
	}
	if c := NewDeviceCaller(tenant, device, CallerRoles{Names: []string{models.RoleAdmin}}); c.IsAdmin {
		t.Fatal("NewDeviceCaller produced an admin")
	}
}

func TestDeviceRoles_FindDevice(t *testing.T) {
	svc, admin, tenant, ctx := rolesTestTenant(t)
	makeTestRole(t, ctx, svc, tenant.ID, "bartender")
	makeTestDevice(t, ctx, svc, tenant.ID, "iPad")
	makeTestDevice(t, ctx, svc, tenant.ID, "ipad")
	gone := makeTestDevice(t, ctx, svc, tenant.ID, "Old laptop")
	if err := models.RevokeActor(ctx, svc.pool, tenant.ID, gone.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	if _, err := svc.AssignDevice(ctx, admin, "IPAD", "bartender"); !errors.Is(err, ErrDeviceAmbiguous) {
		t.Fatalf("ambiguous label error = %v, want ErrDeviceAmbiguous", err)
	}
	if _, err := svc.AssignDevice(ctx, admin, "Old laptop", "bartender"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked device error = %v, want ErrNotFound", err)
	}
	if _, err := svc.AssignDevice(ctx, &Caller{TenantID: tenant.ID}, "Old laptop", "bartender"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin error = %v, want ErrForbidden", err)
	}
}

// The matrix lists live devices with their roles and counts them per role.
func TestDeviceRoles_Membership(t *testing.T) {
	svc, admin, tenant, ctx := rolesTestTenant(t)
	makeTestRole(t, ctx, svc, tenant.ID, "bartender")
	makeTestDevice(t, ctx, svc, tenant.ID, "Bar iPad")
	if _, err := svc.AssignDevice(ctx, admin, "Bar iPad", "bartender"); err != nil {
		t.Fatalf("AssignDevice: %v", err)
	}

	m, err := svc.Membership(ctx, admin, nil)
	if err != nil {
		t.Fatalf("Membership: %v", err)
	}
	if len(m.Devices) != 1 || m.Devices[0].Label != "Bar iPad" || !slices.Equal(m.Devices[0].Roles, []string{"bartender"}) {
		t.Fatalf("devices = %+v, want Bar iPad in bartender", m.Devices)
	}
	for _, r := range m.Roles {
		if r.Name == "bartender" && r.MemberCount != 1 {
			t.Fatalf("bartender count = %d, want 1", r.MemberCount)
		}
	}
}

// ChangeRole backs both tool surfaces: exactly one target, device text.
func TestDeviceRoles_ChangeRoleText(t *testing.T) {
	svc, admin, tenant, ctx := rolesTestTenant(t)
	makeTestRole(t, ctx, svc, tenant.ID, "bartender")
	makeTestDevice(t, ctx, svc, tenant.ID, "Bar iPad")

	if _, err := svc.ChangeRole(ctx, admin, "U1", "Bar iPad", "bartender", true); !errors.Is(err, ErrRoleTarget) {
		t.Fatalf("both targets error = %v, want ErrRoleTarget", err)
	}
	if _, err := svc.ChangeRole(ctx, admin, "", "", "bartender", true); !errors.Is(err, ErrRoleTarget) {
		t.Fatalf("no target error = %v, want ErrRoleTarget", err)
	}
	text, err := svc.ChangeRole(ctx, admin, "", "Bar iPad", "bartender", true)
	if err != nil || text != "Role 'bartender' assigned to device Bar iPad." {
		t.Fatalf("ChangeRole = %q, %v", text, err)
	}
	if text, _ := svc.ChangeRole(ctx, admin, "", "Nope", "bartender", true); !strings.HasPrefix(text, "No paired device") {
		t.Fatalf("missing device text = %q", text)
	}
	members, err := svc.RoleMembersText(ctx, admin, "bartender")
	if err != nil || !strings.Contains(members, "Bar iPad — device id:") {
		t.Fatalf("RoleMembersText = %q, %v", members, err)
	}
}
