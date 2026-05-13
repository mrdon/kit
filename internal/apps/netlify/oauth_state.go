package netlify

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// oauthStateCookie is the name of the short-lived cookie used to CSRF-
// protect the OAuth dance. Value is `<state>:<tenant-slug>` so the
// callback (which lives at a tenant-agnostic path) can recover both.
const oauthStateCookie = "kit_netlify_oauth_state"

// oauthStateTTL is the lifespan of the state cookie — long enough for
// a slow user to complete the Netlify-side authorize step, short
// enough that a stolen cookie has limited use.
const oauthStateTTL = 10 * time.Minute

// genOAuthState produces a random URL-safe state value.
func genOAuthState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// setOAuthStateCookie writes the state cookie scoped to the whole site
// (Path=/) so it travels with the tenant-agnostic callback URL. The
// tenant slug is embedded so the callback knows which tenant initiated
// the flow without trusting the session cookie alone — the session
// cookie carries the tenant identity, but matching it against the slug
// in state prevents an attacker who somehow injected one cookie from
// completing a flow under another tenant.
func setOAuthStateCookie(w http.ResponseWriter, state, slug string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state + ":" + slug,
		Path:     "/",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// readOAuthStateCookie returns the state value and slug from the
// cookie, or empty strings if the cookie is missing / malformed.
func readOAuthStateCookie(r *http.Request) (state, slug string) {
	c, err := r.Cookie(oauthStateCookie)
	if err != nil {
		return "", ""
	}
	parts := strings.SplitN(c.Value, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// clearOAuthStateCookie zeroes the cookie immediately after a
// successful (or failed) callback so it can't be replayed.
func clearOAuthStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// isSecureRequest reports whether the request arrived over HTTPS,
// considering the X-Forwarded-Proto header set by upstream proxies
// (Dokku's nginx in our prod path). Drives the Secure flag on cookies
// — must be false on plaintext local-dev requests or the browser
// silently drops them.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return strings.EqualFold(proto, "https")
	}
	return false
}
