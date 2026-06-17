package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Builtin role names. Every tenant has these two roles; they cannot be
// deleted via the role tools. `member` is auto-assigned to users with no
// explicit roles; `admin` is granted via AssignRole and gates admin-only
// tools.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

type Role struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Name        string
	Description *string
	CreatedAt   time.Time
}

func ListRoles(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Role, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, name, description, created_at
		FROM roles WHERE tenant_id = $1 ORDER BY name
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing roles: %w", err)
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &r.Description, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning role: %w", err)
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// RoleExists checks if a role with the given name exists for a tenant.
func RoleExists(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM roles WHERE tenant_id = $1 AND name = $2)
	`, tenantID, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking role exists: %w", err)
	}
	return exists, nil
}

func CreateRole(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name, description string) (*Role, error) {
	role := &Role{}
	err := pool.QueryRow(ctx, `
		INSERT INTO roles (id, tenant_id, name, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, name, description, created_at
	`, uuid.New(), tenantID, name, nilIfEmpty(description)).Scan(
		&role.ID, &role.TenantID, &role.Name, &role.Description, &role.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating role: %w", err)
	}
	return role, nil
}

// GetOrCreateRole returns the existing role by (tenant, name), or creates it.
// Idempotent — safe to call on every install/reinstall for builtin roles.
// On conflict, `description` is NOT updated: the no-op `SET name = EXCLUDED.name`
// exists only so RETURNING emits the existing row. Callers that need to update
// description should call UpdateRole separately.
func GetOrCreateRole(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name, description string) (*Role, error) {
	role := &Role{}
	err := pool.QueryRow(ctx, `
		INSERT INTO roles (id, tenant_id, name, description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, tenant_id, name, description, created_at
	`, uuid.New(), tenantID, name, nilIfEmpty(description)).Scan(
		&role.ID, &role.TenantID, &role.Name, &role.Description, &role.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get or create role: %w", err)
	}
	return role, nil
}

func AssignRole(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleName string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO user_roles (tenant_id, user_id, role_id)
		SELECT $1, $2, r.id FROM roles r WHERE r.tenant_id = $1 AND r.name = $3
		ON CONFLICT DO NOTHING
	`, tenantID, userID, roleName)
	if err != nil {
		return fmt.Errorf("assigning role: %w", err)
	}
	return nil
}

func UnassignRole(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleName string) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM user_roles
		WHERE tenant_id = $1 AND user_id = $2 AND role_id = (
			SELECT id FROM roles WHERE tenant_id = $1 AND name = $3
		)
	`, tenantID, userID, roleName)
	if err != nil {
		return fmt.Errorf("unassigning role: %w", err)
	}
	return nil
}

func UpdateRole(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name, description string) error {
	_, err := pool.Exec(ctx, `
		UPDATE roles SET description = $3 WHERE tenant_id = $1 AND name = $2
	`, tenantID, name, description)
	if err != nil {
		return fmt.Errorf("updating role: %w", err)
	}
	return nil
}

func DeleteRole(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM roles WHERE tenant_id = $1 AND name = $2
	`, tenantID, name)
	if err != nil {
		return fmt.Errorf("deleting role: %w", err)
	}
	return nil
}

// RoleDeletionImpact counts the entities that would be cascade-affected if
// the named role were deleted. Inline-scope entities (todos, memories) are
// destroyed; join-table entities (skills, rules, tasks, cards, channels,
// calendars) only lose the role's scope row and become invisible.
type RoleDeletionImpact struct {
	TodosDeleted      int
	MemoriesDeleted   int
	SkillsAffected    int
	RulesAffected     int
	TasksAffected     int
	CardsAffected     int
	ChannelsAffected  int
	CalendarsAffected int
}

// HasImpact reports whether the role has any role-scoped data attached.
func (i RoleDeletionImpact) HasImpact() bool {
	return i.TodosDeleted > 0 || i.MemoriesDeleted > 0 ||
		i.SkillsAffected > 0 || i.RulesAffected > 0 || i.TasksAffected > 0 ||
		i.CardsAffected > 0 || i.ChannelsAffected > 0 || i.CalendarsAffected > 0
}

