package netlify

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// ChangeRequest is the input to RequestChange. The prompt is the
// user's ask; SlackChannel + SlackThreadTS bind the run to the
// Slack thread for post-back. Empty Slack fields are valid (MCP
// callers, scripted runs) — RequestChange skips the watcher in
// that case and the caller is responsible for polling.
type ChangeRequest struct {
	Prompt        string
	SlackChannel  string
	SlackThreadTS string
}

// ChangeRunResult is the return shape. Mirrors the small subset of
// AgentRunner fields callers need to surface back to the user from
// Slack / MCP.
type ChangeRunResult struct {
	RunID            string // Netlify's run id (string), useful for dashboard lookups
	State            string
	BaseBranch       string
	PreviewURL       string
	ProductionBranch string
	WatcherStarted   bool   // true when an in-process watcher is polling + will post-back
	ChainedFromRunID string // Netlify run id this one forked off; empty for first run in thread
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
	// Branch chaining: if this Slack thread already has a completed
	// agent run on it, fork the new run off that run's result_branch
	// so subsequent edits accumulate ("blue → lighter blue → no, back
	// to blue") instead of resetting to production every turn.
	// Production-branch fallback applies when (a) it's the first run
	// in this thread or (b) the prior run hasn't produced a
	// result_branch yet (still in flight / failed).
	baseBranch := cfg.ProductionBranch
	chainedFrom := ""
	if in.SlackChannel != "" && in.SlackThreadTS != "" {
		if priorRun, perr := resolvePriorRun(ctx, s.pool, tenantID,
			in.SlackChannel, in.SlackThreadTS); perr == nil &&
			priorRun != nil && priorRun.ResultBranch != "" {
			baseBranch = priorRun.ResultBranch
			chainedFrom = priorRun.NetlifyRunID
		}
	}

	runner, err := createAgentRunner(ctx, accessToken, CreateAgentRunnerInput{
		SiteID: cfg.NetlifySiteID,
		Prompt: in.Prompt,
		Branch: baseBranch,
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

	result := &ChangeRunResult{
		RunID:            runner.ID,
		State:            runner.State,
		BaseBranch:       runner.Branch,
		PreviewURL:       runner.LatestSessionDeployURL,
		ProductionBranch: cfg.ProductionBranch,
		ChainedFromRunID: chainedFrom,
	}

	// If we have Slack thread coordinates, group the run into a
	// change-thread row, persist the agent_run, and spawn a watcher
	// that will post-back once Netlify reports done_at. Skipped for
	// MCP / scripted callers that didn't provide thread info.
	if in.SlackChannel != "" && in.SlackThreadTS != "" {
		ct, cerr := EnsureChangeThread(ctx, s.pool, tenantID,
			in.SlackChannel, in.SlackThreadTS, cfg.ProductionBranch)
		if cerr != nil {
			return nil, fmt.Errorf("ensure change thread: %w", cerr)
		}
		run, rerr := CreateAgentRun(ctx, s.pool, tenantID, ct.ID,
			runner.ID, in.Prompt, baseBranch,
			runner.LatestSessionDeployURL, runner.State)
		if rerr != nil {
			return nil, fmt.Errorf("create agent_run: %w", rerr)
		}
		botToken, terr := s.decryptTenantBotToken(ctx, tenantID)
		if terr != nil {
			// Surface the error but the Netlify run is already
			// happening — return the URL anyway and skip the watcher.
			return result, fmt.Errorf("bot token unavailable; watcher disabled: %w", terr)
		}
		s.startWatcher(watcherInput{
			tenantID:      tenantID,
			runID:         run.ID,
			netlifyRunID:  runner.ID,
			netlifyToken:  accessToken,
			slackBotToken: botToken,
			slackChannel:  in.SlackChannel,
			slackThreadTS: in.SlackThreadTS,
		})
		result.WatcherStarted = true
	}

	return result, nil
}

// resolvePriorRun returns the most recent agent_run row for a given
// Slack thread, or nil if there is none. Used by RequestChange to
// pick the right base branch for branch chaining: if the prior run
// completed and has a result_branch, the new run forks off that
// instead of production.
func resolvePriorRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	slackChannel, slackThreadTS string,
) (*AgentRun, error) {
	const q = `
        SELECT ct.latest_agent_run_id
        FROM app_netlify_change_threads ct
        WHERE ct.tenant_id = $1
          AND ct.slack_channel = $2
          AND ct.slack_thread_ts = $3`
	var runID *uuid.UUID
	err := pool.QueryRow(ctx, q, tenantID, slackChannel, slackThreadTS).Scan(&runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil
		}
		return nil, fmt.Errorf("looking up prior run: %w", err)
	}
	if runID == nil {
		return nil, nil //nolint:nilnil
	}
	return GetAgentRun(ctx, pool, *runID)
}
