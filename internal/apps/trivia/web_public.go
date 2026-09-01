package trivia

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// registerPublicRoutes mounts everything a phone or a TV touches.
//
// Only TenantFromPath wraps these: no session, no CSRF header, no caller. The
// people playing have no Kit accounts -- they are drinking in a bar -- and
// the TV has no credentials to present. Identity, where it exists at all, is
// the per-game team cookie.
//
// ROUTING HAZARD: GET /{slug}/ is a catch-all served by the cards PWA. Go
// 1.22's mux gives these longer literal patterns priority, so they win -- but
// the failure mode (the game page silently serving the card feed) is exactly
// the bug documented in vault/urls.go, so there is a test for it.
func registerPublicRoutes(mux apps.Mux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)
	pub := func(h http.HandlerFunc) http.Handler { return tenantMW(h) }

	// A STABLE ADDRESS FOR THE SCREEN. Without this a host has to walk over
	// to the TV and retype a URL every week, which is exactly the chore the
	// kiosk app exists to remove. "tv" is unambiguous against {game} both by
	// Go 1.22's literal-over-wildcard precedence and because a single word
	// is not a valid game name.
	mux.Handle("GET /{slug}/trivia/tv", pub(a.handleLatestDisplay))
	mux.Handle("GET /{slug}/trivia/tv.version", pub(a.handleTVVersion))

	mux.Handle("GET /{slug}/trivia/{game}", pub(a.handlePlayerPage))
	mux.Handle("GET /{slug}/trivia/{game}/me", pub(a.handleMe))
	mux.Handle("POST /{slug}/trivia/{game}/join", pub(a.handleJoin))
	mux.Handle("POST /{slug}/trivia/{game}/reclaim", pub(a.handleRedeemReclaim))
	mux.Handle("POST /{slug}/trivia/{game}/answer", pub(a.handleAnswer))
	mux.Handle("PUT /{slug}/trivia/{game}/bets", pub(a.handleBet))
	mux.Handle("GET /{slug}/trivia/{game}/state", pub(a.handlePlayerState))
	mux.Handle("GET /{slug}/trivia/{game}/stream", pub(a.handlePlayerStream))

	mux.Handle("GET /{slug}/trivia/{game}/tv", pub(a.handleDisplay))
	mux.Handle("GET /{slug}/trivia/{game}/tv/state", pub(a.handleDisplayState))
	mux.Handle("GET /{slug}/trivia/{game}/tv/stream", pub(a.handleDisplayStream))
	mux.Handle("GET /{slug}/trivia/{game}/tv.version", pub(a.handleTVVersion))
}

// resolveGame turns the URL's three-word name into a game, tenant-scoped.
func (a *App) resolveGame(w http.ResponseWriter, r *http.Request) (*Game, string, bool) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return nil, "", false
	}
	name := r.PathValue("game")
	if !IsValidGameName(name) {
		http.NotFound(w, r)
		return nil, "", false
	}
	game, err := GetGameByName(r.Context(), a.pool, tenant.ID, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return nil, "", false
		}
		serverError(w, "resolving trivia game", err)
		return nil, "", false
	}
	return game, tenant.Slug, true
}

// callerTeam resolves the cookie to a team, or uuid.Nil for a spectator.
//
// SPECTATOR MODE IS A HARD REQUIREMENT, not a nicety: somebody who opens the
// URL with no cookie -- a latecomer, a bartender, a person watching over a
// shoulder -- gets the full read-only view. Returning 401 here would make the
// public URL useless to everyone not already playing.
func (a *App) callerTeam(r *http.Request, game *Game) uuid.UUID {
	c, err := r.Cookie(TeamCookieName)
	if err != nil || c.Value == "" {
		return uuid.Nil
	}
	teamID, token, ok := ParseCookieValue(c.Value)
	if !ok {
		return uuid.Nil
	}
	team, err := FindTeamByToken(r.Context(), a.pool, game.TenantID, game.ID, teamID, HashToken(token))
	if err != nil {
		return uuid.Nil
	}
	return team.ID
}

// requireTeam is the write path's gate: answering and betting need a cookie
// that resolves to a team IN THIS GAME. Another game's cookie, or another
// team's, resolves to nothing.
func (a *App) requireTeam(w http.ResponseWriter, r *http.Request, game *Game) (uuid.UUID, bool) {
	teamID := a.callerTeam(r, game)
	if teamID == uuid.Nil {
		clientError(w, r, http.StatusUnauthorized, "join the game first")
		return uuid.Nil, false
	}
	return teamID, true
}

