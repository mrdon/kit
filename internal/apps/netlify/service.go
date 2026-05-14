package netlify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
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

// ErrNothingToPublish is returned by PublishChange when no active change
// thread exists for the Slack thread (no prior agent run).
var ErrNothingToPublish = errors.New("no agent run to publish in this thread")

// ErrPublishPending is returned when the latest run hasn't completed
// yet — committing a still-running run produces unpredictable
// results. Caller should ask the user to wait.
var ErrPublishPending = errors.New("latest agent run is still in progress")

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
	llm    *anthropic.Client // used by the watcher for diff summaries

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
	// Chaining: if this Slack thread already has a prior turn, add a
	// new session to that runner via POST /agent_runners/<rid>/sessions
	// rather than starting a fresh runner. This is the documented
	// "several agent runs per task" path — the agent sees the
	// cumulative state of the runner regardless of build state.
	//
	// If no prior turn (first message in thread): POST /agent_runners
	// creates a new runner.
	var (
		runID         string // what we'll store as netlify_run_id
		runnerID      string // the runner this turn belongs to
		runState      string
		runPreviewURL string
	)
	chainedFrom := ""

	if in.SlackChannel != "" && in.SlackThreadTS != "" {
		if priorRun, perr := resolvePriorRun(ctx, s.pool, tenantID,
			in.SlackChannel, in.SlackThreadTS); perr == nil &&
			priorRun != nil && priorRun.NetlifyRunnerID != "" {
			chainedFrom = priorRun.NetlifyRunnerID
		}
	}

	if chainedFrom != "" {
		// Follow-up turn: add a session to the existing runner.
		session, serr := createAgentRunnerSession(ctx, accessToken,
			chainedFrom, in.Prompt, cfg.DefaultAgent, "")
		if serr != nil {
			return nil, serr
		}
		runID = session.ID
		runnerID = chainedFrom
		runState = session.State
		runPreviewURL = session.DeployURL
		slog.Info("netlify session created",
			"session_id", runID, "runner_id", runnerID,
			"state", runState, "deploy_url", runPreviewURL)
	} else {
		// First turn in this thread: create a new runner. Branch
		// defaults to production; Netlify forks the agent's working
		// branch off it internally.
		runner, rerr := createAgentRunner(ctx, accessToken, CreateAgentRunnerInput{
			SiteID: cfg.NetlifySiteID,
			Prompt: in.Prompt,
			Branch: cfg.ProductionBranch,
			Agent:  cfg.DefaultAgent,
		})
		if rerr != nil {
			return nil, rerr
		}
		// Netlify sometimes populates latest_session_deploy_url
		// asynchronously: the POST returns before the deploy id has
		// been minted. One follow-up GET gives the URL a chance to
		// land. Best-effort.
		if runner.LatestSessionDeployURL == "" {
			if refreshed, ferr := getAgentRunner(ctx, accessToken, runner.ID); ferr == nil {
				runner = refreshed
			}
		}
		runID = runner.ID
		runnerID = runner.ID // same on first turn
		runState = runner.State
		runPreviewURL = runner.LatestSessionDeployURL
		slog.Info("netlify runner created",
			"runner_id", runID, "state", runState,
			"deploy_url", runPreviewURL, "branch", runner.Branch)
	}

	result := &ChangeRunResult{
		RunID:            runID,
		State:            runState,
		BaseBranch:       cfg.ProductionBranch,
		PreviewURL:       runPreviewURL,
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
			runID, runnerID, in.Prompt, cfg.ProductionBranch,
			runPreviewURL, runState)
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
			tenantID:        tenantID,
			runID:           run.ID,
			netlifyRunID:    runID,
			netlifyRunnerID: runnerID,
			netlifyToken:    accessToken,
			slackBotToken:   botToken,
			slackChannel:    in.SlackChannel,
			slackThreadTS:   in.SlackThreadTS,
		})
		result.WatcherStarted = true
	}

	return result, nil
}

// ChangeStatus is the live status of the latest run in a Slack
// thread. Returned by CheckChangeStatus when the user asks "what's
// happening now?". The status fields come straight from Netlify on
// each call — our DB is just the breadcrumb to find the runner.
type ChangeStatus struct {
	State        string                   // running | done | succeeded | failed | cancelled
	Done         bool                     // true once Netlify has marked the run terminal
	CurrentTask  string                   // what the agent is doing right now
	StartedAt    time.Time                // when our row was created
	ElapsedSecs  int                      // wall-clock elapsed since the run was started
	Prompt       string                   // user's original ask
	PreviewURL   string                   // populated as soon as a deploy id is minted
	ResultBranch string                   // populated after the agent commits
	Summary      string                   // Netlify's narrative if available
	Steps        []AgentRunnerSessionStep // session step trace
}

