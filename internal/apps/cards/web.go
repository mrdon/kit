package cards

import (
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	webapp "github.com/mrdon/kit/web/app"
)

//go:embed templates/*.html
var cardsTemplatesFS embed.FS

var cardsPageTmpl = template.Must(template.ParseFS(cardsTemplatesFS, "templates/*.html"))

// registerCardsRoutes wires all PWA HTTP routes. Workspace-scoped routes
// under /{slug}/... require TenantFromPath to resolve the tenant first;
// authenticated routes add session middleware + AssertTenantMatch on top.
//
// The /app/assets/ prefix serves the shared Vite bundle (absolute asset
// paths baked into the build). Everything else moved under /{slug}/.
func registerCardsRoutes(mux *http.ServeMux, a *CardsApp) {
	if a.signer == nil {
		slog.Warn("cards: session signer not configured, skipping HTTP route registration")
		return
	}

	registerStackRoutes(mux, a)

	// Shared bundle — one copy serves every workspace.
	mux.Handle("GET /app/assets/", webapp.AssetHandler())

	tenantMW := auth.TenantFromPath(a.pool)

	// Per-workspace public endpoints (no session required).
	mux.Handle("GET /{slug}/", tenantMW(http.HandlerFunc(handleSPA)))
	mux.Handle("GET /{slug}/manifest.webmanifest", tenantMW(http.HandlerFunc(handleManifest)))
	mux.Handle("GET /{slug}/icon.svg", tenantMW(http.HandlerFunc(handleIconSVG)))
	mux.Handle("GET /{slug}/icon-192.png", tenantMW(http.HandlerFunc(handleIconPNG192)))
	mux.Handle("GET /{slug}/icon-512.png", tenantMW(http.HandlerFunc(handleIconPNG512)))
	mux.Handle("GET /{slug}/sw.js", tenantMW(http.HandlerFunc(handleServiceWorker)))

	// SPA fallback for client-side routes like /{slug}/stack/{...}.
	mux.Handle("GET /{slug}/stack/", tenantMW(http.HandlerFunc(handleSPA)))

	if a.devMode {
		mux.Handle("GET /{slug}/dev-login", tenantMW(http.HandlerFunc(a.handleDevLogin)))
		slog.Warn("cards: /{slug}/dev-login is enabled (KIT_ENV=dev)")
	}

	if a.slack.ClientID != "" && a.slack.ClientSecret != "" {
		mux.Handle("GET /{slug}/login", tenantMW(http.HandlerFunc(a.handleLogin)))
	} else {
		slog.Warn("cards: Slack client creds not set — /{slug}/login disabled; use /{slug}/dev-login in dev or bearer tokens")
	}

	mux.Handle("GET /{slug}/logout", tenantMW(http.HandlerFunc(a.handleLogout)))
}

// handleSPA serves the PWA HTML with per-workspace asset substitutions.
func handleSPA(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	title := tenant.Name
	if title == "" {
		title = tenant.Slug
	}
	body, err := webapp.IndexHTML(tenant.Slug, title)
	if err != nil {
		http.Error(w, "PWA not built", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// handleIconSVG serves the shared Kit logo for favicons / apple-touch-icon.
// Not per-workspace — Android uses the PNG icons from the manifest for
// the home-screen identity; this is just a browser-tab favicon.
func handleIconSVG(w http.ResponseWriter, _ *http.Request) {
	body, err := webapp.StaticFile("icon.svg")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}

func handleIconPNG192(w http.ResponseWriter, r *http.Request) { serveTenantIcon(w, r, 192) }
func handleIconPNG512(w http.ResponseWriter, r *http.Request) { serveTenantIcon(w, r, 512) }

// serveTenantIcon returns the cached Slack team icon for this workspace
// at the requested manifest size slot. Falls back to the default Kit SVG
// if no PNG was captured at install (e.g. workspace uses Slack's default
// gradient avatar).
func serveTenantIcon(w http.ResponseWriter, r *http.Request, size int) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	var body []byte
	switch size {
	case 192:
		body = tenant.Icon192
	case 512:
		body = tenant.Icon512
	}
	if len(body) > 0 {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(body)
		return
	}
	// Fallback: SVG logo re-served as the icon. Manifest declares PNG,
	// but Firefox will attempt to render whatever bytes come back; for
	// install UX this beats a broken-image icon.
	svg, err := webapp.StaticFile("icon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(svg)
}

// handleServiceWorker returns the shared SW bytes. Scope is implied by
// the URL path — registering from /{slug}/sw.js limits the SW to the
// /{slug}/ path (see web/app/public/sw.js for the slug-derivation
// logic that keys per-scope caches off self.registration.scope).
func handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	body, err := webapp.StaticFile("sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// SW must not be cached aggressively — browsers re-fetch on each
	// navigation to detect updates.
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// handleLogout revokes the request's session and bounces to the
// workspace login page. Safe to hit without an active session — the
// revoke is idempotent.
func (a *CardsApp) handleLogout(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	cookiePath := "/" + tenant.Slug + "/"
	a.signer.Revoke(r.Context(), w, r, a.pool, cookiePath)
	http.Redirect(w, r, cookiePath+"login", http.StatusSeeOther)
}

// handleLogin renders an interstitial that names the workspace and
// explains why we redirect to Slack. The interstitial's Continue link
// hits the same URL with ?continue=1, which mints the state nonce
// cookie and 302s to Slack's OpenID authorize endpoint. Without the
// page, a 401-driven SPA redirect would just bounce the user to
// slack.com with no context — first-time users especially had no idea
// why they were leaving Kit.
func (a *CardsApp) handleLogin(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("continue") != "1" {
		name := tenant.Name
		if name == "" {
			name = tenant.Slug
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := cardsPageTmpl.ExecuteTemplate(w, "login.html", map[string]any{
			"TenantSlug":    tenant.Slug,
			"WorkspaceName": name,
			"HasIcon":       len(tenant.Icon192) > 0,
		}); err != nil {
			slog.Error("cards: rendering login page", "error", err)
		}
		return
	}
	nonce, _, err := models.GenerateToken()
	if err != nil {
		http.Error(w, "nonce error", http.StatusInternalServerError)
		return
	}
	auth.SetPWAOAuthNonce(w, nonce)
	slackURL := auth.SlackAuthorizeURL(a.slack, a.baseURL+"/oauth/callback", "pwa:"+nonce, tenant.SlackTeamID)
	http.Redirect(w, r, slackURL, http.StatusFound)
}

// requireJSON rejects cross-origin simple-requests by insisting on
// application/json for POSTs. GETs pass through.
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

// handleDevLogin mints a session cookie for the path-resolved tenant.
// The Slack user ID is taken from ?user=; the tenant comes from the
// path, so the old ?team= param is gone. Gated behind devMode.
//
// Usage: /<slug>/dev-login?user=<slack_user_id>
func (a *CardsApp) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	userID := r.URL.Query().Get("user")
	if userID == "" {
		http.Error(w, "missing ?user=<slack_user_id>", http.StatusBadRequest)
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
	cookiePath := "/" + tenant.Slug + "/"
	if err := a.signer.Issue(r.Context(), w, a.pool, tenant.ID, user.ID, cookiePath); err != nil {
		http.Error(w, "issuing session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, cookiePath, http.StatusSeeOther)
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
