package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
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

// RoleService is the single home for role business logic — effective-role
// resolution (explicit rows + the tenant default-role fallback), membership
// listing, and assignment. Every surface (agent context, builder, console)
// goes through here so the rules can't drift. enc is optional; it's only
// needed to list the full Slack workspace in Membership.
type RoleService struct {
	pool *pgxpool.Pool
	enc  *crypto.Encryptor
}

// NewRoleService returns a standalone RoleService. Most callers get one via
// the Services bundle (New); this constructor lets surfaces that only need
// roles (e.g. the console roles page) build one without the full bundle.
// enc may be nil for callers that don't list the Slack workspace.
func NewRoleService(pool *pgxpool.Pool, enc *crypto.Encryptor) *RoleService {
	return &RoleService{pool: pool, enc: enc}
}

// EffectiveRoleNames returns the roles a user effectively holds: their
// explicit user_roles, or the tenant's default role if they have none. This
// is THE definition of "what roles does this user have" — agent context,
// builder, and the console all call it so the default-role fallback can
// never drift between surfaces.
func (s *RoleService) EffectiveRoleNames(ctx context.Context, tenant *models.Tenant, userID uuid.UUID) ([]string, error) {
	return models.GetUserRoleNames(ctx, s.pool, tenant.ID, userID)
}

// CallerRoles is a user's effective role names plus role IDs, both resolved
// through the same default-role fallback so name-based checks (e.g. isAdmin)
// and id-based checks (scope filtering) can never disagree.
type CallerRoles struct {
	Names []string
	IDs   []uuid.UUID
}

// ResolveCallerRoles returns the effective roles for building a Caller. This
// is the single path the agent context, tool registry, and API-token
// middleware all use, so none of them re-implement the fallback.
func (s *RoleService) ResolveCallerRoles(ctx context.Context, tenant *models.Tenant, userID uuid.UUID) (CallerRoles, error) {
	names, err := models.GetUserRoleNames(ctx, s.pool, tenant.ID, userID)
	if err != nil {
		return CallerRoles{}, err
	}
	ids, err := models.GetUserRoleIDs(ctx, s.pool, tenant.ID, userID)
	if err != nil {
		return CallerRoles{}, err
	}
	return CallerRoles{Names: names, IDs: ids}, nil
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

// ErrRoleNotFound is returned by Assign/Unassign when the named role does
// not exist in the tenant. Without this check the underlying INSERT/DELETE
// match zero rows and silently succeed, so a typo'd role name looks like it
// worked.
var ErrRoleNotFound = errors.New("role does not exist")

// ErrLastAdmin is returned when an unassign would remove the admin role from
// the final remaining admin — that would lock the workspace out of every
// admin-only operation, so it's refused.
var ErrLastAdmin = errors.New("cannot remove the last admin")

// ErrCannotLeaveMember is returned when something tries to unassign the
// `member` role. Member is a universal catchall every user always holds; it's
// never stored as an assignment and can't be removed.
var ErrCannotLeaveMember = errors.New("everyone is a member; the member role can't be removed")

// Assign assigns a role to a user by Slack ID. Admin only.
func (s *RoleService) Assign(ctx context.Context, c *Caller, slackUserID, roleName string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	// Member is universal and implicit — assigning it is a no-op, and it must
	// never be written as a user_roles row.
	if roleName == models.RoleMember {
		return nil
	}
	exists, err := models.RoleExists(ctx, s.pool, c.TenantID, roleName)
	if err != nil {
		return fmt.Errorf("checking role: %w", err)
	}
	if !exists {
		return fmt.Errorf("%q: %w", roleName, ErrRoleNotFound)
	}
	user, err := models.GetOrCreateUser(ctx, s.pool, c.TenantID, slackUserID, "", "")
	if err != nil {
		return fmt.Errorf("resolving user: %w", err)
	}
	return models.AssignRole(ctx, s.pool, c.TenantID, user.ID, roleName)
}

// Unassign removes a role from a user by Slack ID. Admin only. Refuses to
// strip the admin role from the last admin (ErrLastAdmin).
func (s *RoleService) Unassign(ctx context.Context, c *Caller, slackUserID, roleName string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	if roleName == models.RoleMember {
		return ErrCannotLeaveMember
	}
	exists, err := models.RoleExists(ctx, s.pool, c.TenantID, roleName)
	if err != nil {
		return fmt.Errorf("checking role: %w", err)
	}
	if !exists {
		return fmt.Errorf("%q: %w", roleName, ErrRoleNotFound)
	}
	user, err := models.GetUserBySlackID(ctx, s.pool, c.TenantID, slackUserID)
	if err != nil {
		return fmt.Errorf("resolving user: %w", err)
	}
	if user == nil {
		return ErrNotFound
	}
	if roleName == models.RoleAdmin {
		admins, err := models.ListRoleMembers(ctx, s.pool, c.TenantID, models.RoleAdmin)
		if err != nil {
			return fmt.Errorf("counting admins: %w", err)
		}
		isAdmin := false
		for _, a := range admins {
			if a.ID == user.ID {
				isAdmin = true
				break
			}
		}
		if isAdmin && len(admins) <= 1 {
			return ErrLastAdmin
		}
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

// RoleMembership is the workspace-wide "who holds which role" view: every
// person in the workspace (the full Slack roster, merged with Kit's user
// records) plus the roles each effectively holds. It's the single source
// the console Roles page renders — no surface recomputes membership itself.
type RoleMembership struct {
	Roles []RoleSummary
	Users []UserRoles
}

// RoleSummary is a role plus how many people effectively hold it. Catchall
// marks the universal `member` role: everyone holds it and it can't be
// toggled, so the UI renders it locked.
type RoleSummary struct {
	Name        string
	Description string
	MemberCount int
	Catchall    bool
}

// UserRoles is one person and the roles they effectively hold. UserID is
// empty for people who have no Kit record yet (Slack members who haven't
// interacted with Kit) — they're effectively in the tenant default role.
type UserRoles struct {
	UserID      string
	SlackUserID string
	DisplayName string
	Roles       []string
}

// Membership returns the full role/user matrix. Admin only. People are
// sourced from the entire Slack workspace so everyone shows up, not just
// users who've already used Kit; each person's roles come from the single
// EffectiveRoleNames definition (explicit rows or the default-role fallback).
func (s *RoleService) Membership(ctx context.Context, c *Caller) (*RoleMembership, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	tenant, err := models.GetTenantByID(ctx, s.pool, c.TenantID)
	if err != nil {
		return nil, fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return nil, ErrNotFound
	}
	roles, err := models.ListRoles(ctx, s.pool, c.TenantID)
	if err != nil {
		return nil, fmt.Errorf("listing roles: %w", err)
	}
	kitUsers, err := models.ListUsersByTenant(ctx, s.pool, c.TenantID)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}

	bySlack := make(map[string]models.User, len(kitUsers))
	for _, u := range kitUsers {
		bySlack[u.SlackUserID] = u
	}

	counts := make(map[string]int)
	people := s.workspacePeople(ctx, tenant, kitUsers)
	users := make([]UserRoles, 0, len(people))
	for _, p := range people {
		ur := UserRoles{SlackUserID: p.SlackUserID, DisplayName: p.DisplayName}
		if ku, ok := bySlack[p.SlackUserID]; ok {
			ur.UserID = ku.ID.String()
			names, err := s.EffectiveRoleNames(ctx, tenant, ku.ID)
			if err != nil {
				return nil, err
			}
			ur.Roles = names
		} else {
			// No Kit record yet → effectively just the member catchall.
			ur.Roles = []string{models.RoleMember}
		}
		for _, n := range ur.Roles {
			counts[n]++
		}
		users = append(users, ur)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].DisplayName < users[j].DisplayName })

	summaries := make([]RoleSummary, 0, len(roles))
	for _, r := range roles {
		desc := ""
		if r.Description != nil {
			desc = *r.Description
		}
		summaries = append(summaries, RoleSummary{
			Name:        r.Name,
			Description: desc,
			MemberCount: counts[r.Name],
			Catchall:    r.Name == models.RoleMember,
		})
	}

	return &RoleMembership{Roles: summaries, Users: users}, nil
}

