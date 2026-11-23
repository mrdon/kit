package hoppers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound is a game or player that does not exist in this workspace.
var ErrNotFound = errors.New("hoppers: not found")

// Querier is what both the pool and a transaction offer.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Game is one night's row. Engine is the saved engine; everything else is
// identity, or a column mirrored out of the engine for queries.
type Game struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Name         string
	Title        string
	JoinCode     string
	Pack         string
	Engine       []byte
	StateVersion int64
	Phase        string
	NextDueAt    *time.Time
	CreatedBy    *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const gameColumns = `id, tenant_id, name, title, join_code, pack, engine, state_version,
	phase, next_due_at, created_by, created_at, updated_at`

func scanGame(row pgx.Row) (*Game, error) {
	var g Game
	err := row.Scan(&g.ID, &g.TenantID, &g.Name, &g.Title, &g.JoinCode, &g.Pack, &g.Engine,
		&g.StateVersion, &g.Phase, &g.NextDueAt, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func oneGame(row pgx.Row, what string) (*Game, error) {
	g, err := scanGame(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return g, nil
}

// insertGame stores a new night. The join code comes from the caller so a
// unique violation on it can be retried with a fresh one.
func insertGame(ctx context.Context, q Querier, g *Game) (*Game, error) {
	return oneGame(q.QueryRow(ctx, `
		INSERT INTO app_hoppers_games
		       (tenant_id, name, title, join_code, pack, engine, state_version, phase, next_due_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+gameColumns,
		g.TenantID, g.Name, g.Title, g.JoinCode, g.Pack, g.Engine, g.StateVersion, g.Phase, g.NextDueAt, g.CreatedBy),
		"inserting hoppers game")
}

// GetGame loads a game by id, tenant-scoped.
func GetGame(ctx context.Context, q Querier, tenantID, id uuid.UUID) (*Game, error) {
	return oneGame(q.QueryRow(ctx,
		`SELECT `+gameColumns+` FROM app_hoppers_games WHERE tenant_id = $1 AND id = $2`,
		tenantID, id), "querying hoppers game")
}

// lockGame loads a game FOR UPDATE inside tx. Every engine change holds this
// lock from load to save, so two commands (or a command and the sweeper, on
// two processes) can never both apply to the same saved state.
func lockGame(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (*Game, error) {
	return oneGame(tx.QueryRow(ctx,
		`SELECT `+gameColumns+` FROM app_hoppers_games WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
		tenantID, id), "locking hoppers game")
}

// GetGameByName loads a game by its public name, tenant-scoped: a name in one
// workspace is invisible from another.
func GetGameByName(ctx context.Context, q Querier, tenantID uuid.UUID, name string) (*Game, error) {
	return oneGame(q.QueryRow(ctx,
		`SELECT `+gameColumns+` FROM app_hoppers_games WHERE tenant_id = $1 AND name = $2`,
		tenantID, name), "querying hoppers game by name")
}

// ListGames returns the workspace's newest games first.
func ListGames(ctx context.Context, q Querier, tenantID uuid.UUID, limit int) ([]*Game, error) {
	rows, err := q.Query(ctx,
		`SELECT `+gameColumns+` FROM app_hoppers_games WHERE tenant_id = $1
		  ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing hoppers games: %w", err)
	}
	defer rows.Close()
	var out []*Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning hoppers game: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// saveEngine writes the engine back with its mirrored columns. tx holds the
// row lock from lockGame.
func saveEngine(ctx context.Context, tx pgx.Tx, g *Game) error {
	_, err := tx.Exec(ctx, `
		UPDATE app_hoppers_games
		   SET engine = $3, state_version = $4, phase = $5, next_due_at = $6, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2`,
		g.TenantID, g.ID, g.Engine, g.StateVersion, g.Phase, g.NextDueAt)
	if err != nil {
		return fmt.Errorf("saving hoppers game: %w", err)
	}
	return nil
}

// nameTaken backs livegame.UniqueName.
func nameTaken(ctx context.Context, q Querier, tenantID uuid.UUID, name string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM app_hoppers_games WHERE tenant_id = $1 AND name = $2)`,
		tenantID, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking hoppers game name: %w", err)
	}
	return exists, nil
}

// GameRef is a game the sweeper has to visit.
type GameRef struct {
	TenantID uuid.UUID
	ID       uuid.UUID
}

// DueGames lists every game whose next due time has passed, across all
// workspaces. Like trivia's, this sweep is the one deliberately cross-tenant
// read: it only yields ids, and every load after it is tenant-scoped.
func DueGames(ctx context.Context, q Querier) ([]GameRef, error) {
	rows, err := q.Query(ctx,
		`SELECT tenant_id, id FROM app_hoppers_games WHERE next_due_at IS NOT NULL AND next_due_at <= now()`)
	if err != nil {
		return nil, fmt.Errorf("listing due hoppers games: %w", err)
	}
	defer rows.Close()
	var out []GameRef
	for rows.Next() {
		var r GameRef
		if err := rows.Scan(&r.TenantID, &r.ID); err != nil {
			return nil, fmt.Errorf("scanning due hoppers game: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NextDueIn is how long until the earliest due time anywhere, or ok=false
// when no game is waiting on the clock.
func NextDueIn(ctx context.Context, q Querier) (time.Duration, bool, error) {
	var ms *float64
	err := q.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (MIN(next_due_at) - now())) * 1000
		   FROM app_hoppers_games WHERE next_due_at IS NOT NULL`).Scan(&ms)
	if err != nil {
		return 0, false, fmt.Errorf("reading next hoppers deadline: %w", err)
	}
	if ms == nil {
		return 0, false, nil
	}
	return max(time.Duration(*ms)*time.Millisecond, 0), true, nil
}

// GameByJoinCode resolves a short code to its game and the workspace slug.
//
// The ONE query here that is not tenant-scoped, and it has to be: the code
// exists precisely so the URL does not carry a workspace. The code IS the
// identifier, it selects one row, and the page it leads to is public by
// design. Everything downstream is scoped by the tenant this returns.
func GameByJoinCode(ctx context.Context, q Querier, code string) (*Game, string, error) {
	var slug string
	var g Game
	err := q.QueryRow(ctx, `
		SELECT g.id, g.tenant_id, g.name, t.slug
		  FROM app_hoppers_games g JOIN tenants t ON t.id = g.tenant_id
		 WHERE g.join_code = $1`, code).Scan(&g.ID, &g.TenantID, &g.Name, &slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("resolving hoppers join code: %w", err)
	}
	return &g, slug, nil
}
