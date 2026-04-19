package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// TestAssertBearerMatchesPathTenantMismatchReturns401: a Bearer token issued
// for tenant A presented against /{B}/mcp must return 401, not fall through
// and leak MCP capabilities to a cross-tenant caller.
func TestAssertBearerMatchesPathTenantMismatchReturns401(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenantA, tenantB := twoTenants(t, ctx, pool)
	userA := makeUser(t, ctx, pool, tenantA.ID, "U_mid_A")
	token := issueAPIToken(t, ctx, pool, tenantA.ID, userA.ID)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/"+tenantB.Slug+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), tenantCtxKey, tenantB))

	rec := httptest.NewRecorder()
	AssertBearerMatchesPathTenant(pool, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatalf("next handler ran despite tenant mismatch")
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("expected WWW-Authenticate header on 401")
	}
}

// TestAssertBearerMatchesPathTenantMatchFallsThrough: a token issued for
// the path tenant must not be rejected.
func TestAssertBearerMatchesPathTenantMatchFallsThrough(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenant := oneTenant(t, ctx, pool)
	user := makeUser(t, ctx, pool, tenant.ID, "U_mid_match")
	token := issueAPIToken(t, ctx, pool, tenant.ID, user.ID)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/"+tenant.Slug+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), tenantCtxKey, tenant))

	rec := httptest.NewRecorder()
	AssertBearerMatchesPathTenant(pool, next).ServeHTTP(rec, req)

	if !called {
		t.Fatalf("next handler did not run despite matching tenant")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestAssertBearerMatchesPathTenantNoTokenFallsThrough: requests without
// any token must not be blocked by this middleware — mcp-go's ctx func +
// tool-level caller gating handle anonymous calls.
func TestAssertBearerMatchesPathTenantNoTokenFallsThrough(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenant := oneTenant(t, ctx, pool)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/"+tenant.Slug+"/mcp", nil)
	req = req.WithContext(context.WithValue(req.Context(), tenantCtxKey, tenant))

	rec := httptest.NewRecorder()
	AssertBearerMatchesPathTenant(pool, next).ServeHTTP(rec, req)

	if !called {
		t.Fatalf("expected anonymous request to fall through")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func issueAPIToken(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) string {
	t.Helper()
	raw, hash, err := models.GenerateToken()
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}
	if err := models.CreateAPIToken(ctx, pool, tenantID, userID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("creating api token: %v", err)
	}
	return raw
}
