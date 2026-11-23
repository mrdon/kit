package hoppers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/livegame"
)

// registerConsoleRoutes wires the host's JSON API behind /{slug}/api/hoppers.
//
// Any member may host, and so may a paired device holding hoppers.host (the
// taproom iPad): running the race is work for whoever is behind the bar
// tonight, not an admin chore.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	host := func(h http.HandlerFunc) http.Handler {
		return console.RequireCap(a.pool, a.signer, auth.CapHoppersHost, h)
	}
	mux.Handle("GET /{slug}/api/hoppers/packs", host(a.handlePacks))
	mux.Handle("GET /{slug}/api/hoppers/games", host(a.handleListGames))
	mux.Handle("POST /{slug}/api/hoppers/games", host(a.handleCreateGame))
	mux.Handle("DELETE /{slug}/api/hoppers/games/{id}", host(a.handleDeleteGame))
	mux.Handle("POST /{slug}/api/hoppers/games/{id}/advance", host(a.handleAdvance))
	mux.Handle("GET /{slug}/api/hoppers/games/{id}/state", host(a.handleHostState))
	mux.Handle("GET /{slug}/api/hoppers/games/{id}/stream", host(a.handleHostStream))
	mux.Handle("POST /{slug}/api/hoppers/games/{id}/players/{playerID}/reclaim", host(a.handleReclaim))
}

// packJSON is one choice on the setup screen.
type packJSON struct {
	Key    string                `json:"key"`
	Rules  RuleTweaks            `json:"rules"`
	Levels []engine.LevelSummary `json:"levels"`
}

// handlePacks lists what a night can be made from: a freshly generated cup,
// then every built-in pack, each with the rules it would start with so the
// setup form can show them.
func (a *App) handlePacks(w http.ResponseWriter, _ *http.Request) {
	out := []packJSON{{Key: PackGenerated, Rules: tweaksOf(engine.Default().Rules)}}
	for _, name := range engine.BuiltinPacks() {
		cfg, err := engine.BuiltinPack(name)
		if err != nil {
			continue
		}
		p := packJSON{Key: name, Rules: tweaksOf(cfg.Rules)}
		for _, l := range cfg.Levels {
			p.Levels = append(p.Levels, engine.LevelSummary{Name: l.Name, Layout: l.Layout, Theme: l.Theme, Length: l.Track.Length})
		}
		out = append(out, p)
	}
	writeJSON(w, map[string]any{"packs": out})
}

func tweaksOf(r engine.Rules) RuleTweaks {
	return RuleTweaks{Rounds: r.Rounds, HeatSize: r.HeatSize, MinFrogs: r.MinFrogs, FlickSeconds: int(r.FlickTimeout / time.Second)}
}

// gameJSON is one row of the host's game list.
type gameJSON struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Title     string    `json:"title"`
	Pack      string    `json:"pack"`
	Phase     string    `json:"phase"`
	Players   int       `json:"players"`
	Leader    string    `json:"leader"`
	CreatedAt time.Time `json:"created_at"`
	PlayURL   string    `json:"play_url"`
	JoinURL   string    `json:"join_url"`
}

func (a *App) gameToJSON(ctx context.Context, g *Game, slug string) gameJSON {
	out := gameJSON{
		ID: g.ID, Name: g.Name, Title: g.Title, Pack: g.Pack, Phase: g.Phase, CreatedAt: g.CreatedAt,
		PlayURL: strings.TrimRight(a.baseURL, "/") + JoinURL(slug, g.Name),
		JoinURL: ShortJoinURL(a.baseURL, g.JoinCode),
	}
	// The summary is best-effort: a list must render even if one game's
	// engine will not load.
	if snap, err := a.svc.Snapshot(ctx, g.TenantID, g.ID); err == nil {
		out.Players = len(snap.Mine)
		if st := snap.TV.Standings; len(st) > 0 {
			for _, p := range snap.TV.Players {
				if p.ID == st[0].Player {
					out.Leader = p.Name
				}
			}
		}
	}
	return out
}

