package hoppers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// fixture is a tenant and a service with a fake clock, torn down after the
// test. Every test gets its own tenant, so they can share the pool.
type fixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	svc    *Service
	tenant *models.Tenant
	ctx    context.Context

	mu    sync.Mutex
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_hoppers_test_" + uuid.NewString()
	slug := models.SanitizeSlug("hoppers-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "hoppers-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	f := &fixture{t: t, pool: pool, tenant: tenant, ctx: ctx, clock: time.Now().UTC()}
	f.svc = NewService(pool, "http://localhost:8489")
	f.svc.now = f.now
	return f
}

func (f *fixture) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}

// wait moves the fake clock on.
func (f *fixture) wait(d time.Duration) {
	f.mu.Lock()
	f.clock = f.clock.Add(d)
	f.mu.Unlock()
}

func (f *fixture) newGame() *Game {
	f.t.Helper()
	g, err := f.svc.CreateGame(f.ctx, f.tenant.ID, nil, NewGame{Title: "Frog night", Pack: "default"})
	if err != nil {
		f.t.Fatalf("creating game: %v", err)
	}
	return g
}

func (f *fixture) join(game *Game, name string) *Player {
	f.t.Helper()
	p, token, err := f.svc.Join(f.ctx, f.tenant.ID, game.ID, uuid.Nil, Frog{Name: name})
	if err != nil {
		f.t.Fatalf("joining %s: %v", name, err)
	}
	if token == "" {
		f.t.Fatal("a new phone got no token")
	}
	return p
}

func (f *fixture) snap(game *Game) *Snapshot {
	f.t.Helper()
	s, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		f.t.Fatalf("snapshot: %v", err)
	}
	return s
}

func (f *fixture) sweep(game *Game) {
	f.t.Helper()
	if err := f.svc.Sweep(f.ctx, f.tenant.ID, game.ID); err != nil {
		f.t.Fatalf("sweep: %v", err)
	}
}