// handleMe answers "who am I", so the phone can render "rejoining as Bar
// Flies..." before the first round trip. 204 for a spectator.
func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	teamID := a.callerTeam(r, game)
	if teamID == uuid.Nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	teams, err := ListTeams(r.Context(), a.pool, game.TenantID, game.ID)
	if err != nil {
		serverError(w, "listing trivia teams", err)
		return
	}
	for _, t := range teams {
		if t.ID == teamID {
			writeJSON(w, map[string]string{"teamId": t.ID.String(), "name": t.Name})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type joinRequest struct {
	Name string `json:"name"`
}

func (a *App) handleJoin(w http.ResponseWriter, r *http.Request) {
	game, slug, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	team, token, err := a.svc.Join(r.Context(), game.TenantID, game.ID, req.Name)
	switch {
	case errors.Is(err, ErrGameFull):
		clientError(w, r, http.StatusConflict, "this game is full")
		return
	case errors.Is(err, ErrNameTaken):
		clientError(w, r, http.StatusConflict, "another table already took that name")
		return
	case errors.Is(err, ErrBadRequest), errors.Is(err, ErrClosed):
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		serverError(w, "joining trivia game", err)
		return
	}
	a.setTeamCookie(w, r, slug, game.Name, team.ID, token)
	writeJSON(w, map[string]string{"teamId": team.ID.String(), "name": team.Name})
}

type reclaimRequest struct {
	TeamID string `json:"teamId"`
	Code   string `json:"code"`
}

// handleRedeemReclaim is the only re-entry path for a table that lost its
// cookie. There is deliberately no "pick your team from this list": with
// twenty names on a TV screen that is an impersonation hole, so the trust
// boundary sits with the host, who can see who is asking.
func (a *App) handleRedeemReclaim(w http.ResponseWriter, r *http.Request) {
	game, slug, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	var req reclaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	teamID, err := uuid.Parse(strings.TrimSpace(req.TeamID))
	if err != nil {
		clientError(w, r, http.StatusBadRequest, "unknown team")
		return
	}
	token, err := a.svc.RedeemReclaim(r.Context(), game.TenantID, game.ID, teamID, strings.TrimSpace(req.Code))
	if err != nil {
		clientError(w, r, http.StatusForbidden, "that code is not valid")
		return
	}
	a.setTeamCookie(w, r, slug, game.Name, teamID, token)
	writeJSON(w, map[string]string{"teamId": teamID.String()})
}

// setTeamCookie issues the per-game identity.
//
// Path-scoped to this game so one phone can hold identities for two games at
// once; HttpOnly because nothing in the page needs to read it and a token in
// JS is a token in a screenshot; SameSite=Lax because the phone arrives from
// a QR scan, which is a top-level navigation.
func (a *App) setTeamCookie(w http.ResponseWriter, r *http.Request, slug, gameName string, teamID uuid.UUID, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     TeamCookieName,
		Value:    CookieValue(teamID, token),
		Path:     CookiePath(slug, gameName),
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.HasPrefix(a.baseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   CookieMaxAge,
	})
}

type answerRequest struct {
	Answer string `json:"answer"`
	Stake  *int   `json:"stake"`
}

func (a *App) handleAnswer(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	teamID, ok := a.requireTeam(w, r, game)
	if !ok {
		return
	}
	var req answerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	err := a.svc.SubmitAnswer(r.Context(), game.TenantID, game.ID, teamID, req.Answer, req.Stake)
	switch {
	case errors.Is(err, ErrClosed):
		clientError(w, r, http.StatusConflict, err.Error())
		return
	case errors.Is(err, ErrBadRequest):
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		serverError(w, "submitting trivia answer", err)
		return
	}
	a.writePlayerState(w, r, game, teamID)
}

type betRequest struct {
	Chip   int     `json:"chip"`
	SlotID *string `json:"slotId"`
}

// handleBet is a PUT of the desired placement for one chip, so every retry
// over flaky bar wifi is idempotent.
func (a *App) handleBet(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	teamID, ok := a.requireTeam(w, r, game)
	if !ok {
		return
	}
	var req betRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	var slotID *uuid.UUID
	if req.SlotID != nil && *req.SlotID != "" {
		id, err := uuid.Parse(*req.SlotID)
		if err != nil {
			clientError(w, r, http.StatusBadRequest, "unknown answer")
			return
		}
		slotID = &id
	}
	err := a.svc.PlaceChip(r.Context(), game.TenantID, game.ID, teamID, req.Chip, slotID, 0)
	switch {
	case errors.Is(err, ErrClosed):
		clientError(w, r, http.StatusConflict, err.Error())
		return
	case errors.Is(err, ErrBadRequest):
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, ErrNotFound):
		clientError(w, r, http.StatusBadRequest, "unknown answer")
		return
	case err != nil:
		serverError(w, "placing trivia bet", err)
		return
	}
	a.writePlayerState(w, r, game, teamID)
}

func (a *App) writePlayerState(w http.ResponseWriter, r *http.Request, game *Game, teamID uuid.UUID) {
	snap, err := a.svc.Snapshot(r.Context(), game.TenantID, game.ID)
	if err != nil {
		serverError(w, "loading trivia state", err)
		return
	}
	writeJSON(w, ProjectPlayer(snap, teamID))
}

func (a *App) handlePlayerState(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	teamID := a.callerTeam(r, game)
	snap, ok := a.stateSince(w, r, game.ID, game.TenantID)
	if !ok {
		return
	}
	writeJSON(w, ProjectPlayer(snap, teamID))
}

// handlePlayerStream works with NO cookie: a spectator gets the full
// read-only stream. This is load-bearing, not lenient.
func (a *App) handlePlayerStream(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	teamID := a.callerTeam(r, game)
	a.streamGame(w, r, game.ID, game.TenantID, func(s *Snapshot) any {
		return ProjectPlayer(s, teamID)
	})
}

func (a *App) handleDisplayState(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	snap, ok := a.stateSince(w, r, game.ID, game.TenantID)
	if !ok {
		return
	}
	writeJSON(w, ProjectDisplay(snap))
}

func (a *App) handleDisplayStream(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	a.streamGame(w, r, game.ID, game.TenantID, func(s *Snapshot) any { return ProjectDisplay(s) })
}
