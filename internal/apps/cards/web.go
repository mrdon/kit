package cards

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	webapp "github.com/mrdon/kit/web/app"
)

// registerCardsRoutes wires the PWA endpoints and the dev login. The
// actual /api/v1/stack* handlers live in stack_web.go — they dispatch
// across all registered CardProviders rather than being cards-specific.
func registerCardsRoutes(mux *http.ServeMux, a *CardsApp) {
	if a.signer == nil {
		slog.Warn("cards: session signer not configured, skipping HTTP route registration")
		return
	}

	registerStackRoutes(mux, a)

	if a.devMode {
		mux.HandleFunc("GET /app/dev-login", a.handleDevLogin)
		slog.Warn("cards: /app/dev-login is enabled (KIT_ENV=dev)")
	}

	// Real sign-in via Slack OpenID. Only registered if client creds are set.
	// The callback reuses the MCP /oauth/callback endpoint — same redirect
	// URI registered with the Slack app. The OAuthServer dispatches to
	// PWA-cookie issuance when state starts with "pwa:".
	if a.slack.ClientID != "" && a.slack.ClientSecret != "" {
		mux.HandleFunc("GET /app/login", a.handleLogin)
	} else {
		slog.Warn("cards: Slack client creds not set — /app/login disabled; use /app/dev-login in dev or bearer tokens")
	}

	// Serve the PWA at /app/. SPA fallback is handled inside webapp.Handler.
	mux.Handle("GET /app/", webapp.Handler())
}

// handleLogin starts the PWA Slack OpenID flow. The state carries a
// "pwa:" prefix + high-entropy nonce so the shared /oauth/callback
// handler can tell it apart from the MCP OAuth flow and issue a session
// cookie instead of an OAuth code.
func (a *CardsApp) handleLogin(w http.ResponseWriter, r *http.Request) {
	nonce, _, err := models.GenerateToken()
	if err != nil {
		http.Error(w, "nonce error", http.StatusInternalServerError)
		return
	}
	slackURL := auth.SlackAuthorizeURL(a.slack, a.baseURL+"/oauth/callback", "pwa:"+nonce)
	http.Redirect(w, r, slackURL, http.StatusFound)
}

// requireJSON rejects cross-origin simple-requests by insisting on
// application/json for POSTs. GETs pass through. See auth/session.go for
// the CSRF reasoning.
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// csrfHeader is the custom header a non-JSON POST must set for us to
// accept it. Custom headers take a request out of the CORS "simple
// request" category, so browsers will preflight cross-origin calls and
// same-origin calls from the PWA carry the header naturally.
const csrfHeader = "X-Kit-Chat"

// requireCSRFHeader enforces the X-Kit-Chat: 1 header on POSTs that
// aren't JSON. Used for the voice transcribe endpoint which ships audio
// as multipart/form-data. GETs pass through.
func requireCSRFHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.Header.Get(csrfHeader) != "1" {
				http.Error(w, "missing "+csrfHeader+" header", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireCallerHandler wraps a handler so it runs only if the session
// middleware left a caller in the context.
func requireCallerHandler(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.CallerFromContext(r.Context()) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	})
}

// handleDevLogin mints a session cookie for a named Slack user. Gated
// behind devMode so it cannot run in production.
//
// Usage: /app/dev-login?team=<slack_team_id>&user=<slack_user_id>
func (a *CardsApp) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	teamID := r.URL.Query().Get("team")
	userID := r.URL.Query().Get("user")
	if teamID == "" || userID == "" {
		http.Error(w, "missing ?team=<slack_team_id>&user=<slack_user_id>", http.StatusBadRequest)
		return
	}
	tenant, err := findTenantBySlackTeam(r.Context(), a.pool, teamID)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if tenant == nil {
		http.Error(w, "no tenant with that slack team id", http.StatusNotFound)
		return
	}
	user, err := models.GetUserBySlackID(r.Context(), a.pool, tenant.ID, userID)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, "no user with that slack id — send Kit a message in Slack first", http.StatusNotFound)
		return
	}
	if err := a.signer.Issue(r.Context(), w, a.pool, tenant.ID, user.ID); err != nil {
		http.Error(w, "issuing session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/app/", http.StatusSeeOther)
}

// findTenantBySlackTeam looks up a tenant by its slack_team_id. Kept as a
// private helper here to avoid adding a one-off to the models package for
// now. Returns an error from pool.QueryRow; callers treat pgx.ErrNoRows
// as "not found" via the nil tenant.
func findTenantBySlackTeam(ctx context.Context, pool *pgxpool.Pool, slackTeamID string) (*models.Tenant, error) {
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM tenants WHERE slack_team_id = $1`, slackTeamID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // not found is fine here
		}
		return nil, err
	}
	return models.GetTenantByID(ctx, pool, id)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}
