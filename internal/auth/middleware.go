package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth/opaquetoken"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

type contextKey string

const callerKey contextKey = "caller"

// CallerFromContext extracts the authenticated Caller from the request context.
func CallerFromContext(ctx context.Context) *services.Caller {
	c, _ := ctx.Value(callerKey).(*services.Caller)
	return c
}

// WithCaller returns ctx with the Caller injected under the same key
// CallerFromContext reads. Exported so packages that bypass HTTP (e.g.,
// the MCP session-register hooks in tests) can build a ctx without
// depending on the private context-key type.
func WithCaller(ctx context.Context, caller *services.Caller) context.Context {
	return context.WithValue(ctx, callerKey, caller)
}

// BearerMiddleware extracts a Bearer token, resolves it to a Caller, and adds it to context.
// Returns 401 if no valid token is found.
func BearerMiddleware(pool *pgxpool.Pool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		caller, err := resolveUserToken(r.Context(), pool, token)
		if err != nil {
			slog.Error("resolving token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if caller == nil {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), callerKey, caller)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// InjectCallerFromRequest resolves a Bearer token from the request and returns
// a context with the Caller injected. Used by mcp-go's HTTPContextFunc.
func InjectCallerFromRequest(ctx context.Context, pool *pgxpool.Pool, r *http.Request) context.Context {
	token := extractBearerToken(r)
	if token == "" {
		return ctx
	}
	caller, err := resolveUserToken(ctx, pool, token)
	if err != nil {
		slog.Error("resolving token for mcp", "error", err)
		return ctx
	}
	if caller == nil {
		return ctx
	}
	return context.WithValue(ctx, callerKey, caller)
}

// CORS wraps a handler with permissive CORS headers appropriate for OAuth 2.1
// Dynamic Client Registration, token, and metadata endpoints. MCP's auth spec
// says authorization servers SHOULD support CORS so browser-based clients can
// reach them; Claude Code's SDK does a preflight on the token endpoint and
// aborts with a plaintext-405 parse error if the server doesn't handle it.
//
// Origin is reflected (not "*") so credentialed requests from MCP clients that
// opt into credentials still work; we don't actually read cookies on these
// endpoints, but reflecting is strictly more compatible.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MCPAuthGate enforces Bearer auth on the MCP endpoint and, on failure,
// returns 401 with a WWW-Authenticate header pointing at the tenant's RFC
// 9728 Protected Resource Metadata. Claude Code's MCP SDK (and the draft
// MCP auth spec) uses that header to discover the authorization server.
//
// Responses:
//   - No token → 401 with resource_metadata pointer
//   - Invalid/expired token → 401 invalid_token
//   - Valid token, tenant mismatch with /{slug}/mcp path → 401 invalid_token
//   - Valid token, tenant matches → fall through
//
// baseURL is the server's public origin (e.g. "https://kit.example.com")
// so we can build the resource_metadata absolute URL.
func MCPAuthGate(pool *pgxpool.Pool, baseURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathTenant := TenantFromContext(r.Context())
		if pathTenant == nil {
			http.NotFound(w, r)
			return
		}
		resourceMeta := baseURL + "/.well-known/oauth-protected-resource/" + pathTenant.Slug + "/mcp"

		token := extractBearerToken(r)
		if token == "" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+resourceMeta+`"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		caller, err := resolveUserToken(r.Context(), pool, token)
		if err != nil {
			slog.Error("resolving token for mcp", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if caller == nil {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="`+resourceMeta+`"`)
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}
		if caller.TenantID != pathTenant.ID {
			slog.Warn("mcp bearer token tenant mismatch", "token_tenant", caller.TenantID, "path_tenant", pathTenant.ID)
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="`+resourceMeta+`"`)
			http.Error(w, "token issued for a different workspace", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}

// AssertBearerMatchesPathTenant is a narrower legacy check: it only
// rejects a cross-tenant token, letting anonymous requests fall through.
// Prefer MCPAuthGate for the MCP endpoint; this is kept for tests and for
// use on routes where anonymous is allowed.
func AssertBearerMatchesPathTenant(pool *pgxpool.Pool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathTenant := TenantFromContext(r.Context())
		token := extractBearerToken(r)
		if pathTenant == nil || token == "" {
			next.ServeHTTP(w, r)
			return
		}
		caller, err := resolveUserToken(r.Context(), pool, token)
		if err != nil {
			slog.Error("resolving token for mcp tenant check", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if caller != nil && caller.TenantID != pathTenant.ID {
			slog.Warn("mcp bearer token tenant mismatch", "token_tenant", caller.TenantID, "path_tenant", pathTenant.ID)
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "token issued for a different workspace", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

// resolveToken turns a raw bearer secret or cookie payload into a Caller.
// Returns (nil, nil) for anything that should read as "no session": an
// unknown, expired or revoked token, a deleted user or tenant, a revoked
// actor. The api_tokens row is returned alongside so the session
// middleware can renew a device's expiry; bearer callers ignore it.
func resolveToken(ctx context.Context, pool *pgxpool.Pool, token string) (*services.Caller, *models.APIToken, error) {
	hash := opaquetoken.Hash(token)
	apiToken, err := models.LookupAPIToken(ctx, pool, hash)
	if err != nil || apiToken == nil {
		return nil, nil, err
	}
	tenant, err := models.GetTenantByID(ctx, pool, apiToken.TenantID)
	if err != nil || tenant == nil {
		return nil, nil, err
	}
	if err := models.TouchAPIToken(ctx, pool, apiToken.TenantID, hash); err != nil {
		slog.Warn("touching api token", "error", err)
	}
	if apiToken.ActorID != nil {
		caller, err := resolveActorCaller(ctx, pool, tenant, *apiToken.ActorID)
		return caller, apiToken, err
	}
	if apiToken.UserID == nil {
		return nil, nil, nil
	}
	user, err := models.GetUserByID(ctx, pool, apiToken.TenantID, *apiToken.UserID)
	if err != nil || user == nil {
		return nil, nil, err
	}
	cr, _ := services.NewRoleService(pool).ResolveCallerRoles(ctx, tenant, user.ID)
	return services.NewUserCaller(tenant, user, cr), apiToken, nil
}

// resolveActorCaller builds the caller for a token held by an actor. A
// revoked actor reads as no session even if its token row somehow
// survived, so revocation never depends on the token sweep.
func resolveActorCaller(ctx context.Context, pool *pgxpool.Pool, tenant *models.Tenant, actorID uuid.UUID) (*services.Caller, error) {
	actor, err := models.GetActor(ctx, pool, tenant.ID, actorID)
	if err != nil || actor == nil || actor.RevokedAt != nil {
		return nil, err
	}
	if err := models.TouchActor(ctx, pool, tenant.ID, actor.ID); err != nil {
		slog.Warn("touching actor", "error", err)
	}
	switch actor.Kind {
	case models.ActorKindDevice:
		return services.NewDeviceCaller(tenant, actor), nil
	default:
		slog.Warn("api token held by actor of unsupported kind", "actor_id", actor.ID, "kind", actor.Kind)
		return nil, nil //nolint:nilnil // reads as "no session", the same as a revoked actor
	}
}

// resolveUserToken is resolveToken for the bearer surfaces (MCP and the
// legacy /api/v1 bearer path), which are for people only: a device never
// holds a bearer secret, and an MCP harness acting as a device would get
// a tool catalog no capability list can describe. A non-user token reads
// as invalid rather than as a caller with no rights.
func resolveUserToken(ctx context.Context, pool *pgxpool.Pool, token string) (*services.Caller, error) {
	caller, _, err := resolveToken(ctx, pool, token)
	if err != nil || caller == nil {
		return nil, err
	}
	if !caller.IsUser() {
		slog.Warn("non-user token presented as bearer", "kind", caller.Kind, "actor_id", caller.ActorID)
		return nil, nil //nolint:nilnil // reads as an invalid token, the same as an unknown one
	}
	return caller, nil
}
