package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"
)

// pwaOAuthNonceCookie carries a short-lived nonce that binds the
// "Sign in with Slack" round-trip to the same browser session. Set when
// the PWA /login redirect fires; verified in HandleCallback before any
// session is issued. Prevents login-CSRF: an attacker who captures or
// supplies a valid Slack `code` cannot inject it into a victim's browser
// because the victim's browser won't have the matching nonce cookie.
//
// Uses the __Host- prefix (requires Secure, Path=/, no Domain), which
// forbids subdomain overrides — the tightest cookie integrity guarantee
// browsers provide.
const pwaOAuthNonceCookie = "__Host-kit_pwa_oauth"

const pwaOAuthNonceLifetime = 10 * time.Minute

// ErrPWAOAuthNonceMismatch means the nonce cookie is missing or does not
// match the state parameter. Callers should 403.
var ErrPWAOAuthNonceMismatch = errors.New("pwa oauth nonce mismatch")

// SetPWAOAuthNonce attaches the nonce to the response as a __Host-
// cookie. Called from the /{slug}/login handler before redirecting to
// Slack.
func SetPWAOAuthNonce(w http.ResponseWriter, nonce string) {
	http.SetCookie(w, &http.Cookie{
		Name:     pwaOAuthNonceCookie,
		Value:    nonce,
		Path:     "/",
		MaxAge:   int(pwaOAuthNonceLifetime.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// VerifyAndClearPWAOAuthNonce checks that `want` matches the cookie,
// then immediately clears the cookie. Single-use — a second call with
// the same nonce will fail.
func VerifyAndClearPWAOAuthNonce(w http.ResponseWriter, r *http.Request, want string) error {
	c, err := r.Cookie(pwaOAuthNonceCookie)
	if err != nil || c.Value == "" {
		return ErrPWAOAuthNonceMismatch
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(want)) != 1 {
		return ErrPWAOAuthNonceMismatch
	}
	http.SetCookie(w, &http.Cookie{
		Name:     pwaOAuthNonceCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}
