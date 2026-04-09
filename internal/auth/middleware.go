package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

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

		caller, err := resolveToken(r.Context(), pool, token)
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
	caller, err := resolveToken(ctx, pool, token)
	if err != nil {
		slog.Error("resolving token for mcp", "error", err)
		return ctx
	}
	if caller == nil {
		return ctx
	}
	return context.WithValue(ctx, callerKey, caller)
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func resolveToken(ctx context.Context, pool *pgxpool.Pool, token string) (*services.Caller, error) {
	hash := models.HashToken(token)
	apiToken, err := models.LookupAPIToken(ctx, pool, hash)
	if err != nil {
		return nil, err
	}
	if apiToken == nil {
		return nil, nil //nolint:nilnil // not found
	}

	user, err := models.GetUserByID(ctx, pool, apiToken.TenantID, apiToken.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil //nolint:nilnil // user deleted
	}

	tenant, err := models.GetTenantByID(ctx, pool, apiToken.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, nil //nolint:nilnil // tenant deleted
	}

	roles, _ := models.GetUserRoleNames(ctx, pool, apiToken.TenantID, apiToken.UserID, tenant.DefaultRoleID)

	return &services.Caller{
		TenantID: apiToken.TenantID,
		UserID:   apiToken.UserID,
		Identity: user.SlackUserID,
		Roles:    roles,
		IsAdmin:  user.IsAdmin,
		Timezone: services.ResolveTimezone(user.Timezone, tenant.Timezone),
	}, nil
}
