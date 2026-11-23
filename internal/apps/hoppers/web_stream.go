package hoppers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/mrdon/kit/internal/livegame"
)

// streamGame serves one surface's SSE stream through livegame.Serve.
// project builds the surface's frame at send time; beat, if set, is the
// phone's heartbeat on open and on every ping, and ends the stream by
// returning false.
func (a *App) streamGame(w http.ResponseWriter, r *http.Request, game *Game,
	project func(*Snapshot, time.Time) any, beat func(*http.Request, *Snapshot) bool,
) {
	stream := livegame.Stream[*Snapshot]{
		Broker: a.svc.Broker(),
		GameID: game.ID,
		Load: func(ctx context.Context) (*Snapshot, error) {
			return a.svc.Snapshot(ctx, game.TenantID, game.ID)
		},
		Version: func(s *Snapshot) int64 { return s.StateVersion },
		Project: func(s *Snapshot) any { return project(s, time.Now()) },
	}
	if beat != nil {
		stream.OnBeat = func(_ context.Context, last *Snapshot) bool { return beat(r, last) }
	}
	livegame.Serve(w, r, stream)
}

// stateSince is the poll fallback's shared body. It returns false when it
// has already answered (204, or an error).
func (a *App) stateSince(w http.ResponseWriter, r *http.Request, game *Game) (*Snapshot, bool) {
	snap, err := a.svc.Snapshot(r.Context(), game.TenantID, game.ID)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	if livegame.Unchanged(w, r, snap.StateVersion) {
		return nil, false
	}
	return snap, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writing hoppers json", "error", err)
	}
}

func serverError(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// clientError answers a 4xx and logs it, so a refused request is greppable.
func clientError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	slog.Warn("hoppers request refused", "status", status, "method", r.Method,
		"route", r.Pattern, "path", r.URL.Path, "reason", msg)
	http.Error(w, msg, status)
}