// WorkspacePerson is one workspace member: a Slack identity + display name.
type WorkspacePerson struct {
	SlackUserID string
	DisplayName string
}

// workspacePeople returns everyone in the workspace, sourced from the Slack
// roster (so people who've never used Kit still appear). Kit display names
// win when set. If Slack is unavailable (no encryptor/token or an API
// error) it falls back to the known Kit users so the page still renders.
func (s *RoleService) workspacePeople(ctx context.Context, tenant *models.Tenant, kitUsers []models.User) []WorkspacePerson {
	kitName := make(map[string]string, len(kitUsers))
	for _, u := range kitUsers {
		if u.DisplayName != nil && *u.DisplayName != "" {
			kitName[u.SlackUserID] = *u.DisplayName
		}
	}

	if s.enc != nil && tenant.BotToken != "" {
		token, err := s.enc.Decrypt(tenant.BotToken)
		if err != nil {
			slog.Warn("roles: decrypting bot token; showing kit users only", "tenant_id", tenant.ID, "error", err)
		} else if infos, err := kitslack.NewClient(token).ListAllUsers(ctx); err != nil {
			slog.Warn("roles: listing slack users; showing kit users only", "tenant_id", tenant.ID, "error", err)
		} else {
			seen := make(map[string]bool, len(infos))
			people := make([]WorkspacePerson, 0, len(infos))
			for _, in := range infos {
				name := kitName[in.SlackUserID]
				if name == "" {
					name = in.DisplayName
				}
				if name == "" {
					name = in.SlackUserID
				}
				people = append(people, WorkspacePerson{SlackUserID: in.SlackUserID, DisplayName: name})
				seen[in.SlackUserID] = true
			}
			// Include Kit users Slack didn't return (deactivated, renamed, …).
			for _, u := range kitUsers {
				if !seen[u.SlackUserID] {
					people = append(people, WorkspacePerson{SlackUserID: u.SlackUserID, DisplayName: userDisplayName(u)})
				}
			}
			return people
		}
	}

	people := make([]WorkspacePerson, 0, len(kitUsers))
	for _, u := range kitUsers {
		people = append(people, WorkspacePerson{SlackUserID: u.SlackUserID, DisplayName: userDisplayName(u)})
	}
	return people
}

func userDisplayName(u models.User) string {
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return u.SlackUserID
}
