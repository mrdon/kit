package hoppers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	"github.com/mrdon/kit/internal/buildinfo"
	"github.com/mrdon/kit/internal/livegame"
)

// errRefused marks a command the engine turned down. Unlike a database
// error it does not roll the transaction back: the timers the engine
// processed on the way in are real, and are saved either way.
var errRefused = errors.New("hoppers: refused")

// relayPrefix namespaces Hoppers' channels on the cross-process relay.
const relayPrefix = "hoppers:"

// Service is the game's entry point: the pool, the live broker, and (when
// configured) the cross-process relay.
//
// Every change is ONE transaction holding the game's row lock: load the
// engine, replay heartbeats, advance its clock to now, apply the command,
// save it. The snapshot is published only after the commit, so a rollback is
// never fanned out to the room.
type Service struct {
	pool    *pgxpool.Pool
	broker  *livegame.Broker[*Snapshot]
	relay   *livegame.Relay[*Snapshot]
	baseURL string

	// now and speed are test seams: a fake clock, and the engine's timer
	// speed-up (Options.Speed). Production uses time.Now and 1.
	now   func() time.Time
	speed float64

	// cache is the newest snapshot built in this process, per game. A read
	// whose row is at the same version reuses it instead of restoring the
	// engine again; a newer row (another process moved it) rebuilds.
	cache sync.Map // uuid.UUID -> *Snapshot
}

// NewService builds the service. baseURL makes the join link the TV shows.
func NewService(pool *pgxpool.Pool, baseURL string) *Service {
	return &Service{pool: pool, broker: livegame.NewBroker[*Snapshot](), baseURL: baseURL, now: time.Now}
}

// Broker exposes the fan-out for the SSE handlers.
func (s *Service) Broker() *livegame.Broker[*Snapshot] { return s.broker }

// ConfigureRelay attaches the Redis relay; a nil client leaves fan-out
// per-process, which is exactly correct at one web process.
func (s *Service) ConfigureRelay(rdb *redis.Client) {
	s.relay = livegame.NewRelay(rdb, s.broker, relayPrefix)
}

// options are the per-process engine settings a saved game does not carry.
func (s *Service) options(row *Game) engine.Options {
	return engine.Options{
		JoinURL:    ShortJoinURL(s.baseURL, row.JoinCode),
		Build:      buildinfo.Token(),
		SeenWindow: engine.DefaultSeenWindow,
		Speed:      s.speed,
	}
}

// change runs fn against the game's engine inside the locked transaction and
// publishes if the game moved. fn may return errRefused (wrapped) to report a
// refusal without losing the clock's progress; any other error rolls back.
func (s *Service) change(ctx context.Context, tenantID, gameID uuid.UUID,
	fn func(tx pgx.Tx, g *engine.Game, now time.Time) error,
) (*Snapshot, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning hoppers change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := lockGame(ctx, tx, tenantID, gameID)
	if err != nil {
		return nil, err
	}
	g, err := engine.RestoreWith(row.Engine, s.options(row))
	if err != nil {
		return nil, fmt.Errorf("restoring hoppers game %s: %w", row.ID, err)
	}
	now := s.now().UTC()
	if err := replayHeartbeats(ctx, tx, row, g); err != nil {
		return nil, err
	}
	g.Advance(now)
	cmdErr := fn(tx, g, now)
	if cmdErr != nil && !errors.Is(cmdErr, errRefused) {
		return nil, cmdErr
	}
	before := row.StateVersion
	// The night just ended: its standings go into the history in the same
	// transaction, so the history and the game can never disagree.
	if row.Phase != string(engine.PhaseFinal) && g.Phase() == engine.PhaseFinal {
		if err := insertResult(ctx, tx, row, standingsOf(g.View(engine.RoleTV, ""))); err != nil {
			return nil, err
		}
	}
	if err := s.store(ctx, tx, row, g); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing hoppers change: %w", err)
	}
	snap := buildSnapshot(row, g)
	s.cache.Store(row.ID, snap)
	if snap.StateVersion != before {
		s.broker.Publish(row.ID, snap)
		s.relay.Publish(ctx, row.ID, snap)
	}
	return snap, cmdErr
}

