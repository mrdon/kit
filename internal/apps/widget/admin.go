package widget

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// AdminHandler hosts the session-gated JSON endpoints that mint and
// revoke widget tokens for the React console (/{slug}/web/admin/widget). The
// rendering lives in web/console; this layer is JSON over the existing
// WidgetTokenService. Distinct from the public widget Handler so the
// signer/services dependency stays out of the unauthenticated path.
type AdminHandler struct {
	pool    *pgxpool.Pool
	signer  *auth.SessionSigner
	svc     *services.WidgetTokenService
	baseURL string
}

// NewAdminHandler binds the admin routes to a pool, the session signer,
// the widget token service, and the deployment base URL.
func NewAdminHandler(pool *pgxpool.Pool, signer *auth.SessionSigner, svc *services.WidgetTokenService, baseURL string) *AdminHandler {
	return &AdminHandler{pool: pool, signer: signer, svc: svc, baseURL: baseURL}
}

// Register wires the console JSON routes onto the mux. All are admin-only
// (console.AdminJSON enforces 401/403 + the X-Kit-Web CSRF header).
func (h *AdminHandler) Register(mux apps.Mux) {
	if h.signer == nil {
		slog.Warn("widget admin handler: no session signer; routes not registered")
		return
	}
	adminJSON := func(handler http.HandlerFunc) http.Handler {
		return console.AdminJSON(h.pool, h.signer, handler)
	}
	mux.Handle("GET /{slug}/api/widget/tokens", adminJSON(h.handleList))
	mux.Handle("POST /{slug}/api/widget/tokens", adminJSON(h.handleMint))
	mux.Handle("POST /{slug}/api/widget/tokens/{id}/revoke", adminJSON(h.handleRevoke))
}

// tokenJSON is the wire shape for one token row.
type tokenJSON struct {
	ID             string   `json:"id"`
	Placeholder    string   `json:"placeholder"`
	AllowedOrigins []string `json:"allowed_origins"`
	CreatedAt      string   `json:"created_at"`
	LastUsedAt     string   `json:"last_used_at"`
}

func (h *AdminHandler) handleList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	tokens, err := h.svc.List(r.Context(), caller)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list tokens")
		return
	}
	out := make([]tokenJSON, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, tokenJSON{
			ID:             t.ID.String(),
			Placeholder:    t.Placeholder,
			AllowedOrigins: t.AllowedOrigins,
			CreatedAt:      t.CreatedAt,
			LastUsedAt:     t.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (h *AdminHandler) handleMint(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		Origin string `json:"origin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	origin := strings.TrimRight(strings.TrimSpace(body.Origin), "/")
	if origin == "" {
		writeErr(w, http.StatusBadRequest, "origin is required")
		return
	}
	created, err := h.svc.Create(r.Context(), caller, []string{origin})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"embed_snippet":   created.EmbedSnippet,
		"allowed_origins": created.AllowedOrigin,
	})
}

func (h *AdminHandler) handleRevoke(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid token id")
		return
	}
	if err := h.svc.Revoke(r.Context(), caller, id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
