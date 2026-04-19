package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// tenantCtxKey holds the path-resolved tenant (distinct from the Caller's
// tenant, which comes from the session). Most handlers won't care about
// the difference, but AssertTenantMatch does.
type tenantCtxKeyType struct{}

var tenantCtxKey = tenantCtxKeyType{}

// TenantFromContext returns the tenant resolved from the URL path, or nil
// if no TenantFromPath middleware was run.
func TenantFromContext(ctx context.Context) *models.Tenant {
	t, _ := ctx.Value(tenantCtxKey).(*models.Tenant)
	return t
}

// TenantFromPath reads the {slug} path value, validates it, looks up the
// tenant, and injects it into the request context. Invalid or unknown
// slugs get a plain 404 — callers should not rely on different error
// codes to distinguish "reserved" from "not found" (enumeration defense).
func TenantFromPath(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			slug := r.PathValue("slug")
			if !models.IsValidSlug(slug) {
				http.NotFound(w, r)
				return
			}
			tenant, err := models.GetTenantBySlug(r.Context(), pool, slug)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if tenant == nil {
				http.NotFound(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), tenantCtxKey, tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AssertTenantMatch wraps a handler so it runs only when the session
// caller's tenant matches the path-resolved tenant. Assumes both
// TenantFromPath and SessionSigner.Middleware have run earlier in the
// chain (if not, behaves conservatively: 403).
//
// API routes (paths containing "/api/") get a 403; HTML routes get a
// redirect to /{slug}/login so the user can re-authenticate into the
// correct workspace.
func AssertTenantMatch(signer *SessionSigner, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathTenant := TenantFromContext(r.Context())
		caller := CallerFromContext(r.Context())
		if pathTenant == nil || caller == nil || caller.TenantID != pathTenant.ID {
			if signer != nil {
				signer.Clear(w, "/")
				signer.Clear(w, "/"+r.PathValue("slug")+"/")
			}
			if strings.Contains(r.URL.Path, "/api/") {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			http.Redirect(w, r, "/"+r.PathValue("slug")+"/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
