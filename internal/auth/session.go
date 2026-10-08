package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth/opaquetoken"
	"github.com/mrdon/kit/internal/auth/signedlink"
	"github.com/mrdon/kit/internal/models"
)

// Session cookies are signed with HMAC-SHA256 using a key derived from
// KIT_SESSION_SECRET (or ENCRYPTION_KEY). There is no rotation: a fresh key
// invalidates every outstanding session.
//
// The cookie carries the RAW api token (the same "kit_..." bearer secret an
// MCP client would hold), wrapped in a signedlink envelope. The signature
// lets a tampered or forged cookie fail before it costs a database lookup;
// the `api_tokens` row found by hashing the raw token is what actually
// authenticates, so logout (which deletes the row) ends the session even if
// the cookie is replayed. Session cookies and MCP bearer tokens therefore
// share one resolver, resolveToken.
//
// Rows past expires_at are swept by the "system.token_reaper" scheduled
// task; until then an expired row is simply ignored by LookupAPIToken.
//
// CSRF: the cookie is set with SameSite=Lax, Secure=true, HttpOnly=true,
// and every state-changing route runs behind RequireCSRF (csrf.go).

const (
	SessionCookieName = "kit_session"
	sessionMaxAge     = 30 * 24 * time.Hour

	// sessionPurpose domain-separates the cookie key from every other
	// signedlink user sharing the secret. v2 was the per-workspace Path
	// migration; v3 is the move onto the signedlink wire format.
	sessionPurpose = "kit-session-cookie-v3"

	// apiTokenPrefix marks a Kit bearer secret in logs and pastes.
	apiTokenPrefix = "kit_"
)

// ErrSessionMisconfigured is returned when no signing key is available.
var ErrSessionMisconfigured = errors.New("KIT_SESSION_SECRET is not set")

// SessionSigner issues and verifies session cookies.
type SessionSigner struct {
	link *signedlink.Signer
}

// NewSessionSigner creates a signer from a raw secret string. Returns
// ErrSessionMisconfigured if the secret is empty. The secret is never used
// directly: signedlink derives a purpose-specific key from it, so reusing
// ENCRYPTION_KEY as the source is safe.
func NewSessionSigner(secret string) (*SessionSigner, error) {
	link, err := signedlink.New(secret, sessionPurpose)
	if err != nil {
		return nil, ErrSessionMisconfigured
	}
	return &SessionSigner{link: link}, nil
}

// Issue mints a new `api_tokens` row bound to (tenant, user) and writes a
// signed session cookie pointing to it. The cookie is scoped to `path`,
// which callers should set to the workspace slug prefix (e.g.
// "/gravity-brewing/") so two workspace installs maintain independent
// sessions in the same browser.
//
// A second Set-Cookie with Path=/ and MaxAge=-1 is emitted alongside so
// any stale root-scope cookie from before the migration is cleared.
func (s *SessionSigner) Issue(ctx context.Context, w http.ResponseWriter, pool *pgxpool.Pool, tenantID, userID uuid.UUID, path string) error {
	return s.IssueWithTTL(ctx, w, pool, tenantID, userID, path, sessionMaxAge)
}

