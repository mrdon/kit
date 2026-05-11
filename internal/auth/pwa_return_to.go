package auth

import (
	"net/http"
	"strings"
	"time"
)

// pwaReturnToCookie carries the page the user was trying to visit when
// the /{slug}/login redirect fired, so handlePWACallback can land them
// back on it instead of dumping them on the workspace's cards UI.
//
// Set when the /{slug}/login?continue=1 handler accepts a return_to
// query param; consumed (and cleared) in handlePWACallback after the
// Slack round-trip. Same __Host- prefix + Lax + Secure invariants as
// the nonce cookie.
const pwaReturnToCookie = "__Host-kit_pwa_return_to"

const pwaReturnToLifetime = 10 * time.Minute

// SetPWAReturnTo attaches the validated return path as a __Host- cookie.
// The caller must have already vetted the path against IsSafeReturnTo —
// this function does not re-check. Empty `path` clears the cookie.
func SetPWAReturnTo(w http.ResponseWriter, path string) {
	maxAge := int(pwaReturnToLifetime.Seconds())
	if path == "" {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     pwaReturnToCookie,
		Value:    path,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ConsumePWAReturnTo reads the cookie, validates the path is safe for
// the resolved tenant, and clears it. Returns "" if there's no cookie,
// the value fails validation, or it doesn't belong to this tenant. The
// caller should fall back to a default URL on "".
//
// Validation: the path must be syntactically a same-origin path AND
// start with `/{tenantSlug}/`. The slug check matters because the user
// could sign into a different Slack workspace than the one they started
// on; in that case the cached return_to belongs to the wrong tenant and
// we ignore it.
func ConsumePWAReturnTo(w http.ResponseWriter, r *http.Request, tenantSlug string) string {
	c, err := r.Cookie(pwaReturnToCookie)
	defer SetPWAReturnTo(w, "") // always clear, even on miss
	if err != nil || c.Value == "" {
		return ""
	}
	if !IsSafeReturnTo(c.Value, tenantSlug) {
		return ""
	}
	return c.Value
}

// IsSafeReturnTo validates that `path` is a relative same-origin path
// rooted under /{tenantSlug}/, with no scheme, host, or protocol-
// relative trick. Used by the /login handler before stashing the path
// in a cookie and by the callback before redirecting to it.
//
// Rejects:
//   - empty / too-long values
//   - anything that doesn't start with "/"
//   - "//..." (browser treats as protocol-relative URL)
//   - anything containing "://" (embedded URL)
//   - anything containing "\" (Windows path / smuggling)
//   - paths not under /{tenantSlug}/
func IsSafeReturnTo(path, tenantSlug string) bool {
	if path == "" || len(path) > 512 || tenantSlug == "" {
		return false
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	if strings.HasPrefix(path, "//") {
		return false
	}
	if strings.Contains(path, "://") || strings.ContainsRune(path, '\\') {
		return false
	}
	// Must be under /{tenantSlug}/.
	prefix := "/" + tenantSlug + "/"
	if path == prefix[:len(prefix)-1] /* exact "/slug" */ {
		return true
	}
	return strings.HasPrefix(path, prefix)
}
