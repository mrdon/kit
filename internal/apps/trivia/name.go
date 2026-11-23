package trivia

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/livegame"
)

// UniqueName returns a game name (livegame's verb-animal: jumping-lion) not
// already used by a trivia game in this workspace.
func UniqueName(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (string, error) {
	return livegame.UniqueName(func(name string) (bool, error) {
		return nameTaken(ctx, pool, tenantID, name)
	})
}

func nameTaken(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM app_trivia_games WHERE tenant_id = $1 AND name = $2)`,
		tenantID, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking trivia game name: %w", err)
	}
	return exists, nil
}
