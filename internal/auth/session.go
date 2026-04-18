package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// Session cookies are signed with HMAC-SHA256 using a process-local key
// loaded from KIT_SESSION_SECRET. We do not yet support rotation; a fresh
// key invalidates every outstanding session (per the plan's open question).
//
// The cookie value is `<api_token_id>.<hmac(api_token_id)>`. The `api_tokens`
// row it points at is the same kind issued to MCP clients — this lets the
// middleware reuse the same resolveToken path as BearerMiddleware.
//
// CSRF: the cookie is set with SameSite=Lax, Secure=true, HttpOnly=true.
// All /api/v1 endpoints speak JSON and reject any Content-Type other than
// application/json, which closes the simple-request CSRF hole (browsers
// send a CORS preflight for non-simple content types).

const (
	SessionCookieName = "kit_session"
	sessionMaxAge     = 30 * 24 * time.Hour // 30 days; reaper runs on api_tokens.expires_at
)

// ErrSessionMisconfigured is returned when no signing key is available.
var ErrSessionMisconfigured = errors.New("KIT_SESSION_SECRET is not set")

// SessionSigner issues and verifies session cookies.
type SessionSigner struct {
	key []byte
}

// NewSessionSigner creates a signer from a raw secret string. Returns
// ErrSessionMisconfigured if the secret is empty. The input is SHA256'd
// with a fixed purpose prefix ("kit-session-cookie-v1") so it is safe to
// reuse existing high-entropy key material (e.g. ENCRYPTION_KEY) as the
// source — a compromise of the derived HMAC key doesn't leak the source.
func NewSessionSigner(secret string) (*SessionSigner, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrSessionMisconfigured
	}
	h := sha256.Sum256([]byte("kit-session-cookie-v1:" + secret))
	return &SessionSigner{key: h[:]}, nil
}

// Issue mints a new `api_tokens` row bound to (tenant, user) and writes a
// signed session cookie pointing to it.
func (s *SessionSigner) Issue(ctx context.Context, w http.ResponseWriter, pool *pgxpool.Pool, tenantID, userID uuid.UUID) error {
	raw, hash, err := models.GenerateToken()
	if err != nil {
		return fmt.Errorf("generating token: %w", err)
	}
	expiresAt := time.Now().Add(sessionMaxAge)
	if err := models.CreateAPIToken(ctx, pool, tenantID, userID, hash, expiresAt); err != nil {
		return fmt.Errorf("creating api token: %w", err)
	}
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    s.signValue(raw),
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(sessionMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
	return nil
}

// Clear wipes the session cookie on the client.
func (s *SessionSigner) Clear(w http.ResponseWriter) {
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

// Middleware reads the session cookie, verifies its HMAC, resolves the
// api_token, and injects a Caller into the request context. Requests
// without a valid session get a 401.
func (s *SessionSigner) Middleware(pool *pgxpool.Pool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := s.extractToken(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		caller, err := resolveToken(r.Context(), pool, token)
		if err != nil {
			slog.Error("resolving session token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if caller == nil {
			http.Error(w, "session expired", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), callerKey, caller)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// signValue appends an HMAC tag so a tampered cookie fails the MAC check
// without even hitting the DB.
func (s *SessionSigner) signValue(raw string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(raw))
	tag := hex.EncodeToString(mac.Sum(nil))
	return raw + "." + tag
}

// extractToken verifies the HMAC and returns the raw api-token on success.
func (s *SessionSigner) extractToken(r *http.Request) (string, bool) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	raw, gotTag := parts[0], parts[1]
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(raw))
	wantTag := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(gotTag), []byte(wantTag)) {
		return "", false
	}
	return raw, true
}
