package models

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Device roles: the actor_roles counterpart of user_roles. Devices hold
// exactly the rows stored here — no implicit member catchall — and every read
// drops `admin`, so a row that somehow got in can never make a device an
// admin.

// AssignActorRole adds a role to an actor. Idempotent.
func AssignActorRole(ctx context.Context, pool *pgxpool.Pool, tenantID, actorID uuid.UUID, roleName string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO actor_roles (tenant_id, actor_id, role_id)
		SELECT $1, $2, r.id FROM roles r WHERE r.tenant_id = $1 AND r.name = $3
		ON CONFLICT DO NOTHING
	`, tenantID, actorID, roleName)
	if err != nil {
		return fmt.Errorf("assigning actor role: %w", err)
	}
	return nil
}

// UnassignActorRole removes a role from an actor. Removing one it doesn't
// hold is a no-op.
func UnassignActorRole(ctx context.Context, pool *pgxpool.Pool, tenantID, actorID uuid.UUID, roleName string) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM actor_roles
		WHERE tenant_id = $1 AND actor_id = $2 AND role_id = (
			SELECT id FROM roles WHERE tenant_id = $1 AND name = $3
		)
	`, tenantID, actorID, roleName)
	if err != nil {
		return fmt.Errorf("unassigning actor role: %w", err)
	}
	return nil
}

// ActorRole is one role an actor holds.
type ActorRole struct {
	ActorID  uuid.UUID
	RoleID   uuid.UUID
	RoleName string
}

// ListActorRoles returns the roles held by the tenant's actors, ordered by
// role name. Pass actorID to read one actor; nil reads them all.
func ListActorRoles(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, actorID *uuid.UUID) ([]ActorRole, error) {
	rows, err := pool.Query(ctx, `
		SELECT ar.actor_id, r.id, r.name
		FROM actor_roles ar
		JOIN roles r ON r.id = ar.role_id AND r.tenant_id = ar.tenant_id
		WHERE ar.tenant_id = $1
		  AND ($2::uuid IS NULL OR ar.actor_id = $2)
		  AND r.name <> $3
		ORDER BY r.name
	`, tenantID, actorID, RoleAdmin)
	if err != nil {
		return nil, fmt.Errorf("listing actor roles: %w", err)
	}
	defer rows.Close()
	var out []ActorRole
	for rows.Next() {
		var ar ActorRole
		if err := rows.Scan(&ar.ActorID, &ar.RoleID, &ar.RoleName); err != nil {
			return nil, fmt.Errorf("scanning actor role: %w", err)
		}
		out = append(out, ar)
	}
	return out, rows.Err()
}

// ListRoleActors returns the live actors holding a role by name.
func ListRoleActors(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, roleName string) ([]Actor, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+prefixedActorColumns+`
		FROM actors a
		JOIN actor_roles ar ON ar.actor_id = a.id AND ar.tenant_id = a.tenant_id
		JOIN roles r ON r.id = ar.role_id AND r.tenant_id = ar.tenant_id
		WHERE a.tenant_id = $1 AND r.name = $2 AND a.revoked_at IS NULL
		ORDER BY a.label
	`, tenantID, roleName)
	if err != nil {
		return nil, fmt.Errorf("listing role actors: %w", err)
	}
	defer rows.Close()
	var out []Actor
	for rows.Next() {
		a, err := scanActor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// prefixedActorColumns is actorColumns qualified with the `a` alias, for
// queries that join actors to other tables.
const prefixedActorColumns = `a.id, a.tenant_id, a.kind, a.label, a.capabilities, a.sponsor_user_id, a.created_at, a.last_seen_at, a.revoked_at, a.start_token_hash IS NOT NULL`
