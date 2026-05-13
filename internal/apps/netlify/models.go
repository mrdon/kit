package netlify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is the per-tenant configuration row for the Netlify app.
// Token columns hold ciphertext (hex-encoded AES-256-GCM via
// internal/crypto, same shape as tenants.bot_token) and are decrypted
// in-memory only at the moment of an outbound call — never logged,
// never sent back through the agent surface.
//
// A row exists from the moment the tenant opens the settings page,
// even before Netlify is connected. ConnectedNetlify() reflects
// whether the Netlify side is wired up; the GitHub side lives in
// internal/apps/github and is read from there.
type Config struct {
	TenantID                  uuid.UUID
	NetlifyAccessTokenCipher  string
	NetlifyRefreshTokenCipher string
	NetlifyTokenExpiresAt     *time.Time
	NetlifySiteID             string
	NetlifySiteName           string
	NetlifyRepoOwner          string
	NetlifyRepoName           string
	ProductionBranch          string
	DefaultAgent              string
	MonthlyRunBudget          *int
	BlobsStoreName            string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// ConnectedNetlify reports whether the tenant has finished the Netlify
// OAuth flow and picked a site.
func (c *Config) ConnectedNetlify() bool {
	return c.NetlifyAccessTokenCipher != "" && c.NetlifySiteID != ""
}

// GetConfig fetches the row for the tenant, or nil if no row exists yet.
func GetConfig(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*Config, error) {
	const q = `
        SELECT tenant_id,
               netlify_access_token,
               netlify_refresh_token,
               netlify_token_expires_at,
               netlify_site_id,
               netlify_site_name,
               netlify_repo_owner,
               netlify_repo_name,
               production_branch,
               default_agent,
               monthly_run_budget,
               blobs_store_name,
               created_at,
               updated_at
        FROM app_netlify_config
        WHERE tenant_id = $1`
	var c Config
	var accessToken, refreshToken, siteID, siteName, repoOwner, repoName, blobsStore *string
	err := pool.QueryRow(ctx, q, tenantID).Scan(
		&c.TenantID,
		&accessToken,
		&refreshToken,
		&c.NetlifyTokenExpiresAt,
		&siteID,
		&siteName,
		&repoOwner,
		&repoName,
		&c.ProductionBranch,
		&c.DefaultAgent,
		&c.MonthlyRunBudget,
		&blobsStore,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // explicit "not found" sentinel
		}
		return nil, fmt.Errorf("querying app_netlify_config: %w", err)
	}
	if accessToken != nil {
		c.NetlifyAccessTokenCipher = *accessToken
	}
	if refreshToken != nil {
		c.NetlifyRefreshTokenCipher = *refreshToken
	}
	if siteID != nil {
		c.NetlifySiteID = *siteID
	}
	if siteName != nil {
		c.NetlifySiteName = *siteName
	}
	if repoOwner != nil {
		c.NetlifyRepoOwner = *repoOwner
	}
	if repoName != nil {
		c.NetlifyRepoName = *repoName
	}
	if blobsStore != nil {
		c.BlobsStoreName = *blobsStore
	}
	return &c, nil
}

// ensureRow inserts an empty config row for a tenant if none exists.
// Idempotent — safe to call on every settings-page visit.
func ensureRow(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) error {
	const q = `
        INSERT INTO app_netlify_config (tenant_id)
        VALUES ($1)
        ON CONFLICT (tenant_id) DO NOTHING`
	if _, err := pool.Exec(ctx, q, tenantID); err != nil {
		return fmt.Errorf("inserting app_netlify_config: %w", err)
	}
	return nil
}

// SaveNetlifyTokens writes the encrypted access + refresh token pair
// and the absolute expiry timestamp. Called from the OAuth callback.
// Leaves site_id NULL — the user picks the site on the next page.
func SaveNetlifyTokens(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	accessTokenCipher, refreshTokenCipher string,
	expiresAt time.Time,
) error {
	const q = `
        INSERT INTO app_netlify_config (
            tenant_id,
            netlify_access_token,
            netlify_refresh_token,
            netlify_token_expires_at,
            updated_at
        )
        VALUES ($1, $2, NULLIF($3, ''), $4, now())
        ON CONFLICT (tenant_id) DO UPDATE SET
            netlify_access_token      = EXCLUDED.netlify_access_token,
            netlify_refresh_token     = EXCLUDED.netlify_refresh_token,
            netlify_token_expires_at  = EXCLUDED.netlify_token_expires_at,
            updated_at                = now()
        WHERE app_netlify_config.tenant_id = $1`
	_, err := pool.Exec(ctx, q, tenantID, accessTokenCipher, refreshTokenCipher, expiresAt)
	if err != nil {
		return fmt.Errorf("saving netlify tokens: %w", err)
	}
	return nil
}

// SaveNetlifySite records the user's site pick along with the auto-
// detected production branch and the GitHub repo coordinates parsed
// from build_settings.repo_url. Repo coords are captured here so the
// GitHub install step can hint the user at the right repo.
func SaveNetlifySite(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	siteID, siteName, productionBranch, repoOwner, repoName string,
) error {
	const q = `
        UPDATE app_netlify_config SET
            netlify_site_id    = $2,
            netlify_site_name  = $3,
            production_branch  = COALESCE(NULLIF($4, ''), production_branch),
            netlify_repo_owner = NULLIF($5, ''),
            netlify_repo_name  = NULLIF($6, ''),
            updated_at         = now()
        WHERE tenant_id = $1`
	tag, err := pool.Exec(ctx, q, tenantID, siteID, siteName, productionBranch, repoOwner, repoName)
	if err != nil {
		return fmt.Errorf("saving netlify site: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no app_netlify_config row for tenant %s", tenantID)
	}
	return nil
}

// ClearNetlify drops the Netlify side of the connection (tokens, site).
// The GitHub install lives in a separate table and is unaffected.
func ClearNetlify(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) error {
	const q = `
        UPDATE app_netlify_config SET
            netlify_access_token     = NULL,
            netlify_refresh_token    = NULL,
            netlify_token_expires_at = NULL,
            netlify_site_id          = NULL,
            netlify_site_name        = NULL,
            netlify_repo_owner       = NULL,
            netlify_repo_name        = NULL,
            updated_at               = now()
        WHERE tenant_id = $1`
	if _, err := pool.Exec(ctx, q, tenantID); err != nil {
		return fmt.Errorf("clearing netlify connection: %w", err)
	}
	return nil
}
