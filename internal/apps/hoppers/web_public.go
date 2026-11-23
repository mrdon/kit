package hoppers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/livegame"
)

// maxBody caps a phone's request bodies: a frog is a name, a colour and a hat.
const maxBody = 4 << 10

// registerPublicRoutes mounts everything a phone or a TV touches. Only
// TenantFromPath wraps these: the people playing have no Kit accounts and the
// TV has no credentials. Identity, where it exists, is the per-game cookie.
func registerPublicRoutes(mux apps.Mux, a *App) {
	pub := func(h http.HandlerFunc) http.Handler { return auth.TenantFromPath(a.pool)(h) }

	// The short join link at the root, the same shape as trivia's /j/t/: no
	// workspace in it, so the QR stays small enough to scan across a room.
	// It has no {slug}, so it checks enablement itself.
	mux.Handle("GET /j/h/{code}", http.HandlerFunc(a.handleShortJoin))

	mux.Handle("GET /{slug}/hoppers/tv", pub(a.handleLatestDisplay))
	mux.Handle("GET /{slug}/hoppers/tv.version", pub(a.handleTVVersion))

	mux.Handle("GET /{slug}/hoppers/{game}", pub(a.handlePlayerPage))
	mux.Handle("GET /{slug}/hoppers/{game}/me", pub(a.handleMe))
	mux.Handle("POST /{slug}/hoppers/{game}/join", pub(a.handleJoin))
	mux.Handle("POST /{slug}/hoppers/{game}/reclaim", pub(a.handleRedeemReclaim))
	mux.Handle("POST /{slug}/hoppers/{game}/flick", pub(a.handleFlick))
	mux.Handle("GET /{slug}/hoppers/{game}/state", pub(a.handlePlayerState))
	mux.Handle("GET /{slug}/hoppers/{game}/stream", pub(a.handlePlayerStream))

	mux.Handle("GET /{slug}/hoppers/{game}/tv", pub(a.handleDisplay))
	mux.Handle("GET /{slug}/hoppers/{game}/tv/state", pub(a.handleDisplayState))
	mux.Handle("GET /{slug}/hoppers/{game}/tv/stream", pub(a.handleDisplayStream))
	mux.Handle("GET /{slug}/hoppers/{game}/tv.version", pub(a.handleTVVersion))
}

// resolveGame turns the URL's game name into a game, tenant-scoped.
func (a *App) resolveGame(w http.ResponseWriter, r *http.Request) (*Game, string, bool) {
	tenant := auth.TenantFromContext(r.Context())
	name := r.PathValue("game")
	if tenant == nil || !livegame.IsValidGameName(name) {
		http.NotFound(w, r)
		return nil, "", false
	}
	game, err := GetGameByName(r.Context(), a.pool, tenant.ID, name)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return nil, "", false
	}
	if err != nil {
		serverError(w, "resolving hoppers game", err)
		return nil, "", false
	}
	return game, tenant.Slug, true
}

// callerPlayer resolves the cookie to a player, or uuid.Nil for a spectator.
// SPECTATOR MODE IS A HARD REQUIREMENT: somebody who opens the URL with no
// cookie gets the full read-only view, never a 401.
func (a *App) callerPlayer(r *http.Request, game *Game) uuid.UUID {
	c, err := r.Cookie(PlayerCookieName)
	if err != nil || c.Value == "" {
		return uuid.Nil
	}
	id, token, ok := livegame.ParseCookieValue(c.Value)
	if !ok {
		return uuid.Nil
	}
	p, err := FindPlayerByToken(r.Context(), a.pool, game.TenantID, game.ID, id, livegame.HashToken(token))
	if err != nil {
		return uuid.Nil
	}
	return p.ID
}

func (a *App) setPlayerCookie(w http.ResponseWriter, r *http.Request, slug string, game *Game, id uuid.UUID, token string) {
	livegame.SetCookie(w, r, a.baseURL, PlayerCookieName, CookiePath(slug, game.Name), livegame.CookieValue(id, token))
}

// meResponse tells a phone who it is.
type meResponse struct {
	PlayerID string `json:"playerId"`
	Name     string `json:"name"`
}

// handleMe is "who am I", or 204 for a spectator.
func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	id := a.callerPlayer(r, game)
	if id == uuid.Nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	p, err := GetPlayer(r.Context(), a.pool, game.TenantID, game.ID, id)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, meResponse{PlayerID: p.ID.String(), Name: p.Name})
}

