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

// Actor kinds. Users are not actors: a user_id anywhere still means a
// person, and this table holds only the things that act without one.
const (
	ActorKindDevice = "device"
	ActorKindAgent  = "agent"
	ActorKindWidget = "widget"
)

// Actor is a non-human principal: a paired device today, with agents and
// widgets reserved. Capabilities are the complete statement of what it may
// do; there are no roles behind it.
type Actor struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	Kind          string
	Label         string
	Capabilities  []string
	SponsorUserID *uuid.UUID
	CreatedAt     time.Time
	LastSeenAt    *time.Time
	RevokedAt     *time.Time
}

const actorColumns = `id, tenant_id, kind, label, capabilities, sponsor_user_id, created_at, last_seen_at, revoked_at`

func scanActor(row pgx.Row) (*Actor, error) {
	a := &Actor{}
	err := row.Scan(&a.ID, &a.TenantID, &a.Kind, &a.Label, &a.Capabilities, &a.SponsorUserID, &a.CreatedAt, &a.LastSeenAt, &a.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("scanning actor: %w", err)
	}
	return a, nil
}

// CreateActor inserts an actor. sponsor is the user who approved it, if
// any; it is recorded for audit, not authority.
func CreateActor(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, kind, label string, capabilities []string, sponsor *uuid.UUID) (*Actor, error) {
	if capabilities == nil {
		capabilities = []string{}
	}
	a, err := scanActor(pool.QueryRow(ctx, `
		INSERT INTO actors (tenant_id, kind, label, capabilities, sponsor_user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+actorColumns,
		tenantID, kind, label, capabilities, sponsor))
	if err != nil {
		return nil, fmt.Errorf("creating actor: %w", err)
	}
	return a, nil
}

// GetActor returns one actor, revoked or not; nil when missing.
func GetActor(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Actor, error) {
	return scanActor(pool.QueryRow(ctx, `
		SELECT `+actorColumns+` FROM actors WHERE tenant_id = $1 AND id = $2
	`, tenantID, id))
}

// ListActors returns a tenant's actors of one kind, live ones first, newest
// first within each group.
func ListActors(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, kind string) ([]Actor, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+actorColumns+` FROM actors
		WHERE tenant_id = $1 AND kind = $2
		ORDER BY (revoked_at IS NOT NULL), created_at DESC
	`, tenantID, kind)
	if err != nil {
		return nil, fmt.Errorf("listing actors: %w", err)
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

// UpdateActor changes the label and capability set.
func UpdateActor(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, label string, capabilities []string) error {
	if capabilities == nil {
		capabilities = []string{}
	}
	tag, err := pool.Exec(ctx, `
		UPDATE actors SET label = $3, capabilities = $4
		WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL
	`, tenantID, id, label, capabilities)
	if err != nil {
		return fmt.Errorf("updating actor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeActor marks the actor revoked and revokes every token it holds, so
// the next request from the device fails at the resolver.
func RevokeActor(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning revoke: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	tag, err := tx.Exec(ctx, `
		UPDATE actors SET revoked_at = now()
		WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL
	`, tenantID, id)
	if err != nil {
		return fmt.Errorf("revoking actor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = now()
		WHERE tenant_id = $1 AND actor_id = $2 AND revoked_at IS NULL
	`, tenantID, id); err != nil {
		return fmt.Errorf("revoking actor tokens: %w", err)
	}
	return tx.Commit(ctx)
}

// TouchActor bumps last_seen_at, at most once per touchInterval so a busy
// device doesn't turn every request into a write.
func TouchActor(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE actors SET last_seen_at = now()
		WHERE tenant_id = $1 AND id = $2
		  AND (last_seen_at IS NULL OR last_seen_at < now() - $3::interval)
	`, tenantID, id, touchInterval)
	if err != nil {
		return fmt.Errorf("touching actor: %w", err)
	}
	return nil
}

// touchInterval throttles last_seen_at / last_used_at writes. "Seen in the
// last quarter hour" is as precise as anyone reading the column needs.
const touchInterval = 15 * time.Minute