// CountRoleDeletionImpact returns the cascade preview without modifying state.
func CountRoleDeletionImpact(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string) (RoleDeletionImpact, error) {
	var i RoleDeletionImpact
	err := pool.QueryRow(ctx, `
		WITH r AS (
			SELECT id FROM roles WHERE tenant_id = $1 AND name = $2
		), s AS (
			SELECT id FROM scopes WHERE tenant_id = $1 AND role_id IN (SELECT id FROM r)
		)
		SELECT
			(SELECT count(*) FROM app_todos      WHERE tenant_id = $1 AND scope_id IN (SELECT id FROM s)),
			(SELECT count(*) FROM memories       WHERE tenant_id = $1 AND scope_id IN (SELECT id FROM s)),
			(SELECT count(*) FROM skill_scopes   WHERE tenant_id = $1 AND scope_id IN (SELECT id FROM s)),
			(SELECT count(*) FROM rule_scopes    WHERE tenant_id = $1 AND scope_id IN (SELECT id FROM s)),
			(SELECT count(*) FROM job_scopes    WHERE tenant_id = $1 AND scope_id IN (SELECT id FROM s)),
			(SELECT count(*) FROM app_card_scopes WHERE tenant_id = $1 AND scope_id IN (SELECT id FROM s)),
			(SELECT count(*) FROM app_slack_channel_scopes WHERE tenant_id = $1 AND scope_id IN (SELECT id FROM s)),
			(SELECT count(*) FROM app_calendar_scopes      WHERE tenant_id = $1 AND scope_id IN (SELECT id FROM s))
	`, tenantID, name).Scan(
		&i.TodosDeleted, &i.MemoriesDeleted,
		&i.SkillsAffected, &i.RulesAffected, &i.TasksAffected,
		&i.CardsAffected, &i.ChannelsAffected, &i.CalendarsAffected,
	)
	if err != nil {
		return RoleDeletionImpact{}, fmt.Errorf("counting role deletion impact: %w", err)
	}
	return i, nil
}

// ListRoleMembers returns all users assigned to a role by name.
func ListRoleMembers(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, roleName string) ([]User, error) {
	rows, err := pool.Query(ctx, `
		SELECT u.id, u.tenant_id, u.slack_user_id, u.display_name, u.timezone, u.created_at
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id AND ur.tenant_id = u.tenant_id
		JOIN roles r ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
		WHERE u.tenant_id = $1 AND r.name = $2
		ORDER BY u.slack_user_id
	`, tenantID, roleName)
	if err != nil {
		return nil, fmt.Errorf("listing role members: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.SlackUserID, &u.DisplayName, &u.Timezone, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// FindAdminUser returns one user assigned to the admin role for the tenant,
// or nil if no admins exist. Used by the scheduler to pick a `created_by` for
// builtin tasks.
func FindAdminUser(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*User, error) {
	u := &User{}
	err := pool.QueryRow(ctx, `
		SELECT u.id, u.tenant_id, u.slack_user_id, u.display_name, u.timezone, u.created_at
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id AND ur.tenant_id = u.tenant_id
		JOIN roles r ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
		WHERE u.tenant_id = $1 AND r.name = $2
		ORDER BY u.created_at
		LIMIT 1
	`, tenantID, RoleAdmin).Scan(
		&u.ID, &u.TenantID, &u.SlackUserID, &u.DisplayName, &u.Timezone, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // no admin is not an error
		}
		return nil, fmt.Errorf("finding admin user: %w", err)
	}
	return u, nil
}

// GetUserRoleIDs returns the role IDs a user effectively holds: their
// explicit user_roles plus the universal `member` catchall, which every user
// always belongs to. Member is implicit — it's never stored as a user_roles
// row, so it's added here via UNION rather than read from the table.
func GetUserRoleIDs(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT ur.role_id FROM user_roles ur
		WHERE ur.tenant_id = $1 AND ur.user_id = $2
		UNION
		SELECT id FROM roles WHERE tenant_id = $1 AND name = $3
		ORDER BY 1
	`, tenantID, userID, RoleMember)
	if err != nil {
		return nil, fmt.Errorf("getting user role ids: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetUserRoleNames returns the role names a user effectively holds: their
// explicit user_roles plus the universal `member` catchall (see
// GetUserRoleIDs). Member is implicit and never stored as an assignment.
func GetUserRoleNames(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.name FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.tenant_id = $1 AND ur.user_id = $2
		UNION
		SELECT name FROM roles WHERE tenant_id = $1 AND name = $3
		ORDER BY 1
	`, tenantID, userID, RoleMember)
	if err != nil {
		return nil, fmt.Errorf("getting user roles: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
