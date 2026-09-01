package trivia

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

// processID identifies this web process for the life of the binary. It is
// stamped on every snapshot so a relayed message arriving back at the process
// that sent it is recognised and dropped, instead of being re-fanned to
// clients that already have it.
var processID = newProcessID()

func newProcessID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A collision here would only cost a duplicate fan-out of an
		// identical snapshot, which clients discard on the sequence check.
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// channelPrefix namespaces the relay's pub/sub channels.
const channelPrefix = "trivia:"

// relay carries snapshots between web processes.
//
// Kit runs as one process today, and at one process this layer buys nothing.
// At two it is the only thing that breaks without it: nginx round-robins the
// long-lived streams with no affinity, so a TV lands on process A while the
// host's click lands on B, B publishes to B's subscribers only, and the TV
// freezes mid-question. Everything else already survives -- the sweeper is
// safe by construction because both processes issue the same guarded UPDATE
// and the loser updates zero rows, and all game state is in Postgres.
//
// Redis rather than Postgres LISTEN/NOTIFY: go-redis is already a direct
// dependency and REDIS_URL is already set in production, whereas LISTEN would
// mean holding a connection out of pgxpool in every process with its own
// reconnect handling. Fire-and-forget delivery is a non-issue precisely
// because every frame is a full snapshot and the mailbox already discards
// stale ones.
type relay struct {
	rdb    *redis.Client
	broker *Broker
}

func newRelay(rdb *redis.Client, broker *Broker) *relay {
	return &relay{rdb: rdb, broker: broker}
}

// publish mirrors a locally-published snapshot onto Redis. This is the only
// method the local broker's behaviour changes for: handlers still talk to the
// in-memory broker and nothing else.
func (r *relay) publish(ctx context.Context, snap *Snapshot) {
	payload, err := json.Marshal(snap)
	if err != nil {
		slog.Warn("encoding trivia snapshot for relay", "game_id", snap.GameID, "error", err)
		return
	}
	if err := r.rdb.Publish(ctx, channelPrefix+snap.GameID.String(), payload).Err(); err != nil {
		// A failed relay costs other processes a frame; theirs will heal on
		// the next publish or the clients' poll fallback. It must never fail
		// the host's click, which has already committed.
		slog.Warn("relaying trivia snapshot", "game_id", snap.GameID, "error", err)
	}
}

// start runs the subscriber loop: one goroutine per process holding a
// PSUBSCRIBE over every game channel, feeding what it receives into the local
// broker.
func (r *relay) start(ctx context.Context) {
	sub := r.rdb.PSubscribe(ctx, channelPrefix+"*")
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
func (r *relay) deliver(msg *redis.Message) {
	var snap Snapshot
	if err := json.Unmarshal([]byte(msg.Payload), &snap); err != nil {
		slog.Warn("decoding relayed trivia snapshot", "channel", msg.Channel, "error", err)
		return
	}
	if snap.PublisherID == processID {
		return // our own message coming back around
	}
	gameID := snap.GameID
	if gameID == uuid.Nil {
		id, err := uuid.Parse(strings.TrimPrefix(msg.Channel, channelPrefix))
		if err != nil {
			return
		}
		gameID = id
	}
	r.broker.Publish(gameID, &snap)
}
