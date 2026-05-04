// Package chrome is the shared shell for Kit's non-card-UI HTML pages —
// vault, integrations, and any future server-rendered surface. It owns
// the page header (workspace icon + name, signed-in user, sign-out
// link) so users can tell at a glance the page is a legit Kit page and
// not a phishing clone, and so the look is consistent across apps.
//
// The card UI is the React PWA; it has its own header and is not a
// consumer of this package.
package chrome

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
)

//go:embed templates/header.html
var templateFS embed.FS

//go:embed static/header.css
var staticFS embed.FS

// HeaderCSSPath is the canonical URL where the shared header stylesheet
// is served. Consumers <link rel="stylesheet" href="..."> this in their
// page <head> alongside their own CSS. The route is registered globally
// once via RegisterRoutes; the path doesn't depend on tenant slug since
// the CSS is identical for every workspace.
const HeaderCSSPath = "/_/chrome/header.css"

// Header is the data the header template renders. Populate via For() in
// each page handler, or fill manually in tests / non-request contexts.
type Header struct {
	// WorkspaceName is the Slack workspace name (tenant.Name).
	WorkspaceName string
	// WorkspaceIconURL is the public per-tenant icon URL — see
	// /{slug}/icon-192.png in cards/web.go.
	WorkspaceIconURL string
	// UserDisplayName is the signed-in user's display name (Slack
	// real_name, falling back to display_name).
	UserDisplayName string
	// LogoutURL is the per-tenant sign-out endpoint — see
	// /{slug}/logout in cards/web.go.
	LogoutURL string
	// HomeURL is the brand link's destination — consumer-specific.
	// Vault points it at /{slug}/apps/vault/list; an integration page
	// might point it at the integrations index. Empty string renders
	// the brand as plain text instead of a link.
	HomeURL string
}

// For populates a Header from the request's tenant + caller context.
// homeURL is consumer-specific (e.g., /{slug}/apps/vault/list). Returns
// a zero Header (renders as a degraded but non-broken bar) if the
// request has no tenant resolved.
//
// LogoutURL + UserDisplayName are populated only when a caller is in
// context — token-authed pages (integrations setup) leave them blank
// so we don't render a misleading "Sign out" link to someone who isn't
// signed in.
func For(r *http.Request, pool *pgxpool.Pool, homeURL string) Header {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		return Header{HomeURL: homeURL}
	}
	h := Header{
		WorkspaceName:    tenant.Name,
		WorkspaceIconURL: fmt.Sprintf("/%s/icon-192.png", tenant.Slug),
		HomeURL:          homeURL,
	}
	if caller := auth.CallerFromContext(r.Context()); caller != nil {
		h.LogoutURL = fmt.Sprintf("/%s/logout", tenant.Slug)
		if u, err := models.GetUserByID(r.Context(), pool, caller.TenantID, caller.UserID); err == nil && u != nil && u.DisplayName != nil {
			h.UserDisplayName = *u.DisplayName
		}
	}
	return h
}

// Tmpl returns a parsed template containing the {{ template
// "chrome_header" . }} partial. Consuming apps clone this and call
// .ParseFS(theirOwnFS, ...) to layer their page templates on top.
func Tmpl() *template.Template {
	return template.Must(template.ParseFS(templateFS, "templates/header.html"))
}

// RegisterRoutes wires the static CSS route. Call once from main.go.
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+HeaderCSSPath, func(w http.ResponseWriter, _ *http.Request) {
		body, err := staticFS.ReadFile("static/header.css")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(body)
	})
}
