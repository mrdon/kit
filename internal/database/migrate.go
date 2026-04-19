package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrateLockKey is a stable session-level Postgres advisory lock id used
// to serialize concurrent Migrate calls. This matters in `go test ./...`
// where each test package's binary opens its own pool and races to apply
// pending migrations against a shared DB; without the lock, two parallel
// `CREATE TABLE` statements collide on pg_type. The id is arbitrary —
// any int64 unique to this codebase works.
const migrateLockKey int64 = 0x6B69745F6D696772 // "kit_migr"

func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("setting dialect: %w", err)
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquiring conn for migrate lock: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrateLockKey); err != nil {
		return fmt.Errorf("acquiring migrate advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrateLockKey)
	}()

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	return nil
}
