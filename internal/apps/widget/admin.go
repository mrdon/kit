package widget

import (
	"embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/web/chrome"
)

//go:embed templates/*.html
var adminTemplatesFS embed.FS

// adminTmpl is the widget-admin template set with the shared chrome
// header partial mixed in. Cloned from chrome.Tmpl() like vault/cards
// do so the {{ template "chrome_header" . }} call inside each template
// resolves.
var adminTmpl = template.Must(chrome.Tmpl().ParseFS(adminTemplatesFS, "templates/*.html"))

// AdminHandler hosts the Slack-OAuth-gated admin pages that mint and
// revoke widget tokens. Distinct from the public widget Handler so
// the dependency on signer/services stays out of the unauthenticated
// path.
type AdminHandler struct {
	pool    *pgxpool.Pool
	signer  *auth.SessionSigner
	svc     *services.WidgetTokenService
	baseURL string
}

// NewAdminHandler binds the admin routes to a pool, the existing
// Slack-OAuth signer, the widget token service, and the deployment
// base URL (used to render the embed snippet).
func NewAdminHandler(pool *pgxpool.Pool, signer *auth.SessionSigner, svc *services.WidgetTokenService, baseURL string) *AdminHandler {
	return &AdminHandler{pool: pool, signer: signer, svc: svc, baseURL: baseURL}
}

// Register wires the admin routes onto the given mux. Each route runs
// through the same middleware chain as the cards stack and vault:
// tenantMW → signer.Middleware → AssertTenantMatch → requireCaller.
// HTML page routes redirect unauthenticated requests to the PWA login.
func (h *AdminHandler) Register(mux *http.ServeMux) {
	if h.signer == nil {
		// No signer = no authentication possible; refuse to register so
		// admins see 404 rather than a permissive surface.
		slog.Warn("widget admin handler: no session signer; routes not registered")
		return
	}
	tenantMW := auth.TenantFromPath(h.pool)
	page := func(handler http.HandlerFunc) http.Handler {
		inner := tenantMW(h.signer.Middleware(h.pool, auth.AssertTenantMatch(h.signer, requireAdminHandler(handler))))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := r.Cookie(auth.SessionCookieName); errors.Is(err, http.ErrNoCookie) {
				slug := r.PathValue("slug")
				if slug == "" {
					http.Error(w, "tenant not resolved", http.StatusBadRequest)
					return
				}
				loginURL := "/" + slug + "/login"
				if auth.IsSafeReturnTo(r.URL.RequestURI(), slug) {
					loginURL += "?return_to=" + url.QueryEscape(r.URL.RequestURI())
				}
				http.Redirect(w, r, loginURL, http.StatusSeeOther)
				return
			}
			inner.ServeHTTP(w, r)
		})
	}
	mux.Handle("GET /{slug}/widget", page(h.handleList))
	mux.Handle("POST /{slug}/widget/new", page(h.handleMint))
	mux.Handle("POST /{slug}/widget/{id}/revoke", page(h.handleRevoke))
}

// requireAdminHandler refuses requests where the resolved caller is
// not a tenant admin. The session middleware has already loaded the
// caller; we just check the role flag.
func requireAdminHandler(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !caller.IsAdmin {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		h(w, r)
	})
}

type listPageData struct {
	TenantSlug string
	TenantName string
	ChromeCSS  string
	Header     chrome.Header
	Tokens     []services.WidgetTokenSummary
	Error      string
}

type mintedPageData struct {
	TenantSlug     string
	TenantName     string
	ChromeCSS      string
	Header         chrome.Header
	EmbedSnippet   string
	AllowedOrigins []string
}

func (h *AdminHandler) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := tenantFromCtx(r)
	if tenant == nil {
		http.Error(w, "tenant not found", http.StatusInternalServerError)
		return
	}
	caller := auth.CallerFromContext(r.Context())
	tokens, err := h.svc.List(r.Context(), caller)
	if err != nil {
		slog.Error("listing widget tokens", "error", err, "tenant_id", tenant.ID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := listPageData{
		TenantSlug: tenant.Slug,
		TenantName: tenant.Name,
		ChromeCSS:  chrome.HeaderCSSPath,
		Header:     buildHeader(r, h.pool, tenant),
		Tokens:     tokens,
		Error:      r.URL.Query().Get("err"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminTmpl.ExecuteTemplate(w, "widget_list.html", data); err != nil {
		slog.Error("rendering widget list page", "error", err)
	}
}

func (h *AdminHandler) handleMint(w http.ResponseWriter, r *http.Request) {
	tenant := tenantFromCtx(r)
	if tenant == nil {
		http.Error(w, "tenant not found", http.StatusInternalServerError)
		return
	}
	caller := auth.CallerFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	origin := strings.TrimSpace(r.PostForm.Get("origin"))
	origin = strings.TrimRight(origin, "/")
	if origin == "" {
		http.Redirect(w, r, "/"+tenant.Slug+"/widget?err="+url.QueryEscape("origin is required"), http.StatusSeeOther)
		return
	}
	created, err := h.svc.Create(r.Context(), caller, []string{origin})
	if err != nil {
		slog.Warn("creating widget token", "error", err, "tenant_id", tenant.ID)
		http.Redirect(w, r, "/"+tenant.Slug+"/widget?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	data := mintedPageData{
		TenantSlug:     tenant.Slug,
		TenantName:     tenant.Name,
		ChromeCSS:      chrome.HeaderCSSPath,
		Header:         buildHeader(r, h.pool, tenant),
		EmbedSnippet:   created.EmbedSnippet,
		AllowedOrigins: created.AllowedOrigin,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminTmpl.ExecuteTemplate(w, "widget_minted.html", data); err != nil {
		slog.Error("rendering widget minted page", "error", err)
	}
}

func (h *AdminHandler) handleRevoke(w http.ResponseWriter, r *http.Request) {
	tenant := tenantFromCtx(r)
	if tenant == nil {
		http.Error(w, "tenant not found", http.StatusInternalServerError)
		return
	}
	caller := auth.CallerFromContext(r.Context())
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Redirect(w, r, "/"+tenant.Slug+"/widget?err="+url.QueryEscape("invalid token id"), http.StatusSeeOther)
		return
	}
	if err := h.svc.Revoke(r.Context(), caller, id); err != nil {
		slog.Warn("revoking widget token", "error", err, "tenant_id", tenant.ID, "token_id", id)
		http.Redirect(w, r, "/"+tenant.Slug+"/widget?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/"+tenant.Slug+"/widget", http.StatusSeeOther)
}

// tenantFromCtx pulls the tenant resolved by auth.TenantFromPath out
// of the request context.
func tenantFromCtx(r *http.Request) *models.Tenant {
	return auth.TenantFromContext(r.Context())
}

// buildHeader populates the chrome header struct via the shared
// `chrome.For()` helper so this admin page styles itself like vault
// and cards.
func buildHeader(r *http.Request, pool *pgxpool.Pool, tenant *models.Tenant) chrome.Header {
	return chrome.For(r, pool, "/"+tenant.Slug+"/")
}