// CheckChangeStatus reports on the latest agent run in a Slack
// thread. Used by the status tool so the user can ask Kit "any
// progress?" without leaving Slack.
func (s *Service) CheckChangeStatus(
	ctx context.Context,
	tenantID uuid.UUID,
	slackChannel, slackThreadTS string,
) (*ChangeStatus, error) {
	if slackChannel == "" || slackThreadTS == "" {
		return nil, errors.New("status check requires slack thread coordinates")
	}
	cfg, err := GetConfig(ctx, s.pool, tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading netlify config: %w", err)
	}
	if cfg == nil || !cfg.ConnectedNetlify() {
		return nil, ErrNetlifyNotConnected
	}
	priorRun, err := resolvePriorRun(ctx, s.pool, tenantID, slackChannel, slackThreadTS)
	if err != nil {
		return nil, fmt.Errorf("loading prior run: %w", err)
	}
	if priorRun == nil {
		return nil, ErrNothingToPublish
	}
	accessToken, err := s.enc.Decrypt(cfg.NetlifyAccessTokenCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypting netlify token: %w", err)
	}

	out := &ChangeStatus{
		Prompt:      priorRun.Prompt,
		StartedAt:   priorRun.CreatedAt,
		Summary:     priorRun.Summary,
		ElapsedSecs: int(time.Since(priorRun.CreatedAt).Seconds()),
	}

	// Same branching as the watcher: poll the session endpoint for
	// follow-up turns, the runner endpoint for first turns.
	if priorRun.NetlifyRunID != priorRun.NetlifyRunnerID {
		sess, ferr := getAgentRunnerSession(ctx, accessToken,
			priorRun.NetlifyRunnerID, priorRun.NetlifyRunID)
		if ferr != nil {
			return nil, fmt.Errorf("fetching session: %w", ferr)
		}
		out.State = sess.State
		out.Done = sess.DoneAt != ""
		out.CurrentTask = sess.Title
		out.PreviewURL = sess.DeployURL
		out.Steps = sess.Steps
		if sess.Result != "" {
			out.Summary = sess.Result
		}
	} else {
		runner, ferr := getAgentRunner(ctx, accessToken, priorRun.NetlifyRunID)
		if ferr != nil {
			return nil, fmt.Errorf("fetching runner: %w", ferr)
		}
		out.State = runner.State
		out.Done = runner.DoneAt != ""
		out.CurrentTask = runner.CurrentTask
		out.PreviewURL = runner.LatestSessionDeployURL
		out.ResultBranch = runner.ResultBranch
		if runner.Result != "" {
			out.Summary = runner.Result
		}
	}
	return out, nil
}

// PublishResult is what PublishChange returns when a commit succeeds.
type PublishResult struct {
	TargetBranch string // production branch we shipped to
	RunnerID     string // Netlify runner id that was committed
	Summary      string // latest run's diff summary, if we have it
	PRURL        string // GitHub PR page (created by Netlify, merged by Kit)
	PRTitle      string // PR's auto-generated title
	Additions    int
	Deletions    int
	ChangedFiles int
	MergeSHA     string // commit SHA on the production branch after merge
}

