package livegame

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/sse"
)

// EventState carries a full snapshot. Every frame is one, so there is
// nothing else to name about it.
const EventState sse.EventType = "state"

// EventPing is the liveness beat, and it exists because SILENCE IS NOT
// DISTINGUISHABLE FROM DEATH on a phone.
//
// The client cannot trust an EventSource that looks open -- a suspended iOS
// tab frequently does -- so it treats a gap with no frames as a dead
// connection and reconnects. That is survivable when the quiet stretches are
// a few seconds. It stops being survivable when a game holds a break for as
// long as the room wants a drink: nothing changes, so nothing is published,
// so every phone in the room tears its stream down and rebuilds it every
// twenty seconds, flashing "reconnecting" the whole time.
//
// An SSE COMMENT would keep the socket alive but fire no event in the
// browser, so the client's watchdog would still not see it. This is a real
// named event with no payload for exactly that reason.
const EventPing sse.EventType = "ping"

// PingEvery is well inside the client's 20s silence watchdog, with room for
// one to be lost.
const PingEvery = 8 * time.Second

// Stream describes one surface's live stream of one game.
type Stream[F any] struct {
	Broker *Broker[F]
	GameID uuid.UUID
	// Load reads the game's current snapshot. An error is answered 404: the
	// stream is for a game that does not exist (or is not this caller's).
	Load func(ctx context.Context) (F, error)
	// Version is the snapshot's state version: the one counter that drives
	// the stream, the poll cursor and the client's staleness check.
	Version func(F) int64
	// Project turns a snapshot into what this surface may see. It is called
	// at SEND time, so it is where a game stamps its server clock: a snapshot
	// that sat in a mailbox would otherwise hand the client a skew sample
	// that is milliseconds old. The broker hands the SAME value to every
	// subscriber, so Project must not mutate it -- stamp a copy.
	Project func(F) any
}

// Serve runs one SSE stream until the client goes away.
//
// HANDLER ORDERING MATTERS: subscribe BEFORE taking the first snapshot. The
// other order loses any event published in the gap between them; this order
// can only ever deliver a stale snapshot after a fresh one, which the
// version check discards.
//
// Clients connect with EventSource, which gives browser reconnect for free
// and -- decisively -- sends cookies automatically while being unable to set
// headers, which is exactly why a participant's identity is a cookie.
func Serve[F any](w http.ResponseWriter, r *http.Request, s Stream[F]) {
	ch, cancel := s.Broker.Subscribe(s.GameID)
	defer cancel()

	snap, err := s.Load(r.Context())
	if err != nil {
		http.NotFound(w, r)
		return
	}

	writer, err := sse.New(w, r)
	if err != nil {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	defer writer.Close()

	sent := int64(-1)
	emit := func(f F) bool {
		// A stale frame arriving after a fresher one is dropped rather than
		// repainting the room backwards.
		v := s.Version(f)
		if v <= sent {
			return true
		}
		if err := writer.Emit(EventState, s.Project(f)); err != nil {
			return false
		}
		sent = v
		return true
	}
	if !emit(snap) {
		return
	}

	beat := time.NewTicker(PingEvery)
	defer beat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case next, ok := <-ch:
			if !ok {
				return
			}
			if !emit(next) {
				return
			}
		case <-beat.C:
			// Nothing has changed and that is fine -- say so, rather than
			// letting the room read the silence as a broken connection.
			if err := writer.Emit(EventPing, struct{}{}); err != nil {
				return
			}
		}
	}
}

// Unchanged is the poll fallback's cursor check. Every stream has a poll
// beside it, because a client whose SSE connection is being eaten by a
// captive portal or a proxy should degrade to a few seconds of latency rather
// than a frozen screen.
//
// `since` is the version the client already has. When the game has nothing
// newer, Unchanged answers 204 and returns true: the caller is done. A 204
// rather than a 304 because there is no entity tag involved and the client is
// polling a counter. Either way the response is marked no-store.
func Unchanged(w http.ResponseWriter, r *http.Request, version int64) bool {
	w.Header().Set("Cache-Control", "no-store")
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return false
	}
	since, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version > since {
		return false
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}
