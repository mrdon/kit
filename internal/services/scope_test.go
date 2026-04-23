package services

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

func ptrUUID(u uuid.UUID) *uuid.UUID { return &u }

func TestCallerCanSee(t *testing.T) {
	uid := uuid.New()
	role1 := uuid.New()
	role2 := uuid.New()
	otherUser := uuid.New()
	otherRole := uuid.New()

	caller := &Caller{
		UserID:  uid,
		RoleIDs: []uuid.UUID{role1, role2},
	}
	admin := &Caller{IsAdmin: true}

	cases := []struct {
		name   string
		c      *Caller
		scopes []ScopeRef
		want   bool
	}{
		{"empty scopes denied", caller, nil, false},
		{"admin sees everything", admin, []ScopeRef{{ID: uuid.New(), UserID: ptrUUID(otherUser)}}, true},
		{"admin sees empty", admin, nil, true},
		{"tenant-wide visible", caller, []ScopeRef{{ID: uuid.New()}}, true},
		{"matching role visible", caller, []ScopeRef{{ID: uuid.New(), RoleID: ptrUUID(role2)}}, true},
		{"matching user visible", caller, []ScopeRef{{ID: uuid.New(), UserID: ptrUUID(uid)}}, true},
		{"other role hidden", caller, []ScopeRef{{ID: uuid.New(), RoleID: ptrUUID(otherRole)}}, false},
		{"other user hidden", caller, []ScopeRef{{ID: uuid.New(), UserID: ptrUUID(otherUser)}}, false},
		{"any-of match wins", caller, []ScopeRef{
			{ID: uuid.New(), RoleID: ptrUUID(otherRole)},
			{ID: uuid.New(), UserID: ptrUUID(uid)},
		}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.CanSee(tc.scopes); got != tc.want {
				t.Fatalf("CanSee = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScopeFilterIDs(t *testing.T) {
	uid := uuid.New()
	roleA := uuid.New()
	roleB := uuid.New()
	caller := &Caller{
		UserID:  uid,
		RoleIDs: []uuid.UUID{roleA, roleB},
	}

	frag, args := caller.ScopeFilterIDs("s", 2)

	if !strings.Contains(frag, "s.role_id IS NULL AND s.user_id IS NULL") {
		t.Errorf("missing tenant-wide clause: %q", frag)
	}
	if !strings.Contains(frag, "s.user_id = $2") {
		t.Errorf("missing user clause: %q", frag)
	}
	if !strings.Contains(frag, "s.role_id = ANY($3)") {
		t.Errorf("missing role clause: %q", frag)
	}
	if len(args) != 2 || args[0] != uid {
		t.Fatalf("expected [userID, roleIDs], got %v", args)
	}

	// Caller with no roles: role clause omitted entirely.
	noRoles := &Caller{UserID: uid}
	frag2, args2 := noRoles.ScopeFilterIDs("", 1)
	if strings.Contains(frag2, "role_id = ANY") {
		t.Errorf("role clause should be omitted when RoleIDs empty: %q", frag2)
	}
	if !strings.Contains(frag2, "user_id = $1") {
		t.Errorf("missing user clause: %q", frag2)
	}
	if len(args2) != 1 {
		t.Fatalf("expected 1 arg (userID only), got %d", len(args2))
	}
}

func TestPersonalScopeFilter(t *testing.T) {
	uid := uuid.New()
	roleA := uuid.New()
	caller := &Caller{
		UserID:  uid,
		RoleIDs: []uuid.UUID{roleA},
	}

	frag, _ := caller.PersonalScopeFilter("s", 2)

	if strings.Contains(frag, "IS NULL") {
		t.Errorf("personal filter must exclude tenant-wide: %q", frag)
	}
	if !strings.Contains(frag, "s.user_id = $2") {
		t.Errorf("missing user clause: %q", frag)
	}
	if !strings.Contains(frag, "s.role_id = ANY($3)") {
		t.Errorf("missing role clause: %q", frag)
	}
}

func TestGetOrCreateScopeIdempotent(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_test_scope_" + uuid.NewString()
	slug := models.SanitizeSlug("scope-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "scope-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	role, err := models.CreateRole(ctx, pool, tenant.ID, "barista", "")
	if err != nil {
		t.Fatalf("creating role: %v", err)
	}
	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_scope", "Scope User")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	t.Run("role scope deduped", func(t *testing.T) {
		a, err := models.GetOrCreateScope(ctx, pool, tenant.ID, &role.ID, nil)
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		b, err := models.GetOrCreateScope(ctx, pool, tenant.ID, &role.ID, nil)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if a != b {
			t.Fatalf("expected same scope id, got %v vs %v", a, b)
		}
	})

	t.Run("user scope deduped", func(t *testing.T) {
		a, err := models.GetOrCreateScope(ctx, pool, tenant.ID, nil, &user.ID)
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		b, err := models.GetOrCreateScope(ctx, pool, tenant.ID, nil, &user.ID)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if a != b {
			t.Fatalf("expected same scope id, got %v vs %v", a, b)
		}
	})

	t.Run("tenant-wide scope deduped", func(t *testing.T) {
		a, err := models.GetOrCreateScope(ctx, pool, tenant.ID, nil, nil)
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		b, err := models.GetOrCreateScope(ctx, pool, tenant.ID, nil, nil)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if a != b {
			t.Fatalf("expected same scope id, got %v vs %v", a, b)
		}
	})

	t.Run("different scopes are distinct", func(t *testing.T) {
		roleScope, _ := models.GetOrCreateScope(ctx, pool, tenant.ID, &role.ID, nil)
		userScope, _ := models.GetOrCreateScope(ctx, pool, tenant.ID, nil, &user.ID)
		tenantScope, _ := models.GetOrCreateScope(ctx, pool, tenant.ID, nil, nil)
		if roleScope == userScope || roleScope == tenantScope || userScope == tenantScope {
			t.Fatalf("scopes should be distinct: role=%v user=%v tenant=%v", roleScope, userScope, tenantScope)
		}
	})

	t.Run("both ids set is rejected", func(t *testing.T) {
		_, err := models.GetOrCreateScope(ctx, pool, tenant.ID, &role.ID, &user.ID)
		if err == nil {
			t.Fatalf("expected error when both role_id and user_id set")
		}
	})
}
