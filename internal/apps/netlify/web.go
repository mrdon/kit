package netlify

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

// pageTmpl is the netlify-app template set with the shared chrome
// header partial mixed in (Clone is the documented way to extend a
// parsed template with another file set so {{ template "chrome_header"
// . }} resolves inside our templates).
var pageTmpl = template.Must(chrome.Tmpl().ParseFS(templatesFS, "templates/*.html"))

// teamSitesGroup is one team's slice of sites, used by the picker
// template to render an <optgroup> per team.
type teamSitesGroup struct {
	TeamName string
	TeamSlug string
	Sites    []NetlifySite
}

// groupSitesByTeam splits a flat site list into per-team groups,
// preserving discovery order and labeling unknown-team sites
// (account_name empty) as "Personal". Used by handleSettingsPage
// to feed the picker's <optgroup> rendering.
func groupSitesByTeam(sites []NetlifySite) []teamSitesGroup {
	indexBySlug := map[string]int{}
	out := []teamSitesGroup{}
	for _, s := range sites {
		name := s.AccountName
		if name == "" {
			name = "Personal"
		}
		slug := s.AccountSlug
		key := slug
		if key == "" {
			key = "_personal_"
		}
		idx, ok := indexBySlug[key]
		if !ok {
			out = append(out, teamSitesGroup{TeamName: name, TeamSlug: slug})
			idx = len(out) - 1
			indexBySlug[key] = idx
		}
		out[idx].Sites = append(out[idx].Sites, s)
	}
	return out
}

// settingsPageData is the render struct for the settings page.
type settingsPageData struct {
	TenantSlug string
	StaticBase string
	ChromeCSS  string
	Title      string
	Header     chrome.Header

	// Whether the Netlify OAuth client + Kit GitHub App credentials
	// were configured at boot. False on each disables the
	// corresponding Connect button and shows a "not configured by the
	// host" hint.
	NetlifyConfigured bool
	GitHubConfigured  bool

	// Per-tenant Netlify connection state.
	NetlifyConnected   bool
	NetlifySiteName    string
	NetlifySiteID      string
	NetlifyRepoOwner   string
	NetlifyRepoName    string
	NetlifyNeedsPicker bool             // true when Netlify is OAuth'd but no site picked yet
	NetlifySites       []NetlifySite    // flat list of all sites (for fallback / count)
	NetlifySitesByTeam []teamSitesGroup // grouped for the picker <optgroup> rendering
	NetlifySitesError  string           // surfaced if the sites listing call failed

	// Per-tenant GitHub install state — sourced from the github Kit
	// app's service, since that app owns the install row.
	GitHubConnected      bool
	GitHubAccountLogin   string
	GitHubInstallationID int64

	// Banner copy populated by callback handlers via the `?msg=`
	// query param (e.g. "connected", "disconnected").
	Banner string
}

// handleSettingsPage renders the per-tenant settings page that drives
// the Netlify OAuth + GitHub App install flows. Lazily creates the
// `app_netlify_config` row on first visit so disconnect / connect
// helpers always have something to update.
func (a *App) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	caller := auth.CallerFromContext(r.Context())
	if tenant == nil || caller == nil {
		http.Error(w, "tenant or caller not resolved", http.StatusInternalServerError)
		return
	}
	cfg, err := a.svc.GetConfig(r.Context(), caller.TenantID)
	if err != nil {
		slog.Error("netlify: loading config", "tenant_id", caller.TenantID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	homeURL := fmt.Sprintf("/%s/", tenant.Slug)
	pd := settingsPageData{
		TenantSlug:        tenant.Slug,
		StaticBase:        fmt.Sprintf("/%s/apps/netlify/static", tenant.Slug),
		ChromeCSS:         chrome.HeaderCSSPath,
		Header:            chrome.For(r, a.pool, homeURL),
		Title:             "Website settings",
		NetlifyConfigured: a.svc.HasNetlifyCredentials(),
		GitHubConfigured:  a.svc.HasGitHubAppConfig(),
		NetlifyConnected:  cfg.ConnectedNetlify(),
		NetlifySiteName:   cfg.NetlifySiteName,
		NetlifySiteID:     cfg.NetlifySiteID,
		NetlifyRepoOwner:  cfg.NetlifyRepoOwner,
		NetlifyRepoName:   cfg.NetlifyRepoName,
		Banner:            r.URL.Query().Get("msg"),
	}

	// GitHub state lives in the github Kit app (one install per
	// tenant, shared substrate). Nil-safe — if the github app isn't
	// wired (e.g. boot-time misconfig), GitHub is just "not connected".
	if ghSvc := a.svc.github; ghSvc != nil {
		inst, err := ghSvc.GetInstallation(r.Context(), caller.TenantID)
		if err != nil {
			slog.Warn("netlify: loading github install", "error", err)
		} else if inst != nil {
			pd.GitHubConnected = true
			pd.GitHubAccountLogin = inst.AccountLogin
			pd.GitHubInstallationID = inst.InstallationID
		}
	}

	// "Half-connected" Netlify state: tokens are stored but no site
	// is picked yet. Render the picker dropdown so the user can
	// finish the flow without leaving the page.
	if !cfg.ConnectedNetlify() && cfg.NetlifyAccessTokenCipher != "" {
		pd.NetlifyNeedsPicker = true
		accessToken, err := a.enc.Decrypt(cfg.NetlifyAccessTokenCipher)
		if err != nil {
			slog.Error("netlify: decrypting access token for picker",
				"tenant_id", caller.TenantID, "error", err)
			pd.NetlifySitesError = "Could not decrypt stored token. Disconnect and reconnect."
		} else {
			sites, err := listNetlifySitesAcrossAccounts(r.Context(), accessToken)
			if err != nil {
				slog.Warn("netlify: listing sites", "error", err)
				pd.NetlifySitesError = "Could not load your Netlify sites. Try disconnecting and reconnecting."
			} else {
				pd.NetlifySites = sites
				pd.NetlifySitesByTeam = groupSitesByTeam(sites)
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.ExecuteTemplate(w, "settings.html", pd); err != nil {
		slog.Error("netlify: rendering settings template", "error", err)
	}
}
