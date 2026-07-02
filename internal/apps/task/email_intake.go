package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/email"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

const (
	// emailIntakeInterval is how often the sweep wakes. Actual per-user
	// cadence is each row's cron schedule, checked against last_scanned_at.
	emailIntakeInterval = 15 * time.Minute
	// emailIntakeClaimLease bounds how long a claim is honored before another
	// instance may reclaim a row whose sweep crashed mid-run.
	emailIntakeClaimLease = 30 * time.Minute
	// emailIntakeMaxUsers caps users scanned per tenant per sweep (serial
	// agent runs — keep the sweep bounded).
	emailIntakeMaxUsers = 25
	// emailIntakeMaxSummaries caps the candidate list seeded into one run.
	emailIntakeMaxSummaries = 40
	// emailIntakeFirstRunWindow bounds the first-ever scan for a user.
	emailIntakeFirstRunWindow = 7 * 24 * time.Hour
)

// emailIntakeCron is the task app's periodic email→task sweep. It is the only
// place the whole feature runs; the scheduler is untouched. The closure holds
// the app (agent, enc, pool), so it can run agent sessions without any
// scheduler plumbing.
func (a *TaskApp) emailIntakeCron() apps.CronJob {
	return apps.CronJob{
		Name:     "email_intake",
		Interval: emailIntakeInterval,
		Run: func(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor) error {
			return a.runEmailIntakeSweep(ctx, pool, enc)
		},
	}
}

// runEmailIntakeSweep scans every enabled, due intake row across tenants whose
// Tasks app is enabled. Per-user failures are logged and skipped so one bad
// mailbox never stalls the sweep.
func (a *TaskApp) runEmailIntakeSweep(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor) error {
	if a.agent == nil {
		return nil // not configured (e.g. tests) — nothing to run
	}
	tenants, err := models.ListAllTenants(ctx, pool)
	if err != nil {
		return fmt.Errorf("listing tenants for email intake: %w", err)
	}
	now := time.Now()
	for i := range tenants {
		tenant := tenants[i]
		if !apps.IsEnabled(ctx, tenant.ID, a.Name()) {
			continue
		}
		intakes, err := models.ListEnabledEmailIntakes(ctx, pool, tenant.ID)
		if err != nil {
			slog.Warn("email intake: listing rows", "tenant_id", tenant.ID, "error", err)
			continue
		}
		ran := 0
		for _, in := range intakes {
			if ran >= emailIntakeMaxUsers {
				break
			}
			if !emailIntakeDue(in, tenant.Timezone, now) {
				continue
			}
			claimed, err := models.ClaimEmailIntake(ctx, pool, tenant.ID, in.ID, now.Add(-emailIntakeClaimLease))
			if err != nil {
				slog.Warn("email intake: claiming row", "tenant_id", tenant.ID, "user_id", in.UserID, "error", err)
				continue
			}
			if !claimed {
				continue // another instance owns it this cycle
			}
			ran++
			a.runEmailIntakeForUser(ctx, pool, enc, tenant, in)
		}
	}
	return nil
}

// emailIntakeDue reports whether a row's cron schedule has come due since its
// last scan. A never-scanned row is always due. A malformed cron expression is
// treated as not-due (skip rather than spin) — the console validates schedules.
func emailIntakeDue(in models.EmailIntake, tz string, now time.Time) bool {
	if in.LastScannedAt == nil {
		return true
	}
	next, err := models.NextCronRun(in.Schedule, tz, *in.LastScannedAt)
	if err != nil {
		slog.Warn("email intake: bad schedule", "user_id", in.UserID, "schedule", in.Schedule, "error", err)
		return false
	}
	return !next.After(now)
}

