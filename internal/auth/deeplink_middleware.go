package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DeepLinkMiddlewareConfig configures DeepLinkSigner.Middleware. The
// middleware is purely additive: requests without a "t" query parameter
// pass through untouched. Successful token consumption mints a session
// cookie and 302s to the same URL with "t" removed.
type DeepLinkMiddlewareConfig struct {
	// Pool is used to create the api_tokens row backing the new session.
	Pool *pgxpool.Pool

	// Sessions issues the session cookie on successful token consumption.
	// Must not be nil.
	Sessions *SessionSigner

	// SessionTTL is how long the minted session cookie + api_tokens row
	// stays valid. Should be shorter than the OAuth-issued default; the
	// token proved access for one tap, not an interactive sign-in.
	SessionTTL time.Duration

	// BindCheck verifies any app-specific resource binding the token
	// carries (e.g. entry_id in the URL path matches Claims.EntryID).
	// Return false to reject with DeepLinkEntryMismatch. May be nil to
	// skip the check (no resource binding).
	BindCheck func(r *http.Request, c *Claims) bool

	// OnError handles every token-failure path. Claims is non-nil when
	// the HMAC verified (so callers may safely write audit rows scoped
	// to the token's tenant); it is nil for DeepLinkBadSig and
	// DeepLinkMalformed where the payload is untrusted — those should
	// log via slog only and not touch the audit table. The handler is
	// responsible for the entire HTTP response.
	OnError func(w http.ResponseWriter, r *http.Request, reason DeepLinkReason, claims *Claims)

	// OnSuccess fires after the jti has been consumed and the session
	// has been minted (or skipped, if the same user already had one),
	// but before the redirect is written. Callers should use it to
	// write a success audit row. May be nil. The handler MUST NOT
	// write to the response — that breaks the 303 contract — only do
	// out-of-band work (audit, metrics).
	OnSuccess func(r *http.Request, claims *Claims)
}