// IssueWithTTL is Issue with a caller-supplied lifetime. Used by the
// deep-link middleware to mint shorter-lived sessions (the token itself
// proved access for one resource at one moment; we don't want to leave
// behind a 30-day cookie from a single-tap auth).
func (s *SessionSigner) IssueWithTTL(ctx context.Context, w http.ResponseWriter, pool *pgxpool.Pool, tenantID, userID uuid.UUID, path string, ttl time.Duration) error {
	if path == "" {
		path = "/"
	}
	raw, hash, err := opaquetoken.New(apiTokenPrefix)
	if err != nil {
		return fmt.Errorf("generating token: %w", err)
	}
	expiresAt := time.Now().Add(ttl)
	if err := models.CreateAPIToken(ctx, pool, tenantID, userID, hash, expiresAt); err != nil {
		return fmt.Errorf("creating api token: %w", err)
	}
	if path != "/" {
		// Defensive clear of any lingering root-scope cookie from before
		// the Path=/{slug}/ migration.
		http.SetCookie(w, &http.Cookie{
			Name:     SessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    s.signValue(raw),
		Path:     path,
		Expires:  expiresAt,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Revoke deletes the api_tokens row backing the request's session
// cookie (if any) AND clears the cookie at the given path plus the
// defensive Path=/ sweep. Safe to call even if there is no session —
// no-ops on missing cookie.
func (s *SessionSigner) Revoke(ctx context.Context, w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, path string) {
	if raw, ok := s.extractToken(r); ok {
		hash := opaquetoken.Hash(raw)
		if err := models.DeleteAPIToken(ctx, pool, hash); err != nil {
			slog.Warn("deleting api token during revoke", "error", err)
		}
	}
	s.Clear(w, path)
	if path != "/" {
		s.Clear(w, "/")
	}
}

// Clear wipes the session cookie on the client at the given path.
func (s *SessionSigner) Clear(w http.ResponseWriter, path string) {
	if path == "" {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// Middleware reads the session cookie, verifies its HMAC, resolves the
// api_token, and injects a Caller into the request context. Requests
// without a valid session get a 401 — except HTML page navigations
// (those whose Accept header indicates text/html), which redirect to
// the tenant's login page so users don't see a bare "unauthorized"
// when their session is missing or stale.
func (s *SessionSigner) Middleware(pool *pgxpool.Pool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := s.extractToken(r)
		if !ok {
			s.denyOrRedirect(w, r)
			return
		}
		caller, err := resolveToken(r.Context(), pool, token)
		if err != nil {
			slog.Error("resolving session token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if caller == nil {
			// Stale cookie — same UX as missing: send the user to
			// login instead of a bare 401 page.
			s.denyOrRedirect(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), callerKey, caller)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// denyOrRedirect picks between a 401 (for API/JSON clients) and a 303
// redirect to the tenant login page (for HTML navigations). Returns
// 401 if the tenant slug can't be resolved from the path.
func (s *SessionSigner) denyOrRedirect(w http.ResponseWriter, r *http.Request) {
	// Debug aid: a logged-in user should never reach here on an API call.
	// Logging the path lets us tell a genuine session lapse apart from a
	// bad-vault-password 403 (which never hits this code) in prod logs.
	_, hadCookie := s.extractToken(r)
	slog.Info("auth: session denied",
		"path", r.URL.Path, "had_cookie", hadCookie, "redirect", shouldRedirectToLogin(r))
	if !shouldRedirectToLogin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	slug := r.PathValue("slug")
	if slug == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	loginURL := "/" + slug + "/login"
	if IsSafeReturnTo(r.URL.RequestURI(), slug) {
		loginURL += "?return_to=" + url.QueryEscape(r.URL.RequestURI())
	}
	http.Redirect(w, r, loginURL, http.StatusSeeOther)
}

// pageRouteKey marks a request as an HTML page navigation. Set by
// PageRoute, consumed by shouldRedirectToLogin so any auth failure on
// such a route lands the user at /{slug}/login instead of a bare 401 —
// even when the client's Accept header is non-standard (some webviews,
// curl, etc.).
type pageRouteKeyType struct{}

var pageRouteKey = pageRouteKeyType{}

// PageRoute marks the request as an HTML page navigation so any
// downstream auth failure becomes a redirect to /{slug}/login rather
// than a bare 401. Wrap per-app page handlers with this; API routes
// should not be wrapped (they should keep returning 401/403).
func PageRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), pageRouteKey, true)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// IsPageRoute reports whether the request was tagged by PageRoute.
func IsPageRoute(r *http.Request) bool {
	v, _ := r.Context().Value(pageRouteKey).(bool)
	return v
}

// shouldRedirectToLogin reports whether an auth-failure response should
// be a 303 to the tenant login page (true) or a bare 401 (false).
// Either an explicit PageRoute tag or a text/html Accept header counts.
func shouldRedirectToLogin(r *http.Request) bool {
	return IsPageRoute(r) || wantsHTML(r)
}

// wantsHTML reports whether the request's Accept header asks for
// text/html (i.e. a browser navigation, not a fetch / JSON client).
func wantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	for part := range strings.SplitSeq(accept, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "text/html") || strings.HasPrefix(part, "application/xhtml") {
			return true
		}
	}
	return false
}

// signValue wraps the raw token so a tampered cookie fails the MAC check
// without even hitting the DB. No TTL in the envelope: the api_tokens row's
// expires_at is the one lifetime, which keeps a later sliding renewal to a
// single UPDATE.
func (s *SessionSigner) signValue(raw string) string {
	return s.link.Sign([]byte(raw), 0)
}

// extractToken verifies the HMAC and returns the raw api-token on success.
func (s *SessionSigner) extractToken(r *http.Request) (string, bool) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", false
	}
	tok, err := s.link.Verify(c.Value)
	if err != nil {
		return "", false
	}
	return string(tok.Payload), true
}