// handleJoin makes a frog, or edits this phone's. A new phone gets its
// cookie here and must reopen its stream: EventSource sends the cookies that
// existed when it connected.
func (a *App) handleJoin(w http.ResponseWriter, r *http.Request) {
	game, slug, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	var frog Frog
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&frog); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, token, err := a.svc.Join(r.Context(), game.TenantID, game.ID, a.callerPlayer(r, game), frog)
	switch {
	case errors.Is(err, ErrBadRequest):
		clientError(w, r, http.StatusBadRequest, strings.TrimPrefix(err.Error(), ErrBadRequest.Error()+": "))
		return
	case err != nil:
		serverError(w, "joining hoppers game", err)
		return
	}
	if token != "" {
		a.setPlayerCookie(w, r, slug, game, p.ID, token)
	}
	writeJSON(w, meResponse{PlayerID: p.ID.String(), Name: p.Name})
}

type reclaimRequest struct {
	PlayerID string `json:"playerId"`
	Code     string `json:"code"`
}

// handleRedeemReclaim is the only way back for a frog whose phone died: the
// host reads out a one-time code. There is deliberately no "pick your frog
// from this list" on its own -- with twenty names on the wall that is an
// impersonation hole -- so the trust boundary sits with the host.
func (a *App) handleRedeemReclaim(w http.ResponseWriter, r *http.Request) {
	game, slug, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	var req reclaimRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(req.PlayerID))
	if err != nil {
		clientError(w, r, http.StatusBadRequest, "unknown frog")
		return
	}
	token, err := a.svc.RedeemReclaim(r.Context(), game.TenantID, game.ID, id, strings.TrimSpace(req.Code))
	if err != nil {
		clientError(w, r, http.StatusForbidden, "that code is not valid")
		return
	}
	a.setPlayerCookie(w, r, slug, game, id, token)
	writeJSON(w, meResponse{PlayerID: id.String()})
}

// flickRequest is a phone's flick. The phone judges it and the server trusts
// it (it is a bar game): the engine clamps Power and only records it.
type flickRequest struct {
	Power   float64 `json:"power"`
	Perfect bool    `json:"perfect"`
}

// handleFlick flicks the caller's own frog. The player comes from the
// cookie, never the body, so a phone can only ever act as itself.
func (a *App) handleFlick(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	id := a.callerPlayer(r, game)
	if id == uuid.Nil {
		clientError(w, r, http.StatusUnauthorized, "join the game first")
		return
	}
	var req flickRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	snap, err := a.svc.Command(r.Context(), game.TenantID, game.ID, engine.Command{
		Type: engine.CmdFlick, Player: id.String(), Power: req.Power, Perfect: req.Perfect,
	})
	if err != nil {
		serverError(w, "flicking hoppers frog", err)
		return
	}
	writeJSON(w, ProjectPlayer(snap, id.String(), time.Now()))
}

func (a *App) handlePlayerState(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	id := a.callerPlayer(r, game)
	snap, ok := a.stateSince(w, r, game)
	if !ok {
		return
	}
	if id != uuid.Nil {
		a.heard(r, game, id, snap)
	}
	writeJSON(w, ProjectPlayer(snap, playerKey(id), time.Now()))
}

// handlePlayerStream works with NO cookie: a spectator gets the full
// read-only stream. With one, the open stream is the phone's heartbeat.
func (a *App) handlePlayerStream(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	id := a.callerPlayer(r, game)
	key := playerKey(id)
	var beat func(r *http.Request, last *Snapshot) bool
	if id != uuid.Nil {
		beat = func(r *http.Request, last *Snapshot) bool {
			// A host-issued reclaim code replaces this phone's token: the
			// frog has moved to another phone, and this one is now a
			// spectator. End the stream so it reconnects as one.
			if a.callerPlayer(r, game) != id {
				return false
			}
			a.heard(r, game, id, last)
			return true
		}
	}
	a.streamGame(w, r, game, func(s *Snapshot, now time.Time) any { return ProjectPlayer(s, key, now) }, beat)
}

func (a *App) handleDisplayState(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	if snap, ok := a.stateSince(w, r, game); ok {
		writeJSON(w, ProjectTV(snap, time.Now()))
	}
}

func (a *App) handleDisplayStream(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	a.streamGame(w, r, game, func(s *Snapshot, now time.Time) any { return ProjectTV(s, now) }, nil)
}

// heard records a phone's heartbeat. A failure costs one beat, never the
// request.
func (a *App) heard(r *http.Request, game *Game, id uuid.UUID, last *Snapshot) {
	if err := a.svc.Heard(r.Context(), game.TenantID, game.ID, id, last); err != nil && r.Context().Err() == nil {
		slog.Warn("hoppers heartbeat failed", "game_id", game.ID, "error", err)
	}
}

// playerKey is the engine's id for a player; "" is a spectator.
func playerKey(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
