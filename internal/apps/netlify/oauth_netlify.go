package netlify

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/mrdon/kit/internal/auth"
)

// netlifyRedirectURI builds the absolute callback URL Netlify will
// send the user back to after authorize. Must match the redirect URI
// registered with the Netlify OAuth app exactly.
func (a *App) netlifyRedirectURI() string {
	return a.baseURL + "/oauth/netlify/callback"
}

// handleNetlifyConnect kicks off the Netlify OAuth dance. Caller must
// be an admin of the tenant identified by the URL slug (enforced by
// the middleware chain). Generates a CSRF state value, stores it in a
// short-lived cookie alongside the slug, and redirects the user to
// Netlify's authorize endpoint.
func (a *App) handleNetlifyConnect(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	if !a.svc.HasNetlifyCredentials() {
		http.Error(w, "netlify oauth not configured for this kit install", http.StatusServiceUnavailable)
		return
	}
	state, err := genOAuthState()
	if err != nil {
		slog.Error("netlify: generating state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setOAuthStateCookie(w, state, tenant.Slug, isSecureRequest(r))

	q := url.Values{
		"response_type": {"code"},
		"client_id":     {a.svc.netlifyClientID},
		"redirect_uri":  {a.netlifyRedirectURI()},
		"state":         {state},
	}
	http.Redirect(w, r, netlifyOAuthAuthorize+"?"+q.Encode(), http.StatusSeeOther)
}

// handleNetlifyCallbackBounce runs at /oauth/netlify/callback — the
// fixed redirect URI registered with Netlify's OAuth app. It only
// reads the slug from the (root-scoped) state cookie and 303s to the
// per-tenant callback URL so the session cookie (Path=/{slug}/)
// gets sent by the browser and the full auth chain runs at
// handleNetlifyCallback.
//
// Doesn't clear the state cookie or validate state vs URL state —
// both happen at the per-tenant handler so any tampered request
// gets the same uniform error response.
func (a *App) handleNetlifyCallbackBounce(w http.ResponseWriter, r *http.Request) {
	_, slug := readOAuthStateCookie(r)
	if slug == "" {
		http.Error(w, "missing or expired connect state — restart from Kit", http.StatusBadRequest)
		return
	}
	dest := "/" + slug + "/oauth/netlify/callback"
	if q := r.URL.RawQuery; q != "" {
		dest += "?" + q
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// handleNetlifyCallback runs at /{slug}/oauth/netlify/callback after
// the top-level bouncer 303s here. Verifies state, cross-checks the
// cookie slug against the URL slug, exchanges the authorization code
// for tokens, encrypts them, persists, and redirects to
// /{slug}/apps/netlify/settings so the site picker can render.
func (a *App) handleNetlifyCallback(w http.ResponseWriter, r *http.Request) {
	if !a.svc.HasNetlifyCredentials() {
		http.Error(w, "netlify oauth not configured", http.StatusServiceUnavailable)
		return
	}
	urlState := r.URL.Query().Get("state")
	cookieState, cookieSlug := readOAuthStateCookie(r)
	clearOAuthStateCookie(w, isSecureRequest(r))
	if urlState == "" || cookieState == "" || urlState != cookieState {
		http.Error(w, "oauth state mismatch", http.StatusBadRequest)
		return
	}
	if errCode := r.URL.Query().Get("error"); errCode != "" {
		desc := r.URL.Query().Get("error_description")
		slog.Warn("netlify: oauth user-denied", "error", errCode, "desc", desc)
		a.redirectToSettings(w, r, cookieSlug, "Connection cancelled.")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	// Auth chain has already validated session, tenant slug, and admin
	// role (this runs under the per-tenant `page` middleware). Cross-
	// check that the state cookie's slug matches the URL slug so a
	// stolen state cookie can't be used to plant tokens under a
	// different tenant.
	tenant := auth.TenantFromContext(r.Context())
	caller := auth.CallerFromContext(r.Context())
	if tenant == nil || caller == nil {
		http.Error(w, "tenant or caller not resolved", http.StatusInternalServerError)
		return
	}
	if cookieSlug != tenant.Slug {
		slog.Warn("netlify: callback slug mismatch",
			"cookie_slug", cookieSlug, "url_slug", tenant.Slug)
		http.Error(w, "connect state slug mismatch", http.StatusBadRequest)
		return
	}

	tok, err := exchangeNetlifyCode(r.Context(),
		a.svc.netlifyClientID, a.svc.netlifyClientSecret,
		code, a.netlifyRedirectURI())
	if err != nil {
		slog.Error("netlify: code exchange", "tenant_id", caller.TenantID, "error", err)
		http.Error(w, "netlify code exchange failed", http.StatusBadGateway)
		return
	}

	accessCipher, err := a.enc.Encrypt(tok.AccessToken)
	if err != nil {
		slog.Error("netlify: encrypting access token", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var refreshCipher string
	if tok.RefreshToken != "" {
		refreshCipher, err = a.enc.Encrypt(tok.RefreshToken)
		if err != nil {
			slog.Error("netlify: encrypting refresh token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.ExpiresIn == 0 {
		// Netlify long-lived tokens — record a far-future expiry so the
		// non-NULL contract holds and the refresh-on-expiry logic just
		// never fires.
		expiresAt = time.Now().Add(365 * 24 * time.Hour)
	}
	if err := SaveNetlifyTokens(r.Context(), a.pool, caller.TenantID,
		accessCipher, refreshCipher, expiresAt); err != nil {
		slog.Error("netlify: saving tokens", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	a.redirectToSettings(w, r, tenant.Slug, "Netlify connected. Pick a site below.")
}

// redirectToSettings issues a 303 back to the console Website settings
// page, with an optional banner message the page surfaces via ?msg=.
// Site-pick and disconnect are now JSON endpoints (web_console.go); this
// is used only by the OAuth connect/callback flow, which is full-page.
func (a *App) redirectToSettings(w http.ResponseWriter, r *http.Request, slug, msg string) {
	dest := "/" + slug + "/web/netlify"
	if msg != "" {
		dest += "?msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
