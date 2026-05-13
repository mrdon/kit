package netlify

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/github"
	"github.com/mrdon/kit/internal/crypto"
)

// Service is the thin business-logic layer over the netlify config
// table. Stays small in v1 — most useful work shows up once agent
// runs and image uploads land.
//
// Cross-app dependency on the github Kit app is intentional: GitHub
// install is workspace-shared infrastructure, not Netlify-specific.
// The netlify app reads the install via github.Service.GetInstallation
// but never writes to it.
type Service struct {
	pool   *pgxpool.Pool
	enc    *crypto.Encryptor
	github *github.Service

	// Netlify OAuth client credentials, populated via Configure.
	// Empty strings leave the Netlify Connect button disabled.
	netlifyClientID     string
	netlifyClientSecret string
	baseURL             string
}

// NewService builds a Service over the given pool + encryptor.
func NewService(pool *pgxpool.Pool, enc *crypto.Encryptor) *Service {
	return &Service{pool: pool, enc: enc}
}

// GetConfig returns the per-tenant config, ensuring a row exists first
// so the settings page always has something to render.
func (s *Service) GetConfig(ctx context.Context, tenantID uuid.UUID) (*Config, error) {
	if err := ensureRow(ctx, s.pool, tenantID); err != nil {
		return nil, err
	}
	cfg, err := GetConfig(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		// Shouldn't happen after ensureRow, but be defensive.
		return nil, fmt.Errorf("no app_netlify_config row after ensure for tenant %s", tenantID)
	}
	return cfg, nil
}

// HasNetlifyCredentials reports whether the Netlify OAuth client
// credentials were configured at boot — drives whether the settings
// page enables the "Connect Netlify" button.
func (s *Service) HasNetlifyCredentials() bool {
	return s.netlifyClientID != "" && s.netlifyClientSecret != ""
}

// HasGitHubAppConfig reports whether the Kit GitHub App credentials
// were configured at boot — delegates to the github Kit app's
// service since that app owns the App credentials.
func (s *Service) HasGitHubAppConfig() bool {
	if s.github == nil {
		return false
	}
	return s.github.HasAppConfig()
}
