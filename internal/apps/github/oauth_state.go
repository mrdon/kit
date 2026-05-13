package github

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// oauthStateCookie carries `<state>:<slug>:<return_to>` so the
// tenant-agnostic callback can recover both the tenant and the
// origin app's settings page to bounce the user back to.
const oauthStateCookie = "kit_github_install_state"

const oauthStateTTL = 10 * time.Minute

// genOAuthState produces a random URL-safe state value.
func genOAuthState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func setOAuthStateCookie(w http.ResponseWriter, state, slug, returnTo string, secure bool) {
	val := state + ":" + slug + ":" + base64.RawURLEncoding.EncodeToString([]byte(returnTo))
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    val,
		Path:     "/",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func readOAuthStateCookie(r *http.Request) (state, slug, returnTo string) {
	c, err := r.Cookie(oauthStateCookie)
	if err != nil {
		return "", "", ""
	}
	parts := strings.SplitN(c.Value, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	state = parts[0]
	slug = parts[1]
	if decoded, err := base64.RawURLEncoding.DecodeString(parts[2]); err == nil {
		returnTo = string(decoded)
	}
	return state, slug, returnTo
}

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

func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return strings.EqualFold(proto, "https")
	}
	return false
}
