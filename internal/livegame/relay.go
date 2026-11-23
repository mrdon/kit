package livegame

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// processID identifies this web process for the life of the binary. It rides
// on every relayed message so one arriving back at the process that sent it
// is recognised and dropped, instead of being re-fanned to clients that
// already have it.
var processID = newProcessID()

func newProcessID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A collision here would only cost a duplicate fan-out of an
		// identical snapshot, which clients discard on the version check.
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// envelope is what goes over Redis: the snapshot plus who sent it. The game
// id is the channel name, so it is not repeated here.
type envelope struct {
	Publisher string          `json:"p"`
	Frame     json.RawMessage `json:"f"`
}

// Relay carries snapshots between web processes.
//
// Kit runs as one process today, and at one process this layer buys nothing.
// At two it is the only thing that breaks without it: nginx round-robins the
// long-lived streams with no affinity, so a TV lands on process A while the
// host's click lands on B, B publishes to B's subscribers only, and the TV
// freezes mid-game. Everything else already survives, because all game state
// is in Postgres and every write is a guarded UPDATE.
//
// Redis rather than Postgres LISTEN/NOTIFY: go-redis is already a direct
// dependency and REDIS_URL is already set in production, whereas LISTEN would
// mean holding a connection out of pgxpool in every process with its own
// reconnect handling. Fire-and-forget delivery is a non-issue precisely
// because every frame is a full snapshot and the mailbox already discards
// stale ones.
//
// F must round-trip through encoding/json: whatever a game's snapshot
// carries, a relayed copy has to arrive with all of it.
type Relay[F any] struct {
	rdb    *redis.Client
	broker *Broker[F]
	// prefix namespaces the pub/sub channels, one per game app ("trivia:"),
	// so two games' relays never hear each other.
	prefix string
	// self is the process id stamped on what this relay sends and dropped on
	// what it hears. Always processID outside tests, which give two relays
	// in one binary different ids to stand in for two processes.
	self string
}

// NewRelay builds a relay for one game app. rdb may be nil, in which case it
// returns nil and every method on the nil relay is a no-op: fan-out stays
// per-process, which is exactly correct at one web process.
func NewRelay[F any](rdb *redis.Client, broker *Broker[F], prefix string) *Relay[F] {
	if rdb == nil {
		return nil
	}
	return &Relay[F]{rdb: rdb, broker: broker, prefix: prefix, self: processID}
}

// Publish mirrors a locally-published snapshot onto Redis. This is the only
// method the local broker's behaviour changes for: handlers still talk to the
// in-memory broker and nothing else.
func (r *Relay[F]) Publish(ctx context.Context, gameID uuid.UUID, snap F) {
	if r == nil {
		return
	}
	frame, err := json.Marshal(snap)
	if err != nil {
		slog.Warn("encoding live snapshot for relay", "prefix", r.prefix, "game_id", gameID, "error", err)
		return
	}
	payload, err := json.Marshal(envelope{Publisher: r.self, Frame: frame})
	if err != nil {
		slog.Warn("encoding live relay envelope", "prefix", r.prefix, "game_id", gameID, "error", err)
		return
	}
	if err := r.rdb.Publish(ctx, r.prefix+gameID.String(), payload).Err(); err != nil {
		// A failed relay costs other processes a frame; theirs will heal on
		// the next publish or the clients' poll fallback. It must never fail
		// the host's click, which has already committed.
		slog.Warn("relaying live snapshot", "prefix", r.prefix, "game_id", gameID, "error", err)
	}
}

// Start runs the subscriber loop: one goroutine per process holding a
// PSUBSCRIBE over every game channel under the prefix, feeding what it
// receives into the local broker. It stops when ctx ends.
func (r *Relay[F]) Start(ctx context.Context) {
	if r == nil {
		return
	}
	sub := r.rdb.PSubscribe(ctx, r.prefix+"*")
	go func() {
		defer func() { _ = sub.Close() }()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				r.deliver(msg)
			}
		}
	}()
}

// deliver decodes one relayed message and hands it to the local broker,
// unless this process is the one that sent it.
func (r *Relay[F]) deliver(msg *redis.Message) {
	gameID, err := uuid.Parse(strings.TrimPrefix(msg.Channel, r.prefix))
	if err != nil {
		return
	}
	var env envelope
	if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
		slog.Warn("decoding live relay envelope", "channel", msg.Channel, "error", err)
		return
	}
	if env.Publisher == r.self {
		return // our own message coming back around
	}
	var snap F
	if err := json.Unmarshal(env.Frame, &snap); err != nil {
		slog.Warn("decoding relayed live snapshot", "channel", msg.Channel, "error", err)
		return
	}
	r.broker.Publish(gameID, snap)
}
