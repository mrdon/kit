package github

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service exposes the per-tenant install lookup other apps consume.
// Stays small in v1 — installation-token minting + REST API calls
// land in a later commit when the netlify app actually starts doing
// merges and compare diffs.
type Service struct {
	pool *pgxpool.Pool

	// Kit GitHub App credentials, set via Configure at boot. An app
	// with empty credentials is "not configured" and the netlify
	// settings page disables the install button.
	appSlug    string
	appID      int64
	privateKey []byte
	baseURL    string
}

// NewService builds a Service over the given pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// HasAppConfig reports whether the Kit GitHub App credentials were
// configured at boot.
func (s *Service) HasAppConfig() bool {
	return s != nil && s.appSlug != "" && s.appID != 0 && len(s.privateKey) > 0
}

// AppSlug returns the public slug of the Kit GitHub App (used to
// build install URLs from other apps' UIs).
func (s *Service) AppSlug() string {
	if s == nil {
		return ""
	}
	return s.appSlug
}

// GetInstallation is a thin pass-through to the models layer so
// other apps don't need to import the model package directly.
func (s *Service) GetInstallation(ctx context.Context, tenantID uuid.UUID) (*Installation, error) {
	return GetInstallation(ctx, s.pool, tenantID)
}