// store writes the engine and its mirrored columns back onto row.
func (s *Service) store(ctx context.Context, tx pgx.Tx, row *Game, g *engine.Game) error {
	blob, err := g.Save()
	if err != nil {
		return fmt.Errorf("saving hoppers engine: %w", err)
	}
	row.Engine = blob
	row.StateVersion = int64(g.Version())
	row.Phase = string(g.Phase())
	row.NextDueAt = nil
	if next := g.NextDeadline(); !next.IsZero() {
		row.NextDueAt = &next
	}
	return saveEngine(ctx, tx, row)
}

// replayHeartbeats feeds the engine every phone ping it has not heard yet,
// in time order.
//
// Pings land in app_hoppers_players.last_seen_at (one small UPDATE) rather
// than in the engine, so a heartbeat never rewrites the game. The engine is
// the one that decides who has dropped out, though, so before it advances to
// now it is walked through the pings since it last ran: Seen(id, at)
// advances its clock to `at` first, which makes this an exact replay rather
// than an approximation.
func replayHeartbeats(ctx context.Context, tx pgx.Tx, row *Game, g *engine.Game) error {
	players, err := ListPlayers(ctx, tx, row.TenantID, row.ID)
	if err != nil {
		return err
	}
	since := g.Now()
	heard := slices.DeleteFunc(players, func(p *Player) bool {
		return p.LastSeenAt == nil || !p.LastSeenAt.After(since)
	})
	slices.SortFunc(heard, func(a, b *Player) int { return a.LastSeenAt.Compare(*b.LastSeenAt) })
	for _, p := range heard {
		g.Seen(p.ID.String(), *p.LastSeenAt)
	}
	return nil
}

// Snapshot reads a game's current state. Reads never advance the clock:
// that is the sweeper's job, and every write advances it first.
func (s *Service) Snapshot(ctx context.Context, tenantID, gameID uuid.UUID) (*Snapshot, error) {
	row, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, err
	}
	if v, ok := s.cache.Load(row.ID); ok {
		if snap := v.(*Snapshot); snap.StateVersion == row.StateVersion && snap.Title == row.Title {
			return snap, nil
		}
	}
	g, err := engine.RestoreWith(row.Engine, s.options(row))
	if err != nil {
		return nil, fmt.Errorf("restoring hoppers game %s: %w", row.ID, err)
	}
	snap := buildSnapshot(row, g)
	s.cache.Store(row.ID, snap)
	return snap, nil
}

// Command applies one engine command for a player (flick) or the host.
func (s *Service) Command(ctx context.Context, tenantID, gameID uuid.UUID, c engine.Command) (*Snapshot, error) {
	return s.change(ctx, tenantID, gameID, func(_ pgx.Tx, g *engine.Game, now time.Time) error {
		if _, err := g.Apply(c, now); err != nil {
			return fmt.Errorf("%w: %w", errRefused, err)
		}
		return nil
	})
}

// Sweep runs a game's due timers.
func (s *Service) Sweep(ctx context.Context, tenantID, gameID uuid.UUID) error {
	_, err := s.change(ctx, tenantID, gameID, func(pgx.Tx, *engine.Game, time.Time) error { return nil })
	return err
}

// Heard is a phone's heartbeat: its stream opened or pinged. Normally that is
// one throttled UPDATE. A phone the engine had already timed out is brought
// back at once, rather than waiting for the next thing to touch the game.
func (s *Service) Heard(ctx context.Context, tenantID, gameID, playerID uuid.UUID, last *Snapshot) error {
	now := s.now().UTC()
	if err := touchPlayer(ctx, s.pool, tenantID, gameID, playerID, now); err != nil {
		return err
	}
	bits, ok := last.Mine[playerID.String()]
	if !ok || bits.You == nil || bits.You.Connected {
		return nil
	}
	_, err := s.change(ctx, tenantID, gameID, func(_ pgx.Tx, g *engine.Game, now time.Time) error {
		g.Seen(playerID.String(), now)
		return nil
	})
	return err
}
