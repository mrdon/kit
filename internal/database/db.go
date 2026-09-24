package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}

	// pgxpool defaults MaxConns to max(4, NumCPU), which on a small box is
	// four. A live trivia night is twenty phones, a wall and a console all
	// holding streams and polling snapshots, and at four connections the
	// queue that forms includes the host's own action -- the one thing the
	// whole room is waiting on. A URL that sets pool_max_conns still wins.
	if config.MaxConns < 16 {
		config.MaxConns = 16
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}
