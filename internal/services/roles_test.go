package services

import (
	"context"
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
