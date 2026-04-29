package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// RoleTools defines the shared tool metadata for role operations.
var RoleTools = []ToolMeta{
	{Name: "list_roles", Description: "List all roles.", Schema: props(map[string]any{}), AdminOnly: true},
	{Name: "create_role", Description: "Create a new role.", Schema: propsReq(map[string]any{
		"name": field("string", "Role name (e.g., 'bartender')"), "description": field("string", "Brief description"),
	}, "name"), AdminOnly: true},
	{Name: "assign_role", Description: "Assign a role to a Slack user.", Schema: propsReq(map[string]any{
		"slack_user_id": field("string", "Slack user ID (e.g., 'U1234567890')"), "role_name": field("string", "Role name to assign"),
	}, "slack_user_id", "role_name"), AdminOnly: true},
	{Name: "unassign_role", Description: "Remove a role from a user.", Schema: propsReq(map[string]any{
		"slack_user_id": field("string", "Slack user ID"), "role_name": field("string", "Role name to remove"),
	}, "slack_user_id", "role_name"), AdminOnly: true},
	{Name: "update_role", Description: "Update a role's description.", Schema: propsReq(map[string]any{
		"name": field("string", "Role name"), "description": field("string", "New description"),
	}, "name", "description"), AdminOnly: true},
	{Name: "delete_role", Description: "Delete a role. Refuses if the role has scope-attached data; pass force=true to confirm. Cascade deletes all role-scoped todos and memories; other entities just lose visibility.", Schema: propsReq(map[string]any{
		"name":  field("string", "Role name"),
		"force": map[string]any{"type": "boolean", "description": "Confirm deletion even if the role has scope-attached data."},
	}, "name"), AdminOnly: true},
	{Name: "list_role_members", Description: "List all users assigned to a role.", Schema: propsReq(map[string]any{
		"role_name": field("string", "Role name"),
	}, "role_name"), AdminOnly: true},
}

// RoleService handles role operations with authorization.
type RoleService struct {
	pool *pgxpool.Pool
}

// List returns all roles in the tenant. Admin only.
func (s *RoleService) List(ctx context.Context, c *Caller) ([]models.Role, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.ListRoles(ctx, s.pool, c.TenantID)
}

// Create creates a role. Admin only.
func (s *RoleService) Create(ctx context.Context, c *Caller, name, description string) (*models.Role, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.CreateRole(ctx, s.pool, c.TenantID, name, description)
}

// Assign assigns a role to a user by Slack ID. Admin only.
func (s *RoleService) Assign(ctx context.Context, c *Caller, slackUserID, roleName string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	user, err := models.GetOrCreateUser(ctx, s.pool, c.TenantID, slackUserID, "", "")
	if err != nil {
		return fmt.Errorf("resolving user: %w", err)
	}
	return models.AssignRole(ctx, s.pool, c.TenantID, user.ID, roleName)
}

// Unassign removes a role from a user by Slack ID. Admin only.
func (s *RoleService) Unassign(ctx context.Context, c *Caller, slackUserID, roleName string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	user, err := models.GetUserBySlackID(ctx, s.pool, c.TenantID, slackUserID)
	if err != nil {
		return fmt.Errorf("resolving user: %w", err)
	}
	if user == nil {
		return ErrNotFound
	}
	return models.UnassignRole(ctx, s.pool, c.TenantID, user.ID, roleName)
}

// Update updates a role's description. Admin only.
func (s *RoleService) Update(ctx context.Context, c *Caller, name, description string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	return models.UpdateRole(ctx, s.pool, c.TenantID, name, description)
}

// ErrRoleHasImpact is returned by Delete when the role has cascade-affected
// data and force=false. The wrapped impact is in the error message; callers
// can call DeletionImpact for the structured count.
var ErrRoleHasImpact = errors.New("role has scoped data; pass force=true to confirm deletion")

// DeletionImpact returns a preview of what would be cascade-affected by
// deleting the named role.
func (s *RoleService) DeletionImpact(ctx context.Context, c *Caller, name string) (models.RoleDeletionImpact, error) {
	if !c.IsAdmin {
		return models.RoleDeletionImpact{}, ErrForbidden
	}
	return models.CountRoleDeletionImpact(ctx, s.pool, c.TenantID, name)
}

// Delete deletes a role. Admin only. By default refuses if the role has
// scope-attached data; pass force=true to confirm. Inline-scope entities
// (todos, memories) are destroyed; join-table entities lose the role's
// scope row and become invisible. The builtin `admin` and `member` roles
// cannot be deleted.
func (s *RoleService) Delete(ctx context.Context, c *Caller, name string, force bool) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	if name == models.RoleAdmin || name == models.RoleMember {
		return fmt.Errorf("cannot delete builtin role %q", name)
	}
	if !force {
		impact, err := models.CountRoleDeletionImpact(ctx, s.pool, c.TenantID, name)
		if err != nil {
			return err
		}
		if impact.HasImpact() {
			return fmt.Errorf("%w (todos=%d, memories=%d, skills=%d, rules=%d, tasks=%d, cards=%d, channels=%d, calendars=%d)",
				ErrRoleHasImpact,
				impact.TodosDeleted, impact.MemoriesDeleted,
				impact.SkillsAffected, impact.RulesAffected, impact.TasksAffected,
				impact.CardsAffected, impact.ChannelsAffected, impact.CalendarsAffected,
			)
		}
	}
	return models.DeleteRole(ctx, s.pool, c.TenantID, name)
}

// ListMembers lists users assigned to a role. Admin only.
func (s *RoleService) ListMembers(ctx context.Context, c *Caller, roleName string) ([]models.User, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.ListRoleMembers(ctx, s.pool, c.TenantID, roleName)
}