// Middleware returns an HTTP middleware that consumes a deep-link token
// from the "t" query parameter and, on success, mints a session cookie
// for the bound user. Requests without a "t" parameter fall through.
//
// Verification order:
//  1. Parse + HMAC + expiry. Failure → onError(reason, nil) for bad_sig
//     and malformed; onError(expired, claims) for expired (claims are
//     signature-verified so they're safe to log against).
//  2. Tenant binding: claims.TenantID == TenantFromContext(r).ID.
//     Failure → onError(tenant_mismatch, claims).
//  3. Resource binding: cfg.BindCheck(r, claims). Failure →
//     onError(entry_mismatch, claims).
//  4. jti single-use. Failure → onError(consumed, claims).
//  5. Existing session: if the request already has a valid cookie for
//     the same user as the token, skip minting; if it has a cookie for
//     a different user, revoke first.
//  6. Mint the new session, set Referrer-Policy: no-referrer on the
//     redirect (so the token doesn't leak via Referer on later requests
//     for assets on the destination page), 302 to the cleaned URL.
//
// The middleware MUST run after TenantFromPath (it reads
// TenantFromContext) and BEFORE the cookie-based SessionSigner.Middleware
// (it leaves the just-minted cookie for that middleware to resolve on
// the subsequent request after the 302).
func (s *DeepLinkSigner) Middleware(cfg DeepLinkMiddlewareConfig) func(http.Handler) http.Handler {
	if cfg.Sessions == nil {
		// A misconfigured deep-link path is safer to 500 than to
		// silently disable verification. The returned middleware will
		// reject every request that carries a token.
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("t") != "" {
					http.Error(w, "deep-link not configured", http.StatusInternalServerError)
					return
				}
				next.ServeHTTP(w, r)
			})
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.URL.Query().Get("t")
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			claims, err := s.Verify(token)
			if err != nil {
				var ve *VerifyError
				if errors.As(err, &ve) {
					switch ve.Reason {
					case DeepLinkBadSig, DeepLinkMalformed:
						s.invokeOnError(cfg, w, r, ve.Reason, nil)
					case DeepLinkExpired:
						// Re-parse to recover sig-verified identity for
						// the audit handler. VerifySignature skips the
						// expiry check so the trusted tenant survives.
						trusted, sigErr := s.VerifySignature(token)
						if sigErr != nil {
							s.invokeOnError(cfg, w, r, DeepLinkExpired, nil)
							return
						}
						s.invokeOnError(cfg, w, r, DeepLinkExpired, trusted)
					case DeepLinkConsumed, DeepLinkTenantMismatch, DeepLinkEntryMismatch:
						// Verify() never returns these reasons (they're
						// middleware-level decisions), but switch must
						// stay exhaustive in case the enum grows.
						s.invokeOnError(cfg, w, r, ve.Reason, nil)
					}
					return
				}
				s.invokeOnError(cfg, w, r, DeepLinkMalformed, nil)
				return
			}

			pathTenant := TenantFromContext(r.Context())
			if pathTenant == nil || pathTenant.ID != claims.TenantID {
				s.invokeOnError(cfg, w, r, DeepLinkTenantMismatch, claims)
				return
			}

			if cfg.BindCheck != nil && !cfg.BindCheck(r, claims) {
				s.invokeOnError(cfg, w, r, DeepLinkEntryMismatch, claims)
				return
			}

			if consErr := s.ConsumeJTI(claims.JTI); consErr != nil {
				var ve *VerifyError
				if errors.As(consErr, &ve) {
					s.invokeOnError(cfg, w, r, ve.Reason, claims)
					return
				}
				s.invokeOnError(cfg, w, r, DeepLinkConsumed, claims)
				return
			}

			slug := r.PathValue("slug")
			cookiePath := "/"
			if slug != "" {
				cookiePath = "/" + slug + "/"
			}

			skipMint := false
			if existingRaw, ok := cfg.Sessions.extractToken(r); ok {
				if existing, lookupErr := resolveToken(r.Context(), cfg.Pool, existingRaw); lookupErr == nil && existing != nil {
					if existing.UserID == claims.UserID && existing.TenantID == claims.TenantID {
						// Same user — leave the cookie alone. 302 to
						// the clean URL so the token doesn't sit in
						// the address bar.
						skipMint = true
					} else {
						// Cross-user: log + revoke before minting.
						// Don't silently swap one user's session for
						// another's.
						slog.Warn("deep-link: revoking existing session for different user",
							"existing_user", existing.UserID,
							"token_user", claims.UserID,
						)
						cfg.Sessions.Revoke(r.Context(), w, r, cfg.Pool, cookiePath)
					}
				}
			}

			if !skipMint {
				ttl := cfg.SessionTTL
				if ttl <= 0 {
					ttl = 1 * time.Hour
				}
				if err := cfg.Sessions.IssueWithTTL(r.Context(), w, cfg.Pool, claims.TenantID, claims.UserID, cookiePath, ttl); err != nil {
					slog.Error("deep-link: issuing session", "error", err)
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			}
			if cfg.OnSuccess != nil {
				cfg.OnSuccess(r, claims)
			}
			s.redirectClean(w, r)
		})
	}
}

// redirectClean sets Referrer-Policy: no-referrer (so the
// stripped-from-history token can't leak via Referer on subsequent
// cross-origin asset fetches) and 303s to the same URL with "t" removed.
// 303 (See Other) matches the project's convention for post-action
// redirects and forces a GET on the destination.
func (s *DeepLinkSigner) redirectClean(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, stripQueryParam(r.URL, "t"), http.StatusSeeOther)
}

func (s *DeepLinkSigner) invokeOnError(cfg DeepLinkMiddlewareConfig, w http.ResponseWriter, r *http.Request, reason DeepLinkReason, claims *Claims) {
	if cfg.OnError != nil {
		cfg.OnError(w, r, reason, claims)
		return
	}
	http.Error(w, "deep-link rejected: "+string(reason), http.StatusForbidden)
}

// stripQueryParam returns the URL's request-uri form with the named
// query parameter removed. Other parameters survive.
func stripQueryParam(u *url.URL, name string) string {
	q := u.Query()
	q.Del(name)
	encoded := q.Encode()
	if encoded == "" {
		return u.Path
	}
	return u.Path + "?" + encoded
}
