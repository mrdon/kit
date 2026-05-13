package netlify

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/github"
	"github.com/mrdon/kit/internal/crypto"
)

// ErrNetlifyNotConnected is returned by RequestChange when the tenant
// hasn't connected Netlify yet. Surfaced to the agent so it can tell
// the user what to do, rather than the LLM hallucinating an
// explanation.
var ErrNetlifyNotConnected = errors.New("netlify not connected")

// ErrGitHubNotConnected is the analogue for the GitHub side.
var ErrGitHubNotConnected = errors.New("github not connected")

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

// ChangeRequest is the v1 input to RequestChange. Only the prompt is
// user-controlled; the site, base branch, and agent come from
// app_netlify_config.
type ChangeRequest struct {
	Prompt string
}

// ChangeRunResult is the v1 return shape. Mirrors the small subset
// of AgentRunner fields callers need to surface back to the user
// from Slack / MCP.
type ChangeRunResult struct {
	RunID            string
	State            string
	BaseBranch       string
	PreviewURL       string
	ProductionBranch string
}

// RequestChange starts a Netlify Agent Run for the tenant. v1
// behaviour: single-shot run forked off the production branch
// (no chaining), default agent, no clarifier. Returns immediately
// with the new run's id + preview URL — the URL 404s until the
// build completes (~60s), but is correct as soon as Netlify
// accepts the run.
func (s *Service) RequestChange(
	ctx context.Context,
	tenantID uuid.UUID,
	in ChangeRequest,
) (*ChangeRunResult, error) {
	cfg, err := GetConfig(ctx, s.pool, tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading netlify config: %w", err)
	}
	if cfg == nil || !cfg.ConnectedNetlify() {
		return nil, ErrNetlifyNotConnected
	}
	if s.github == nil {
		return nil, ErrGitHubNotConnected
	}
	inst, err := s.github.GetInstallation(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading github install: %w", err)
	}
	if inst == nil {
		return nil, ErrGitHubNotConnected
	}
	accessToken, err := s.enc.Decrypt(cfg.NetlifyAccessTokenCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypting netlify token: %w", err)
	}
	runner, err := createAgentRunner(ctx, accessToken, CreateAgentRunnerInput{
		SiteID: cfg.NetlifySiteID,
		Prompt: in.Prompt,
		Branch: cfg.ProductionBranch,
		Agent:  cfg.DefaultAgent,
	})
	if err != nil {
		return nil, err
	}
	// Netlify sometimes populates latest_session_deploy_url
	// asynchronously: the POST returns before the deploy id has been
	// minted. One follow-up GET gives the URL a chance to land
	// without us having to poll repeatedly. Best-effort — if the
	// fetch fails, fall through with whatever we had.
	if runner.LatestSessionDeployURL == "" {
		if refreshed, ferr := getAgentRunner(ctx, accessToken, runner.ID); ferr == nil {
			runner = refreshed
		}
	}
	return &ChangeRunResult{
		RunID:            runner.ID,
		State:            runner.State,
		BaseBranch:       runner.Branch,
		PreviewURL:       runner.LatestSessionDeployURL,
		ProductionBranch: cfg.ProductionBranch,
	}, nil
}
