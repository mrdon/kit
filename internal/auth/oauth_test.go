package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// TestHandleTokenRejectsCrossTenantCode: a code issued for tenant A must
// fail with invalid_grant when redeemed against /{B}/oauth/token.
func TestHandleTokenRejectsCrossTenantCode(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenantA, tenantB := twoTenants(t, ctx, pool)
	userA := makeUser(t, ctx, pool, tenantA.ID, "U_A")

	// Issue an oauth code under tenant A.
	code := "code-" + uuid.NewString()
	if err := models.CreateOAuthCode(ctx, pool, code, "client-A", tenantA.ID, userA.ID, "https://client/cb", "", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("creating code: %v", err)
	}

	// Build the server + a request whose path tenant is B, carrying tenant A's code.
	server := NewOAuthServer(pool, "https://kit.example.com", "slack-id", "slack-secret", "test-secret-test-secret-test", nil)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)

	req := httptest.NewRequest(http.MethodPost, "/"+tenantB.Slug+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), tenantCtxKey, tenantB))

	rec := httptest.NewRecorder()
	server.HandleToken(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_grant") {
		t.Fatalf("body = %q, want invalid_grant", rec.Body.String())
	}
}

// TestHandleTokenAcceptsSameTenantCode: sanity check that the happy path
// still works — code issued under tenant A redeemed at /{A}/oauth/token.
func TestHandleTokenAcceptsSameTenantCode(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenantA := oneTenant(t, ctx, pool)
	userA := makeUser(t, ctx, pool, tenantA.ID, "U_A2")

	code := "code-" + uuid.NewString()
	if err := models.CreateOAuthCode(ctx, pool, code, "client-A", tenantA.ID, userA.ID, "https://client/cb", "", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("creating code: %v", err)
	}

	server := NewOAuthServer(pool, "https://kit.example.com", "slack-id", "slack-secret", "test-secret-test-secret-test", nil)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)

	req := httptest.NewRequest(http.MethodPost, "/"+tenantA.Slug+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), tenantCtxKey, tenantA))

	rec := httptest.NewRecorder()
	server.HandleToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "access_token") {
		t.Fatalf("body missing access_token: %q", rec.Body.String())
	}
}

// TestHandleTokenRejectsCrossTenantClient: client_id registered under tenant A
// cannot be used at /{B}/oauth/token.
func TestHandleTokenRejectsCrossTenantClient(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenantA, tenantB := twoTenants(t, ctx, pool)
	userB := makeUser(t, ctx, pool, tenantB.ID, "U_B")

	// Client registered under tenant A only.
	if _, err := models.CreateOAuthClient(ctx, pool, tenantA.ID, "shared-client", "secret", []string{"https://client/cb"}, "Client"); err != nil {
		t.Fatalf("creating client in A: %v", err)
	}

	// Code issued under tenant B (same path tenant as the request) — so the
	// tenant check passes but the client_id lookup (scoped to B) should fail.
	code := "code-" + uuid.NewString()
	if err := models.CreateOAuthCode(ctx, pool, code, "shared-client", tenantB.ID, userB.ID, "https://client/cb", "", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("creating code: %v", err)
	}

	server := NewOAuthServer(pool, "https://kit.example.com", "slack-id", "slack-secret", "test-secret-test-secret-test", nil)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", "shared-client")

	req := httptest.NewRequest(http.MethodPost, "/"+tenantB.Slug+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), tenantCtxKey, tenantB))

	rec := httptest.NewRecorder()
	server.HandleToken(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_client") {
		t.Fatalf("body = %q, want invalid_client", rec.Body.String())
	}
}

// TestHandleMetadataIssuerMatchesPrefix: RFC 8414 requires the issuer to
// align with the well-known URL path. We serve at /{slug}/.well-known/... ,
// so issuer must be baseURL + "/" + slug.
func TestHandleMetadataIssuerMatchesPrefix(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	tenant := oneTenant(t, ctx, pool)
	server := NewOAuthServer(pool, "https://kit.example.com", "slack-id", "slack-secret", "test-secret-test-secret-test", nil)

	req := httptest.NewRequest(http.MethodGet, "/"+tenant.Slug+"/.well-known/oauth-authorization-server", nil)
	req = req.WithContext(context.WithValue(req.Context(), tenantCtxKey, tenant))

	rec := httptest.NewRecorder()
	server.HandleMetadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	wantIssuer := "https://kit.example.com/" + tenant.Slug
	if !strings.Contains(rec.Body.String(), `"issuer":"`+wantIssuer+`"`) {
		t.Fatalf("body missing issuer %q; got %s", wantIssuer, rec.Body.String())
	}
	wantAuthorize := wantIssuer + "/oauth/authorize"
	if !strings.Contains(rec.Body.String(), wantAuthorize) {
		t.Fatalf("body missing authorize endpoint %q; got %s", wantAuthorize, rec.Body.String())
	}
}

// --- helpers ---

func oneTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *models.Tenant {
	t.Helper()
	teamID := "T_oauth_" + uuid.NewString()
	slug := models.SanitizeSlug("oauth-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "Test Tenant", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})
	return tenant
}

func twoTenants(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (*models.Tenant, *models.Tenant) {
	t.Helper()
	return oneTenant(t, ctx, pool), oneTenant(t, ctx, pool)
}

func makeUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackID string) *models.User {
	t.Helper()
	user, err := models.GetOrCreateUser(ctx, pool, tenantID, slackID+"_"+uuid.NewString(), "Test User", false)
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	return user
}
