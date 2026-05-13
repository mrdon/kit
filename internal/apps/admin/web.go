package admin

import (
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/web/chrome"
)

//go:embed templates/*.html
var templatesFS embed.FS

var pageTmpl = template.Must(chrome.Tmpl().ParseFS(templatesFS, "templates/*.html"))

// integrationRow is the projected view-model the template renders per
// registered Integration.
type integrationRow struct {
	Name        string
	Description string
	Slug        string
	Connected   bool
	Detail      string
	StatusError string // populated when Status() returned an error
	ManageURL   string
}

type integrationsPageData struct {
	TenantSlug string
	ChromeCSS  string
	Title      string
	Header     chrome.Header
	Rows       []integrationRow
}

func (a *App) handleIntegrationsIndex(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	caller := auth.CallerFromContext(r.Context())
	if tenant == nil || caller == nil {
		http.Error(w, "tenant or caller not resolved", http.StatusInternalServerError)
		return
	}

	registered := Integrations()
	rows := make([]integrationRow, 0, len(registered))
	for _, integ := range registered {
		row := integrationRow{
			Name:        integ.Name(),
			Description: integ.Description(),
			Slug:        integ.Slug(),
			ManageURL:   integ.ManageURL(tenant.Slug),
		}
		status, err := integ.Status(r.Context(), caller.TenantID)
		if err != nil {
			slog.Warn("admin: integration status",
				"slug", integ.Slug(), "tenant_id", caller.TenantID, "error", err)
			row.StatusError = "Could not load status."
		} else {
			row.Connected = status.Connected
			row.Detail = status.Detail
		}
		rows = append(rows, row)
	}

	pd := integrationsPageData{
		TenantSlug: tenant.Slug,
		ChromeCSS:  chrome.HeaderCSSPath,
		Title:      "Integrations",
		Header:     chrome.For(r, a.pool, fmt.Sprintf("/%s/", tenant.Slug)),
		Rows:       rows,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.ExecuteTemplate(w, "integrations.html", pd); err != nil {
		slog.Error("admin: rendering integrations template", "error", err)
	}
}