// runEmailIntakeForUser fetches new mail since the watermark, seeds the
// candidate summaries into an agent session run as the user, and advances the
// watermark on success. On any failure it releases the claim without advancing
// so the row retries next tick.
func (a *TaskApp) runEmailIntakeForUser(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenant models.Tenant, in models.EmailIntake) {
	release := func() {
		if err := models.ReleaseEmailIntakeClaim(ctx, pool, tenant.ID, in.UserID); err != nil {
			slog.Warn("email intake: releasing claim", "tenant_id", tenant.ID, "user_id", in.UserID, "error", err)
		}
	}

	user, err := models.GetUserByID(ctx, pool, tenant.ID, in.UserID)
	if err != nil || user == nil {
		slog.Warn("email intake: loading user", "tenant_id", tenant.ID, "user_id", in.UserID, "error", err)
		release()
		return
	}

	since := time.Now().Add(-emailIntakeFirstRunWindow)
	if in.LastScannedAt != nil {
		since = *in.LastScannedAt
	}

	inbox, err := email.SearchSince(ctx, pool, enc, tenant.ID, in.UserID, "INBOX", since, emailIntakeMaxSummaries)
	if errors.Is(err, email.ErrNotConfigured) {
		// User removed their mailbox but left intake enabled; skip quietly.
		release()
		return
	}
	if err != nil {
		slog.Warn("email intake: searching mail", "tenant_id", tenant.ID, "user_id", in.UserID, "error", err)
		release()
		return
	}

	if len(inbox) == 0 {
		// Nothing new received — advance the watermark so due-ness keeps moving.
		if err := models.AdvanceEmailIntakeWatermark(ctx, pool, tenant.ID, in.UserID, time.Now()); err != nil {
			slog.Warn("email intake: advancing empty watermark", "tenant_id", tenant.ID, "user_id", in.UserID, "error", err)
		}
		return
	}

	// Sent mail is context only — it lets the agent skip inbound threads the
	// user already replied to. Best-effort: a missing sent folder is non-fatal.
	sent, serr := email.SearchSentSince(ctx, pool, enc, tenant.ID, in.UserID, since, emailIntakeMaxSummaries)
	if serr != nil {
		slog.Warn("email intake: searching sent", "tenant_id", tenant.ID, "user_id", in.UserID, "error", serr)
		sent = nil
	}

	// Watermark tracks only received mail; advancing past a newer *sent*
	// timestamp could skip an inbound email that arrived in between.
	newest := since
	for _, s := range inbox {
		if s.Date.After(newest) {
			newest = s.Date
		}
	}

	userText := composeEmailIntakeInstructions(in.ExtraInstructions) +
		"\n\n## Received emails to review\n\n" + renderEmailSummaries(inbox)
	if len(sent) > 0 {
		userText += "\n\n## Your recent sent emails (replies you've already made)\n\n" +
			renderEmailSummaries(sent)
	}

	if err := a.runIntakeAgent(ctx, pool, enc, tenant, user, in, userText); err != nil {
		slog.Warn("email intake: agent run failed", "tenant_id", tenant.ID, "user_id", in.UserID, "error", err)
		release()
		return
	}

	if err := models.AdvanceEmailIntakeWatermark(ctx, pool, tenant.ID, in.UserID, newest); err != nil {
		slog.Warn("email intake: advancing watermark", "tenant_id", tenant.ID, "user_id", in.UserID, "error", err)
	}
}

// runIntakeAgent runs one agent session as the given user, mirroring the
// scheduled-job execution path (bot-initiated session, no Slack channel — the
// tasks it creates surface as cards). Returns an error only on setup failure or
// an agent run error, which the caller treats as "don't advance the watermark".
func (a *TaskApp) runIntakeAgent(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenant models.Tenant, user *models.User, in models.EmailIntake, userText string) error {
	botToken, err := enc.Decrypt(tenant.BotToken)
	if err != nil {
		return fmt.Errorf("decrypting bot token: %w", err)
	}
	slackClient := kitslack.NewClient(botToken)

	threadTS := fmt.Sprintf("email-intake-%s-%d", in.ID, time.Now().UnixMilli())
	session, err := models.CreateSession(ctx, pool, tenant.ID, "", threadTS, user.ID, true)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	authorName := user.SlackUserID
	if user.DisplayName != nil && *user.DisplayName != "" {
		authorName = *user.DisplayName
	}

	return a.agent.Run(ctx, agent.RunInput{
		Slack:    slackClient,
		Tenant:   &tenant,
		User:     user,
		Session:  session,
		Channel:  "",
		UserText: userText,
		Job: &agent.JobContext{
			ID:            in.ID,
			Description:   "Email intake",
			AuthorSlackID: user.SlackUserID,
			AuthorName:    authorName,
		},
	})
}

// renderEmailSummaries formats the candidate list seeded into the agent
// message: one compact line per email plus its snippet, keyed by uid so the
// agent can read_email the ones worth opening.
func renderEmailSummaries(summaries []email.Summary) string {
	var b strings.Builder
	for _, m := range summaries {
		date := ""
		if !m.Date.IsZero() {
			date = m.Date.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&b, "- uid=%d | %s | from: %s | %s\n", m.UID, date, m.From, m.Subject)
		if m.Snippet != "" {
			fmt.Fprintf(&b, "    %s\n", m.Snippet)
		}
	}
	return b.String()
}

// defaultEmailIntakeInstructions renders the baked triage prose. It ships in
// the binary and is always current — never copied into the DB — so improving
// it reaches every tenant on deploy.
func defaultEmailIntakeInstructions() string {
	return mustRender("system_email_intake.tmpl", nil)
}

// composeEmailIntakeInstructions layers a user's per-row append onto the baked
// default. Users can only add to the default, never edit it out, so the core
// triage logic (bills-vs-receipts, dedup) always applies.
func composeEmailIntakeInstructions(extra string) string {
	base := defaultEmailIntakeInstructions()
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return base
	}
	return base + "\n\n## Additional instructions from this user\n\n" + extra
}
