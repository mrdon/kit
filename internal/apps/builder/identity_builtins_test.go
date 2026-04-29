// Package builder: identity_builtins_test.go drives current_user() through
// the shared Monty engine and asserts the cross-boundary shape. Follows
// the same pattern as util_builtins_test.go; shares the TestMain-built
// runner from db_builtins_test.go.
package builder

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// runIdentity compiles + runs a Python main() function with the supplied
// IdentityBuiltins. Mirrors runUtil over in util_builtins_test.go.
func runIdentity(t *testing.T, ctx context.Context, src string, bundle *IdentityBuiltins) (any, runtime.Metadata, error) {
	t.Helper()
	mod, err := testEngine.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	caps := &runtime.Capabilities{
		BuiltIns:      bundle.BuiltIns,
		BuiltInParams: bundle.Params,
		RunID:         uuid.New(),
	}
	return testEngine.Run(ctx, mod, "main", nil, caps)
}

// TestCurrentUser_ShapeWithoutPool: nil pool returns the shape with a
// blank display_name, proving the function degrades gracefully when there
// is no DB wired (common in isolated test runs).
func TestCurrentUser_ShapeWithoutPool(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	tenantID := uuid.New()
	userID := uuid.New()
	bundle := BuildIdentityBuiltins(nil, tenantID, userID, []string{"admin", "manager"}, "America/Denver")

	src := `
def main():
    return current_user()
`
	result, _, err := runIdentity(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}

	if got := m["id"]; got != userID.String() {
		t.Errorf("id = %v, want %s", got, userID.String())
	}
	if got := m["display_name"]; got != "" {
		t.Errorf("display_name = %v, want empty (no pool)", got)
	}
	if got := m["timezone"]; got != "America/Denver" {
		t.Errorf("timezone = %v, want America/Denver", got)
	}

	roles, ok := m["roles"].([]any)
	if !ok {
		t.Fatalf("roles type = %T, want []any", m["roles"])
	}
	if len(roles) != 2 || roles[0] != "admin" || roles[1] != "manager" {
		t.Errorf("roles = %v, want [admin manager]", roles)
	}
}

// TestCurrentUser_NilRolesReturnsEmptyList: a caller with no roles must
// still produce a list on the Python side so scripts can `"admin" in roles`
// without a None check.
func TestCurrentUser_NilRolesReturnsEmptyList(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildIdentityBuiltins(nil, uuid.New(), uuid.New(), nil, "UTC")

	src := `
def main():
    u = current_user()
    return {"roles": u["roles"]}
`
	result, _, err := runIdentity(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	m := result.(map[string]any)
	roles, ok := m["roles"].([]any)
	if !ok {
		t.Fatalf("roles type = %T, want []any", m["roles"])
	}
	if len(roles) != 0 {
		t.Errorf("roles = %v, want empty list", roles)
	}
}

// TestCurrentUser_DisplayNameFromDB: with a real pool and an existing
// users row, display_name should reflect the stored value.
func TestCurrentUser_DisplayNameFromDB(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	pool := testdb.Open(t)

	teamID := "T_identity_" + uuid.NewString()
	slug := models.SanitizeSlug("identity-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "identity-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_identity_"+uuid.NewString()[:8], "Alice Example", "")
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	bundle := BuildIdentityBuiltins(pool, tenant.ID, user.ID, []string{"admin"}, "UTC")

	src := `
def main():
    return current_user()["display_name"]
`
	result, _, err := runIdentity(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result != "Alice Example" {
		t.Errorf("display_name = %v, want Alice Example", result)
	}
}
