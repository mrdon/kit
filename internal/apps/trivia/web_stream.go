package trivia

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/sse"
)

// snapshotEvent is the only event type on these streams. Every frame is a
// full snapshot, so there is nothing else to name.
const snapshotEvent sse.EventType = "state"

// streamGame is the shared plumbing behind all three streams. project turns
// the snapshot into whatever that surface is allowed to see.
//
// HANDLER ORDERING MATTERS: subscribe BEFORE taking the first snapshot. The
// other order loses any event published in the gap between them; this order
// can only ever deliver a stale snapshot after a fresh one, which the
// client's version check discards.
//
// Clients connect with EventSource, which gives browser reconnect for free
// and -- decisively -- sends cookies automatically while being unable to set
// headers, which is exactly why the player's identity is a cookie.
func (a *App) streamGame(w http.ResponseWriter, r *http.Request, gameID, tenantID uuid.UUID, project func(*Snapshot) any) {
	ch, cancel := a.svc.Broker().Subscribe(gameID)
	defer cancel()

	// Heal any expired phase before the first frame, so a screen that opened
	// during a stale round does not paint the wrong thing for up to 500ms.
	if err := a.svc.SweepDue(r.Context(), tenantID, gameID); err != nil {
		slog.Warn("sweeping trivia game on stream open", "game_id", gameID, "error", err)
	}
	snap, err := a.svc.Snapshot(r.Context(), tenantID, gameID)
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
	emit := func(s *Snapshot) bool {
		// A stale frame arriving after a fresher one is dropped rather than
		// repainting the room backwards.
		if s.StateVersion <= sent {
			return true
		}
		// Stamp the wall clock at send time, not at assembly time: a
		// snapshot that sat in a mailbox would otherwise hand the client a
		// skew sample that is milliseconds old and bias its countdown.
		s.ServerNow = time.Now().UTC()
		if err := writer.Emit(snapshotEvent, project(s)); err != nil {
			return false
		}
		sent = s.StateVersion
		return true
	}
	if !emit(snap) {
		return
	}

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
		}
	}
}

// handleHostStream serves the console's stream, which carries the answer.
func (a *App) handleHostStream(w http.ResponseWriter, r *http.Request) {
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	a.streamGame(w, r, game.ID, game.TenantID, func(s *Snapshot) any { return ProjectHost(s) })
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
// when it has already written the response (a 304 or an error).
func (a *App) stateSince(w http.ResponseWriter, r *http.Request, gameID, tenantID uuid.UUID) (*Snapshot, bool) {
	if err := a.svc.SweepDue(r.Context(), tenantID, gameID); err != nil {
		slog.Warn("sweeping trivia game on poll", "game_id", gameID, "error", err)
	}
	snap, err := a.svc.Snapshot(r.Context(), tenantID, gameID)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	if raw := r.URL.Query().Get("since"); raw != "" {
		if since, err := strconv.ParseInt(raw, 10, 64); err == nil && snap.StateVersion <= since {
			// Nothing new. A 204 rather than a 304 because there is no
			// entity tag involved and the client is polling a counter.
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return nil, false
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	return snap, true
}
