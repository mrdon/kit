// Package testdb provides a shared Postgres fixture for *_test.go files.
//
// Tests call testdb.Open(t) to get a *pgxpool.Pool against the local test
// database (started by `make up`). Migrations are applied once per process,
// so individual tests pay no startup cost beyond the first call.
//
// Tests are responsible for their own data isolation — typically by creating
// a fresh tenant per test and cleaning it up via t.Cleanup. The pool itself
// is shared and must not be closed by callers.
package testdb

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/mrdon/kit/internal/database"
)

var (
	once    sync.Once
	pool    *pgxpool.Pool
	initErr error
)

// URL returns the test database URL, defaulting to the local docker compose
// Postgres exposed by `make up`.
func URL() string {
	if u := os.Getenv("DATABASE_URL"); u != "" {
		return u
	}
	return "postgres://kit:kit@localhost:5488/kit?sslmode=disable"
}

// Open returns a shared pool against the test database, running migrations
// the first time it is called. The pool is owned by this package — do not
// close it. Fails the test if Postgres is unreachable.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(initPool)
	if initErr != nil {
		t.Fatalf("testdb: %v", initErr)
	}
	return pool
}

func initPool() {
	cfg, err := pgxpool.ParseConfig(URL())
	if err != nil {
		initErr = err
		return
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		initErr = err
		return
	}
	if err := p.Ping(context.Background()); err != nil {
		p.Close()
		initErr = err
		return
	}
	db := stdlib.OpenDBFromPool(p)
	defer db.Close()
	if err := database.Migrate(db); err != nil {
		p.Close()
		initErr = err
		return
	}
	pool = p
}
