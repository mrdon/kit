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
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/web/chrome"
)

//go:embed templates/*.html
var adminTemplatesFS embed.FS

//go:embed static/admin.css
var adminStaticFS embed.FS

// adminCSSPath is the URL suffix where the widget admin stylesheet is
// served (prefixed with the tenant slug). Kept in the same path tree
// as the admin pages so the middleware chain is uniform.
const adminCSSPath = "/apps/widget/static/admin.css"

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
// through the same middleware chain as vault: tenantMW →
// signer.Middleware → AssertTenantMatch → requireAdmin. HTML page
// routes redirect unauthenticated requests to the PWA login.
func (h *AdminHandler) Register(mux *http.ServeMux) {
	if h.signer == nil {
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
	static := func(handler http.HandlerFunc) http.Handler {
		return tenantMW(http.HandlerFunc(handler))
	}
	mux.Handle("GET /{slug}/apps/widget", page(h.handleList))
	mux.Handle("POST /{slug}/apps/widget/new", page(h.handleMint))
	mux.Handle("POST /{slug}/apps/widget/{id}/revoke", page(h.handleRevoke))
	mux.Handle("GET /{slug}/apps/widget/static/admin.css", static(h.handleAdminCSS))
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

// listPageData is the render data for the widget admin list page.
// Embeds chrome.PageData so the shared layout fields render uniformly
// with vault and other admin surfaces.
type listPageData struct {
	chrome.PageData
	TenantSlug    string
	Tokens        []services.WidgetTokenSummary
	Error         string
	PrefillOrigin string
}

// mintedPageData is the render data for the post-mint reveal page.
type mintedPageData struct {
	chrome.PageData
	TenantSlug     string
	EmbedSnippet   string
	AllowedOrigins []string
}

func (h *AdminHandler) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
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
		PageData: chrome.PageData{
			Title:     "Website chat widget — " + tenant.Name,
			ChromeCSS: chrome.HeaderCSSPath,
			Header:    chrome.For(r, h.pool, "/"+tenant.Slug+"/apps/widget"),
			ExtraCSS:  []string{"/" + tenant.Slug + adminCSSPath},
			MainAttrs: template.HTMLAttr(`id="widget-admin"`),
		},
		TenantSlug:    tenant.Slug,
		Tokens:        tokens,
		Error:         r.URL.Query().Get("err"),
		PrefillOrigin: r.URL.Query().Get("origin"),
	}
	if err := chrome.RenderPage(w, adminTemplatesFS, "templates/list.html", data); err != nil {
		slog.Error("rendering widget list page", "error", err)
	}
}

func (h *AdminHandler) handleMint(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
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
		http.Redirect(w, r, "/"+tenant.Slug+"/apps/widget?err="+url.QueryEscape("origin is required"), http.StatusSeeOther)
		return
	}
	created, err := h.svc.Create(r.Context(), caller, []string{origin})
	if err != nil {
		slog.Warn("creating widget token", "error", err, "tenant_id", tenant.ID)
		http.Redirect(w, r, "/"+tenant.Slug+"/apps/widget?err="+url.QueryEscape(err.Error())+"&origin="+url.QueryEscape(origin), http.StatusSeeOther)
		return
	}
	data := mintedPageData{
		PageData: chrome.PageData{
			Title:     "Widget token created — " + tenant.Name,
			ChromeCSS: chrome.HeaderCSSPath,
			Header:    chrome.For(r, h.pool, "/"+tenant.Slug+"/apps/widget"),
			ExtraCSS:  []string{"/" + tenant.Slug + adminCSSPath},
			MainAttrs: template.HTMLAttr(`id="widget-admin"`),
		},
		TenantSlug:     tenant.Slug,
		EmbedSnippet:   created.EmbedSnippet,
		AllowedOrigins: created.AllowedOrigin,
	}
	if err := chrome.RenderPage(w, adminTemplatesFS, "templates/minted.html", data); err != nil {
		slog.Error("rendering widget minted page", "error", err)
	}
}

func (h *AdminHandler) handleRevoke(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not found", http.StatusInternalServerError)
		return
	}
	caller := auth.CallerFromContext(r.Context())
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Redirect(w, r, "/"+tenant.Slug+"/apps/widget?err="+url.QueryEscape("invalid token id"), http.StatusSeeOther)
		return
	}
	if err := h.svc.Revoke(r.Context(), caller, id); err != nil {
		slog.Warn("revoking widget token", "error", err, "tenant_id", tenant.ID, "token_id", id)
		http.Redirect(w, r, "/"+tenant.Slug+"/apps/widget?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/"+tenant.Slug+"/apps/widget", http.StatusSeeOther)
}

func (h *AdminHandler) handleAdminCSS(w http.ResponseWriter, _ *http.Request) {
	body, err := adminStaticFS.ReadFile("static/admin.css")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}
