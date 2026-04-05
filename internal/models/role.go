package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

// ListRoleMembers returns all users assigned to a role by name.
func ListRoleMembers(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, roleName string) ([]User, error) {
	rows, err := pool.Query(ctx, `
		SELECT u.id, u.tenant_id, u.slack_user_id, u.display_name, u.is_admin, u.timezone, u.created_at
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
		if err := rows.Scan(&u.ID, &u.TenantID, &u.SlackUserID, &u.DisplayName, &u.IsAdmin, &u.Timezone, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// GetUserRoleNames returns the role names for a user.
// If the user has no assigned roles and the tenant has a default role,
// the user is auto-assigned to it and that role name is returned.
func GetUserRoleNames(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, defaultRoleID *uuid.UUID) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.name FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.tenant_id = $1 AND ur.user_id = $2
		ORDER BY r.name
	`, tenantID, userID)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Auto-assign default role if user has none
	if len(names) == 0 && defaultRoleID != nil {
		_, err := pool.Exec(ctx, `
			INSERT INTO user_roles (tenant_id, user_id, role_id)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, tenantID, userID, *defaultRoleID)
		if err != nil {
			return nil, fmt.Errorf("auto-assigning default role: %w", err)
		}
		var name string
		err = pool.QueryRow(ctx, `SELECT name FROM roles WHERE id = $1`, *defaultRoleID).Scan(&name)
		if err == nil {
			names = append(names, name)
		}
	}

	return names, nil
}
