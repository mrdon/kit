package netlify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// watcherTimeout is the hard upper bound on a watcher goroutine.
// A Netlify Agent Run takes 30–120s typically; 5 minutes covers
// long-tail runs. Past this, we log + post a "taking longer than
// expected" message so the user isn't left hanging.
const watcherTimeout = 5 * time.Minute

// watcherPollInterval is the cadence we hit Netlify's
// GET /agent_runners/<id>. 5s strikes a balance: fast enough that
// the post-back lands within ~5s of completion, slow enough not to
// hammer the API.
const watcherPollInterval = 5 * time.Second

// watcherInput is the immutable bundle the watcher needs to do its
// job. Constructed by RequestChange just before goroutine spawn.
type watcherInput struct {
	tenantID      uuid.UUID
	runID         uuid.UUID // our DB id
	netlifyRunID  string
	netlifyToken  string // already decrypted
	slackBotToken string // already decrypted
	slackChannel  string
	slackThreadTS string
}

// startWatcher kicks off a background goroutine that polls Netlify
// until the run reports done_at, then posts a Slack message to the
// originating thread.
//
// Uses context.Background() + an explicit timeout so the goroutine
// outlives the tool-call's request context (which is cancelled as
// soon as the agent loop returns).
//
// In-process only — a Kit redeploy mid-run leaves the user without
// a post-back. The spec calls out this v1 limitation; a durable
// version is a cron job on this app polling state='running' rows.
func (s *Service) startWatcher(in watcherInput) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), watcherTimeout)
		defer cancel()
		s.runWatcher(ctx, in)
	}()
}

func (s *Service) runWatcher(ctx context.Context, in watcherInput) {
	ticker := time.NewTicker(watcherPollInterval)
	defer ticker.Stop()
	// First fetch immediately rather than waiting for the first tick.
	// Cuts ~5s off the happy-path latency when Netlify has already
	// populated the preview URL by the time we get here.
	if done := s.checkRun(ctx, in); done {
		return
	}
	for {
		select {
		case <-ctx.Done():
			s.postWatcherTimeout(in)
			return
		case <-ticker.C:
			if done := s.checkRun(ctx, in); done {
				return
			}
		}
	}
}

// checkRun does one GET /agent_runners/<id>, updates the DB row,
// and posts to Slack + returns true if the run is in a terminal
// state. Returns false if the run is still in flight (caller
// should keep polling).
func (s *Service) checkRun(ctx context.Context, in watcherInput) (done bool) {
	runner, err := getAgentRunner(ctx, in.netlifyToken, in.netlifyRunID)
	if err != nil {
		// Transient errors are common (rate limits, momentary
		// flakes); don't give up — let the next tick retry.
		slog.Warn("netlify watcher: get agent_runner",
			"netlify_run_id", in.netlifyRunID, "error", err)
		return false
	}
	var doneAt *time.Time
	if runner.DoneAt != "" {
		if t, perr := time.Parse(time.RFC3339, runner.DoneAt); perr == nil {
			doneAt = &t
		} else {
			// Unparseable but non-empty done_at still indicates
			// terminal — stamp it now so we don't loop forever.
			now := time.Now()
			doneAt = &now
		}
	}
	if uerr := UpdateAgentRunProgress(ctx, s.pool, in.runID,
		runner.State, runner.ResultBranch, runner.LatestSessionDeployURL, doneAt); uerr != nil {
		slog.Warn("netlify watcher: persisting progress",
			"run_id", in.runID, "error", uerr)
	}
	if doneAt == nil {
		return false
	}
	s.postWatcherResult(in, runner)
	return true
}

// postWatcherResult sends the "your build is ready" message back to
// the originating Slack thread. Uses the tenant's bot token to call
// chat.postMessage directly — does NOT go through the agent loop
// because the agent loop is long gone by now.
func (s *Service) postWatcherResult(in watcherInput, runner *AgentRunner) {
	postCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := kitslack.NewClient(in.slackBotToken)
	var b strings.Builder
	switch {
	case runner.State == "succeeded" || (runner.DoneAt != "" && runner.State == ""):
		b.WriteString(":white_check_mark: Build ready.\n")
		if runner.LatestSessionDeployURL != "" {
			fmt.Fprintf(&b, "Preview: %s\n", runner.LatestSessionDeployURL)
		}
		if runner.ResultBranch != "" {
			fmt.Fprintf(&b, "Branch: `%s`\n", runner.ResultBranch)
		}
		b.WriteString("\n_Diff summary coming in a follow-up; for now click through and check._")
	case runner.State == "failed":
		fmt.Fprintf(&b, ":x: The agent run failed.\n")
		if runner.CurrentTask != "" {
			fmt.Fprintf(&b, "Last task: %s\n", runner.CurrentTask)
		}
		fmt.Fprintf(&b, "Reply in this thread to try a different request.")
	case runner.State == "cancelled":
		b.WriteString(":no_entry_sign: The agent run was cancelled.")
	default:
		// Unknown terminal-ish state — be defensive, share what we have.
		fmt.Fprintf(&b, "Agent run finished (state: %s).", runner.State)
		if runner.LatestSessionDeployURL != "" {
			fmt.Fprintf(&b, "\nPreview: %s", runner.LatestSessionDeployURL)
		}
	}
	if err := client.PostMessage(postCtx, in.slackChannel, in.slackThreadTS, b.String()); err != nil {
		slog.Warn("netlify watcher: posting result to slack",
			"channel", in.slackChannel, "thread", in.slackThreadTS, "error", err)
	}
}

// postWatcherTimeout posts a single "still running" notice when
// the 5-minute watcher timeout fires. The Netlify run itself
// keeps going; the user just doesn't get an automatic post-back.
// Tells them where to look.
func (s *Service) postWatcherTimeout(in watcherInput) {
	postCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := kitslack.NewClient(in.slackBotToken)
	msg := ":hourglass: The Netlify run is taking longer than expected. " +
		"It may still finish — check the Netlify dashboard for run `" + in.netlifyRunID + "`."
	if err := client.PostMessage(postCtx, in.slackChannel, in.slackThreadTS, msg); err != nil {
		slog.Warn("netlify watcher: posting timeout to slack",
			"channel", in.slackChannel, "thread", in.slackThreadTS, "error", err)
	}
}

// decryptTenantBotToken loads the tenant's bot token from the
// tenants table and decrypts it. Used by the watcher to get the
// credential it needs for chat.postMessage.
func (s *Service) decryptTenantBotToken(ctx context.Context, tenantID uuid.UUID) (string, error) {
	tenant, err := models.GetTenantByID(ctx, s.pool, tenantID)
	if err != nil {
		return "", fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return "", errors.New("tenant not found")
	}
	token, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		return "", fmt.Errorf("decrypting bot token: %w", err)
	}
	return token, nil
}
