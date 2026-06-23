package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// TestSkillSetScopeAdminGuard: re-scoping a skill is admin-only, like every
// other skill mutation. The guard short-circuits before any DB access.
func TestSkillSetScopeAdminGuard(t *testing.T) {
	svc := &SkillService{}
	err := svc.SetScope(context.Background(), &Caller{IsAdmin: false}, uuid.New(), "tenant")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin SetScope = %v, want ErrForbidden", err)
	}
}

// TestJobResolveScopeGuards verifies the shared scope guards used by both job
// create and the new scope editor: non-admins can't go tenant-wide or grab a
// role they don't hold, but can scope to themselves.
func TestJobResolveScopeGuards(t *testing.T) {
	svc := &JobService{}
	ctx := context.Background()
	c := &Caller{UserID: uuid.New(), IsAdmin: false, Roles: []string{models.RoleMember}}

	if _, _, err := svc.resolveScope(ctx, c, string(models.ScopeTypeTenant)); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-admin tenant scope = %v, want ErrForbidden", err)
	}
	if _, _, err := svc.resolveScope(ctx, c, "managers"); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-admin scoping to a role they lack = %v, want ErrForbidden", err)
	}
	roleID, userID, err := svc.resolveScope(ctx, c, string(models.ScopeTypeUser))
	if err != nil || userID == nil || *userID != c.UserID || roleID != nil {
		t.Errorf("user scope: got role=%v user=%v err=%v, want user=self", roleID, userID, err)
	}
}
