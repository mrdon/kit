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
//
// If netlifyRunID == netlifyRunnerID this is a first-turn runner
// (poll GET /agent_runners/<id>). Otherwise it's a follow-up session
// (poll GET /agent_runners/<runner_id>/sessions/<session_id>).
type watcherInput struct {
	tenantID        uuid.UUID
	runID           uuid.UUID // our DB id
	netlifyRunID    string    // runner id OR session id
	netlifyRunnerID string    // always the runner id
	netlifyToken    string    // already decrypted
	slackBotToken   string    // already decrypted
	slackChannel    string
	slackThreadTS   string
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

// checkRun polls Netlify for the current state of this run/session,
// updates the DB row, and posts to Slack + returns true if the
// run is in a terminal state. Returns false if still in flight.
//
// Branches on whether this is a first-turn runner or a follow-up
// session — see watcherInput.
func (s *Service) checkRun(ctx context.Context, in watcherInput) (done bool) {
	var (
		state, doneAtStr, deployURL, resultBranch, resultDiff, resultNarrative string
	)
	postRunner := &AgentRunner{} // placeholder for postWatcherResult

	if in.netlifyRunID != in.netlifyRunnerID {
		sess, err := getAgentRunnerSession(ctx, in.netlifyToken,
			in.netlifyRunnerID, in.netlifyRunID)
		if err != nil {
			slog.Warn("netlify watcher: get session",
				"runner_id", in.netlifyRunnerID,
				"session_id", in.netlifyRunID, "error", err)
			return false
		}
		state = sess.State
		doneAtStr = sess.DoneAt
		deployURL = sess.DeployURL
		resultDiff = sess.ResultDiff
		resultNarrative = sess.Result
		postRunner.ID = sess.AgentRunnerID
		postRunner.State = sess.State
		postRunner.DoneAt = sess.DoneAt
		postRunner.LatestSessionDeployURL = sess.DeployURL
		postRunner.CurrentTask = sess.Title
	} else {
		runner, err := getAgentRunner(ctx, in.netlifyToken, in.netlifyRunID)
		if err != nil {
			slog.Warn("netlify watcher: get agent_runner",
				"netlify_run_id", in.netlifyRunID, "error", err)
			return false
		}
		state = runner.State
		doneAtStr = runner.DoneAt
		deployURL = runner.LatestSessionDeployURL
		resultBranch = runner.ResultBranch
		resultDiff = runner.ResultDiff
		resultNarrative = runner.Result
		postRunner = runner
	}

	var doneAt *time.Time
	if doneAtStr != "" {
		if t, perr := time.Parse(time.RFC3339, doneAtStr); perr == nil {
			doneAt = &t
		} else {
			now := time.Now()
			doneAt = &now
		}
	}
	if uerr := UpdateAgentRunProgress(ctx, s.pool, in.runID,
		state, resultBranch, deployURL, doneAt); uerr != nil {
		slog.Warn("netlify watcher: persisting progress",
			"run_id", in.runID, "error", uerr)
	}
	if doneAt == nil {
		return false
	}

	// Prefer Netlify's own narrative (the `result` field on the
	// runner/session — same text that ends up in the PR body). Fall
	// back to a Haiku summary of the diff *only* if both `result`
	// is empty AND the diff is populated. In practice Netlify keeps
	// the diff internal and returns empty — so `result` is the path
	// that actually works.
	summary := strings.TrimSpace(resultNarrative)
	if summary == "" && resultDiff != "" && s.llm != nil {
		summarizeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if out, serr := summarizeDiff(summarizeCtx, s.llm, resultDiff); serr != nil {
			slog.Warn("netlify watcher: summarizing diff",
				"run_id", in.runID, "error", serr)
		} else {
			summary = out
		}
		cancel()
	}
	if summary != "" {
		if perr := PersistAgentRunSummary(ctx, s.pool, in.runID, summary); perr != nil {
			slog.Warn("netlify watcher: persisting summary",
				"run_id", in.runID, "error", perr)
		}
	}

	s.postWatcherResult(in, postRunner, summary)
	return true
}

// postWatcherResult sends the "your build is ready" message back to
// the originating Slack thread. Uses the tenant's bot token to call
// chat.postMessage directly — does NOT go through the agent loop
// because the agent loop is long gone by now.
//
// summary is the Haiku-generated plain-language description of the
// diff. Empty string means we didn't summarize (no diff, no LLM
// wired, or summarize call failed) — the post-back falls back to
// a generic "click to check" hint.
func (s *Service) postWatcherResult(in watcherInput, runner *AgentRunner, summary string) {
	postCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := kitslack.NewClient(in.slackBotToken)
	var b strings.Builder
	switch {
	case isSuccessState(runner.State) || (runner.DoneAt != "" && runner.State == ""):
		b.WriteString(":white_check_mark: Build ready.\n")
		if summary != "" {
			fmt.Fprintf(&b, "_%s_\n", summary)
		}
		if runner.LatestSessionDeployURL != "" {
			fmt.Fprintf(&b, "Preview: %s\n", runner.LatestSessionDeployURL)
		}
		if runner.ResultBranch != "" {
			fmt.Fprintf(&b, "Branch: `%s`\n", runner.ResultBranch)
		}
		if summary == "" {
			b.WriteString("\n_Click through to check what changed._")
		} else {
			b.WriteString("\nReply in this thread to iterate, or say \"publish\" to make it live.")
		}
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

// isSuccessState reports whether a Netlify run/session state string
// means "completed successfully." Netlify uses both "done" (current)
// and "succeeded" (per OpenAPI spec) at different surfaces — accept
// either.
func isSuccessState(s string) bool {
	return s == "done" || s == "succeeded"
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