// PublishChange takes the latest agent run for the given Slack thread
// and gets its changes onto the tenant's production branch in two
// steps:
//
//  1. POST /agent_runners/<id>/commit?target_branch=<pr_branch>:
//     Netlify pushes the agent's working state to its designated PR
//     branch on GitHub (the runner can only commit to that branch;
//     target_branch is not arbitrary).
//  2. POST /repos/<owner>/<repo>/merges via Kit's GitHub App: merges
//     the PR branch into the tenant's production branch. Netlify's
//     normal CI auto-deploys production from there.
//
// Refuses if (a) no change_thread exists for this Slack thread,
// (b) the latest run hasn't completed yet, (c) Netlify isn't
// connected, or (d) Kit's GitHub App isn't installed (needed for
// step 2). Marks the change_thread as 'published' on success.
func (s *Service) PublishChange(
	ctx context.Context,
	tenantID uuid.UUID,
	slackChannel, slackThreadTS string,
) (*PublishResult, error) {
	if slackChannel == "" || slackThreadTS == "" {
		return nil, errors.New("publish requires slack thread coordinates")
	}
	cfg, err := GetConfig(ctx, s.pool, tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading netlify config: %w", err)
	}
	if cfg == nil || !cfg.ConnectedNetlify() {
		return nil, ErrNetlifyNotConnected
	}
	if cfg.NetlifyRepoOwner == "" || cfg.NetlifyRepoName == "" {
		return nil, errors.New("netlify site doesn't have a parsed GitHub repo — re-pick the site on the integrations page")
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
	priorRun, err := resolvePriorRun(ctx, s.pool, tenantID, slackChannel, slackThreadTS)
	if err != nil {
		return nil, fmt.Errorf("loading prior run: %w", err)
	}
	if priorRun == nil {
		return nil, ErrNothingToPublish
	}
	if priorRun.DoneAt == nil {
		return nil, ErrPublishPending
	}
	accessToken, err := s.enc.Decrypt(cfg.NetlifyAccessTokenCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypting netlify token: %w", err)
	}

	// Step 1: open a PR via Netlify. This pushes the agent's
	// working state to a GitHub branch and populates pr_branch on
	// the runner — that field stays empty until we (or someone in
	// the Netlify UI) explicitly request a PR.
	prResp, err := pullRequestAgentRunner(ctx, accessToken, priorRun.NetlifyRunnerID)
	if err != nil {
		return nil, fmt.Errorf("creating netlify PR: %w", err)
	}
	prBranch := prResp.PRBranch
	if prBranch == "" {
		// /pull_request response may not include the branch field on
		// some Netlify deploys — fall back to a fresh GET.
		runner, gerr := getAgentRunner(ctx, accessToken, priorRun.NetlifyRunnerID)
		if gerr != nil {
			return nil, fmt.Errorf("loading runner state: %w", gerr)
		}
		prBranch = runner.PRBranch
	}
	if prBranch == "" {
		return nil, errors.New("netlify didn't assign a PR branch even after pull_request — try again in a moment")
	}

	// Step 2: commit the runner's working state to its PR branch.
	// (Netlify's commit endpoint only accepts target_branch=<pr_branch>;
	// it can't be redirected to main directly.)
	if err := commitAgentRunner(ctx, accessToken,
		priorRun.NetlifyRunnerID, prBranch); err != nil {
		return nil, err
	}

	// Step 3: merge the PR branch into production via Kit's
	// GitHub App. Netlify's auto-deploy picks up the resulting
	// main-branch commit and rebuilds the live site.
	commitMsg := "Publish via Kit"
	if priorRun.Summary != "" {
		commitMsg = "Publish via Kit: " + priorRun.Summary
	}
	if err := s.github.MergeBranch(ctx, inst.InstallationID,
		cfg.NetlifyRepoOwner, cfg.NetlifyRepoName,
		cfg.ProductionBranch, prBranch, commitMsg); err != nil {
		return nil, fmt.Errorf("merging %s → %s: %w",
			prBranch, cfg.ProductionBranch, err)
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE app_netlify_change_threads SET state = 'published', updated_at = now()
         WHERE id = $1`, priorRun.ChangeThreadID); err != nil {
		slog.Warn("publish: marking change_thread published",
			"thread_id", priorRun.ChangeThreadID, "error", err)
	}

	result := &PublishResult{
		TargetBranch: cfg.ProductionBranch,
		RunnerID:     priorRun.NetlifyRunnerID,
		Summary:      priorRun.Summary,
	}

	// Best-effort PR enrichment for the confirmation message. Use
	// the pr_number from the runner state if we have it; merge is
	// already done so a fetch failure here is cosmetic.
	if prResp.PRNumber > 0 {
		if pr, perr := s.github.GetPullRequest(ctx, inst.InstallationID,
			cfg.NetlifyRepoOwner, cfg.NetlifyRepoName, prResp.PRNumber); perr == nil {
			result.PRURL = pr.URL
			result.PRTitle = pr.Title
			result.Additions = pr.Additions
			result.Deletions = pr.Deletions
			result.ChangedFiles = pr.ChangedFiles
			result.MergeSHA = pr.MergeCommitSHA
		} else {
			slog.Warn("publish: fetching PR for confirmation",
				"pr_number", prResp.PRNumber, "error", perr)
		}
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
