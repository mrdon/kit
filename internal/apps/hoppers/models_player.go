package hoppers

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Player is a phone's identity in one game. The frog is in the engine, under
// this row's id.
type Player struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	GameID     uuid.UUID
	Name       string
	TokenHash  string
	LastSeenAt *time.Time
	CreatedAt  time.Time
}

const playerColumns = `id, tenant_id, game_id, name, token_hash, last_seen_at, created_at`

func scanPlayer(row pgx.Row) (*Player, error) {
	var p Player
	if err := row.Scan(&p.ID, &p.TenantID, &p.GameID, &p.Name, &p.TokenHash, &p.LastSeenAt, &p.CreatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// insertPlayer adds a phone's identity. It is seen as of now, so a frog that
// has just joined is never timed out before its stream is up.
func insertPlayer(ctx context.Context, q Querier, tenantID, gameID uuid.UUID, name, tokenHash string, now time.Time) (*Player, error) {
	p, err := scanPlayer(q.QueryRow(ctx, `
		INSERT INTO app_hoppers_players (tenant_id, game_id, name, token_hash, last_seen_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+playerColumns, tenantID, gameID, name, tokenHash, now))
	if err != nil {
		return nil, fmt.Errorf("inserting hoppers player: %w", err)
	}
	return p, nil
}

// GetPlayer loads one player of one game.
func GetPlayer(ctx context.Context, q Querier, tenantID, gameID, id uuid.UUID) (*Player, error) {
	p, err := scanPlayer(q.QueryRow(ctx,
		`SELECT `+playerColumns+` FROM app_hoppers_players WHERE tenant_id = $1 AND game_id = $2 AND id = $3`,
		tenantID, gameID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying hoppers player: %w", err)
	}
	return p, nil
}

// FindPlayerByToken resolves a cookie to its player, or ErrNotFound. The
// hash compare is constant-time; the id narrows it to one row first.
func FindPlayerByToken(ctx context.Context, q Querier, tenantID, gameID, id uuid.UUID, tokenHash string) (*Player, error) {
	p, err := GetPlayer(ctx, q, tenantID, gameID, id)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(p.TokenHash), []byte(tokenHash)) != 1 {
		return nil, ErrNotFound
	}
	return p, nil
}

// ListPlayers returns every phone that joined a game, oldest first.
func ListPlayers(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) ([]*Player, error) {
	rows, err := q.Query(ctx,
		`SELECT `+playerColumns+` FROM app_hoppers_players WHERE tenant_id = $1 AND game_id = $2 ORDER BY created_at`,
		tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing hoppers players: %w", err)
	}
	defer rows.Close()
	var out []*Player
	for rows.Next() {
		p, err := scanPlayer(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning hoppers player: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// setPlayerName keeps the row's name in step with the frog's, for the host's
// list.
func setPlayerName(ctx context.Context, q Querier, tenantID, gameID, id uuid.UUID, name string) error {
	_, err := q.Exec(ctx,
		`UPDATE app_hoppers_players SET name = $4 WHERE tenant_id = $1 AND game_id = $2 AND id = $3`,
		tenantID, gameID, id, name)
	if err != nil {
		return fmt.Errorf("renaming hoppers player: %w", err)
	}
	return nil
}

// SetPlayerToken replaces a player's token hash: a host-issued reclaim code,
// or the fresh token it is redeemed for. Either way the old phone's cookie
// stops resolving.
func SetPlayerToken(ctx context.Context, q Querier, tenantID, gameID, id uuid.UUID, tokenHash string) error {
	tag, err := q.Exec(ctx,
		`UPDATE app_hoppers_players SET token_hash = $4 WHERE tenant_id = $1 AND game_id = $2 AND id = $3`,
		tenantID, gameID, id, tokenHash)
	if err != nil {
		return fmt.Errorf("setting hoppers player token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// touchPlayer is a heartbeat: one small UPDATE, skipped when the last one is
// fresh enough, so twenty phones pinging every eight seconds cost nothing.
func touchPlayer(ctx context.Context, q Querier, tenantID, gameID, id uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE app_hoppers_players SET last_seen_at = $4
		 WHERE tenant_id = $1 AND game_id = $2 AND id = $3
		   AND (last_seen_at IS NULL OR last_seen_at < $4::timestamptz - interval '2 seconds')`,
		tenantID, gameID, id, now)
	if err != nil {
		return fmt.Errorf("touching hoppers player: %w", err)
	}
	return nil
}
