package trivia

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/livegame"
)

// streamGame is the shared plumbing behind all three streams. project turns
// the snapshot into whatever that surface is allowed to see; the transport
// itself (subscribe-before-snapshot, the stale-frame drop, the liveness ping)
// is livegame.Serve, shared with every other live game.
func (a *App) streamGame(w http.ResponseWriter, r *http.Request, gameID, tenantID uuid.UUID, project func(*Snapshot) any) {
	livegame.Serve(w, r, livegame.Stream[*Snapshot]{
		Broker: a.svc.Broker(),
		GameID: gameID,
		Load: func(ctx context.Context) (*Snapshot, error) {
			return a.svc.Snapshot(ctx, tenantID, gameID)
		},
		Version: func(s *Snapshot) int64 { return s.StateVersion },
		Project: func(s *Snapshot) any {
			// Stamp the wall clock at send time, not at assembly time: a
			// snapshot that sat in a mailbox would otherwise hand the client
			// a skew sample that is milliseconds old and bias its countdown.
			//
			// ON A COPY. The broker hands the SAME *Snapshot to every
			// subscriber, so stamping it in place had all twenty-odd stream
			// goroutines writing one time.Time while the others were
			// marshalling the struct around it -- a real race, and a torn
			// read becomes a garbage serverNow and a countdown stuck at zero.
			local := *s
			local.ServerNow = time.Now().UTC()
			return project(&local)
		},
	})
}

// hostStreamMaxAge caps one host connection. The session is checked when
// the stream opens and never again while it is up, so a revoked device
// would otherwise keep receiving answers for as long as the socket lived.
// Closing it makes EventSource reconnect, and the reconnect runs the
// session middleware again. Fifteen minutes is short enough to matter and
// long enough that a reconnect is never visible during a round.
const hostStreamMaxAge = 15 * time.Minute

// handleHostStream serves the console's stream, which carries the answer.
func (a *App) handleHostStream(w http.ResponseWriter, r *http.Request) {
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), hostStreamMaxAge)
	defer cancel()
	a.streamGame(w, r.WithContext(ctx), game.ID, game.TenantID, func(s *Snapshot) any { return ProjectHost(s) })
}

// handleHostState is the poll fallback. Every stream has one, because a
// client whose SSE connection is being eaten by a captive portal or a proxy
// should degrade to a few seconds of latency rather than a frozen screen.
//
// `since` is the state_version the client already has; the same counter that
// drives the stream drives this, so there is one notion of "newer" in the
// system.
func (a *App) handleHostState(w http.ResponseWriter, r *http.Request) {
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	snap, ok := a.stateSince(w, r, game.ID, game.TenantID)
	if !ok {
		return
	}
	writeJSON(w, ProjectHost(snap))
}

// stateSince is the shared body of the three poll endpoints. It returns false
// when it has already written the response (a 204 or an error).
func (a *App) stateSince(w http.ResponseWriter, r *http.Request, gameID, tenantID uuid.UUID) (*Snapshot, bool) {
	snap, err := a.svc.Snapshot(r.Context(), tenantID, gameID)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	if livegame.Unchanged(w, r, snap.StateVersion) {
		return nil, false
	}
	return snap, true
}
