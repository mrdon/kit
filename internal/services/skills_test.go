package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

func lookupRoleID(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`SELECT id FROM roles WHERE tenant_id = $1 AND name = $2`,
		tenantID, name).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("looking up role %q: %w", name, err)
	}
	return id, nil
}

// TestSkillRoleScoping verifies the role-based scope boundary: a user in the
// "member" role must NOT be able to access a skill scoped to "founder" via any
// service-layer code path (Load, LoadFile, List, Search). This is the
// regression test for the default-deny scoping rule documented in CLAUDE.md.
func TestSkillRoleScoping(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := &SkillService{pool: pool}

	// Isolated tenant per run.
	teamID := "T_test_scope_" + uuid.NewString()
	slug := models.SanitizeSlug("test-scope-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "scope-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	// Roles must exist before we can create scope rows that reference them.
	if _, err := models.CreateRole(ctx, pool, tenant.ID, "founder", "founder role"); err != nil {
		t.Fatalf("creating founder role: %v", err)
	}
	if _, err := models.CreateRole(ctx, pool, tenant.ID, "member", "regular member"); err != nil {
		t.Fatalf("creating member role: %v", err)
	}

	// Founder-scoped skill (created via the model layer; the service Create
	// path is admin-gated, but the underlying SQL is the same).
	founderSkill, err := models.CreateSkill(ctx, pool,
		tenant.ID,
		"founder-only-doc",
		"Sensitive founder material",
		"# Confidential\nFounder-only content.",
		"test",
		"founder",
	)
	if err != nil {
		t.Fatalf("creating founder skill: %v", err)
	}

	// Attach a file so we can also exercise LoadFile.
	founderFile, err := models.AddSkillFile(ctx, pool, tenant.ID, founderSkill.ID, "secret.txt", "top secret")
	if err != nil {
		t.Fatalf("adding skill file: %v", err)
	}

	// Member user, holding only the "member" role.
	memberUser, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_member", "Member User", false)
	if err != nil {
		t.Fatalf("creating member user: %v", err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, memberUser.ID, "member"); err != nil {
		t.Fatalf("assigning member role: %v", err)
	}

	memberRoleID, err := lookupRoleID(ctx, pool, tenant.ID, "member")
	if err != nil {
		t.Fatalf("looking up member role id: %v", err)
	}
	memberCaller := &Caller{
		TenantID: tenant.ID,
		UserID:   memberUser.ID,
		Identity: memberUser.SlackUserID,
		Roles:    []string{"member"},
		RoleIDs:  []uuid.UUID{memberRoleID},
		IsAdmin:  false,
	}

	t.Run("Load returns ErrForbidden", func(t *testing.T) {
		_, _, err := svc.Load(ctx, memberCaller, founderSkill.ID)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("LoadFile returns ErrForbidden", func(t *testing.T) {
		_, err := svc.LoadFile(ctx, memberCaller, founderFile.ID)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("List omits founder skill", func(t *testing.T) {
		got, err := svc.List(ctx, memberCaller, "")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, s := range got {
			if s.ID == founderSkill.ID {
				t.Fatalf("founder skill leaked into member List output: %+v", s)
			}
		}
	})

	t.Run("Search omits founder skill", func(t *testing.T) {
		// FTS query that matches the founder skill content.
		got, err := svc.Search(ctx, memberCaller, "confidential founder")
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		for _, s := range got {
			if s.ID == founderSkill.ID {
				t.Fatalf("founder skill leaked into member Search output: %+v", s)
			}
		}
	})

	t.Run("GetSkillCatalog omits founder skill", func(t *testing.T) {
		// System-prompt path used for non-admin agent context assembly.
		catalog, err := models.GetSkillCatalog(ctx, pool, tenant.ID, memberCaller.UserID, memberCaller.RoleIDs)
		if err != nil {
			t.Fatalf("catalog: %v", err)
		}
		for _, s := range catalog {
			if s.ID == founderSkill.ID {
				t.Fatalf("founder skill leaked into member catalog: %+v", s)
			}
		}
	})

	// Sanity check: a founder-role caller CAN load the same skill, so we know
	// the test setup actually wired the scope row correctly.
	t.Run("founder role can load", func(t *testing.T) {
		founderRoleID, err := lookupRoleID(ctx, pool, tenant.ID, "founder")
		if err != nil {
			t.Fatalf("looking up founder role id: %v", err)
		}
		founderCaller := &Caller{
			TenantID: tenant.ID,
			UserID:   memberUser.ID, // user identity irrelevant; access is by role
			Identity: "U_founder",
			Roles:    []string{"founder"},
			RoleIDs:  []uuid.UUID{founderRoleID},
			IsAdmin:  false,
		}
		s, _, err := svc.Load(ctx, founderCaller, founderSkill.ID)
		if err != nil {
			t.Fatalf("founder load: %v", err)
		}
		if s.ID != founderSkill.ID {
			t.Fatalf("got wrong skill: %v", s.ID)
		}
	})
}