func (a *App) handleListGames(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	games, err := ListGames(r.Context(), a.pool, tenant.ID, 50)
	if err != nil {
		serverError(w, "listing hoppers games", err)
		return
	}
	out := make([]gameJSON, 0, len(games))
	for _, g := range games {
		out = append(out, a.gameToJSON(r.Context(), g, tenant.Slug))
	}
	writeJSON(w, map[string]any{"games": out})
}

func (a *App) handleCreateGame(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var req NewGame
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	g, err := a.svc.CreateGame(r.Context(), tenant.ID, callerID(r), req)
	switch {
	case errors.Is(err, ErrBadRequest):
		clientError(w, r, http.StatusBadRequest, strings.TrimPrefix(err.Error(), ErrBadRequest.Error()+": "))
		return
	case err != nil:
		serverError(w, "creating hoppers game", err)
		return
	}
	writeJSON(w, a.gameToJSON(r.Context(), g, tenant.Slug))
}

func (a *App) handleDeleteGame(w http.ResponseWriter, r *http.Request) {
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	if err := DeleteGame(r.Context(), a.pool, game.TenantID, game.ID); err != nil {
		serverError(w, "deleting hoppers game", err)
		return
	}
	a.svc.cache.Delete(game.ID)
	w.WriteHeader(http.StatusNoContent)
}

// handleAdvance is the host's button. The body is the stage the host saw
// (see Stage); a press from a stage the game has left is a 409, which the
// console treats as "that already happened", not an error.
func (a *App) handleAdvance(w http.ResponseWriter, r *http.Request) {
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	var from Stage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&from); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	snap, err := a.svc.HostAdvance(r.Context(), game.TenantID, game.ID, from)
	switch {
	case errors.Is(err, ErrConflict):
		clientError(w, r, http.StatusConflict, "that already happened")
		return
	case err != nil:
		serverError(w, "advancing hoppers game", err)
		return
	}
	writeJSON(w, ProjectHost(snap, time.Now()))
}

func (a *App) handleHostState(w http.ResponseWriter, r *http.Request) {
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	if snap, ok := a.stateSince(w, r, game); ok {
		writeJSON(w, ProjectHost(snap, time.Now()))
	}
}

// hostStreamMaxAge caps one host connection, the way trivia's does: the
// session is checked when the stream opens and never again, so a revoked
// device would otherwise keep its controls for as long as the socket lived.
// Closing it makes EventSource reconnect through the session check.
const hostStreamMaxAge = 15 * time.Minute

func (a *App) handleHostStream(w http.ResponseWriter, r *http.Request) {
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), hostStreamMaxAge)
	defer cancel()
	a.streamGame(w, r.WithContext(ctx), game, func(s *Snapshot, now time.Time) any { return ProjectHost(s, now) }, nil)
}

// handleReclaim issues a lost phone's frog a four-digit code, for the host to
// read out. Issuing it signs the old phone out.
func (a *App) handleReclaim(w http.ResponseWriter, r *http.Request) {
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	playerID, err := uuid.Parse(r.PathValue("playerID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	code := livegame.ReclaimCode()
	if err := a.svc.IssueReclaim(r.Context(), game.TenantID, game.ID, playerID, code); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		serverError(w, "issuing hoppers reclaim code", err)
		return
	}
	writeJSON(w, map[string]string{"code": code})
}

func (a *App) gameFromPath(w http.ResponseWriter, r *http.Request) (*Game, bool) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		clientError(w, r, http.StatusNotFound, "not a game id")
		return nil, false
	}
	game, err := GetGame(r.Context(), a.pool, tenant.ID, id)
	if errors.Is(err, ErrNotFound) {
		clientError(w, r, http.StatusNotFound, "no such game")
		return nil, false
	}
	if err != nil {
		serverError(w, "loading hoppers game", err)
		return nil, false
	}
	return game, true
}

// callerID is the person behind the request, for created_by. A paired
// device is not a person, so a game it creates has no creator.
func callerID(r *http.Request) *uuid.UUID {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || !caller.IsUser() {
		return nil
	}
	id := caller.UserID
	return &id
}
