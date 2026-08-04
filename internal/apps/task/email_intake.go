package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/email"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
	kitslack "github.com/mrdon/kit/internal/slack"
)

const (
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

// errIntakeBusy means a scan is already running for the row (claimed by the
// cron or a prior manual trigger). errIntakeUnavailable means the agent isn't
// wired (should not happen in production).
var (
	errIntakeBusy        = errors.New("email intake scan already running")
	errIntakeUnavailable = errors.New("email intake unavailable")
)

// triggerEmailIntakeNow runs a one-off scan for a single user, bypassing the
// schedule and the enabled flag (so it works for testing before opt-in) but
// honoring the claim so it can't race the cron. It launches the scan in the
// background and returns once the claim is staked: nil = started,
// errIntakeBusy = already running, models.ErrEmailIntakeNotFound = no saved row.
func (a *TaskApp) triggerEmailIntakeNow(ctx context.Context, tenantID, userID uuid.UUID) error {
	if a.agent == nil {
		return errIntakeUnavailable
	}
	row, err := models.GetEmailIntake(ctx, a.svc.pool, tenantID, userID)
	if err != nil {
		return err // includes ErrEmailIntakeNotFound
	}
	tenant, err := models.GetTenantByID(ctx, a.svc.pool, tenantID)
	if err != nil || tenant == nil {
		return fmt.Errorf("loading tenant for manual intake: %w", err)
	}
	claimed, err := models.ClaimEmailIntake(ctx, a.svc.pool, tenantID, row.ID, time.Now().Add(-emailIntakeClaimLease))
	if err != nil {
		return err
	}
	if !claimed {
		return errIntakeBusy
	}
	// Detach from the request: the scan (IMAP + an agent run) outlives the
	// HTTP response. runEmailIntakeForUser releases/advances the claim itself.
	go a.runEmailIntakeForUser(context.Background(), a.svc.pool, a.enc, *tenant, *row)
	return nil
}

// registerEmailIntakeTask declares the intake sweep.
//
// LLMBound because runEmailIntakeForUser runs a full agent loop: this has to
// be claimed into the serialized agent lane, or a fleet of tenants sweeping
// at once would multiply LLM concurrency by the width of the function lane.
func (a *TaskApp) registerEmailIntakeTask() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "task.email_intake",
		Description: "Scan connected mailboxes for tasks",
		DefaultCron: "6,21,36,51 * * * *",
		LLMBound:    true,
		AppliesTo:   a.hasEmailIntake,
		Run: func(ctx context.Context, job models.Job) error {
			return a.runEmailIntakeForTenant(ctx, job.TenantID)
		},
	})
}

// hasEmailIntake reports whether this tenant has the app enabled and at
// least one mailbox wired up.
func (a *TaskApp) hasEmailIntake(ctx context.Context, tenantID uuid.UUID) bool {
	if a.svc == nil || a.svc.pool == nil || a.agent == nil {
		return false
	}
	if !apps.IsEnabled(ctx, tenantID, a.Name()) {
		return false
	}
	intakes, err := models.ListEnabledEmailIntakes(ctx, a.svc.pool, tenantID)
	return err == nil && len(intakes) > 0
}

// runEmailIntakeForTenant scans one tenant's enabled, due intake rows.
// Per-user failures are logged and skipped so one bad mailbox never stalls
// the rest.
//
// The outer per-tenant loop this used to carry is now the scheduler's job:
// each tenant has its own row, so a tenant whose mailbox auth has expired
// carries that failure itself instead of it disappearing into a fleet sweep.
func (a *TaskApp) runEmailIntakeForTenant(ctx context.Context, tenantID uuid.UUID) error {
	if a.agent == nil {
		return nil // not configured (e.g. tests) — nothing to run
	}
	pool := a.svc.pool
	tenant, err := models.GetTenantByID(ctx, pool, tenantID)
	if err != nil {
		return fmt.Errorf("looking up tenant for email intake: %w", err)
	}
	if tenant == nil {
		return errors.New("tenant not found")
	}

	intakes, err := models.ListEnabledEmailIntakes(ctx, pool, tenantID)
	if err != nil {
		return fmt.Errorf("listing email intake rows: %w", err)
	}
	now := time.Now()
	ran := 0
	for _, in := range intakes {
		if ran >= emailIntakeMaxUsers {
			break
		}
		if !emailIntakeDue(in, tenant.Timezone, now) {
			continue
		}
		claimed, err := models.ClaimEmailIntake(ctx, pool, tenantID, in.ID, now.Add(-emailIntakeClaimLease))
		if err != nil {
			slog.Warn("email intake: claiming row", "tenant_id", tenantID, "user_id", in.UserID, "error", err)
			continue
		}
		if !claimed {
			continue // another instance owns it this cycle
		}
		ran++
		a.runEmailIntakeForUser(ctx, pool, a.enc, *tenant, in)
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
