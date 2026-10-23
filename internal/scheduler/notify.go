package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// Every job, whatever runs it, tells its owner in Slack when it starts
// failing and again when it recovers. Once per failure, not once per run: a
// minute job with a broken setting would otherwise ping sixty times an hour,
// and a DM that arrives every run stops being read. The row's own last_error
// is the memory -- the run before this one either failed or it did not.

type jobAlert int

const (
	jobAlertNone jobAlert = iota
	jobAlertFailed
	jobAlertRecovered
)

// jobAlertFor compares the error the previous run left with this run's.
func jobAlertFor(prevErr, newErr *string) jobAlert {
	switch {
	case newErr != nil && prevErr == nil:
		return jobAlertFailed
	case newErr == nil && prevErr != nil:
		return jobAlertRecovered
	}
	return jobAlertNone
}

// maxAlertError keeps a long error (a sync log, a stack of API text) inside
// what reads comfortably in a DM; last_error keeps the whole thing.
const maxAlertError = 2500

func jobAlertText(kind jobAlert, job models.Job, errText string) string {
	if kind == jobAlertRecovered {
		return fmt.Sprintf("✅ Scheduled job is working again: _%s_", job.Description)
	}
	if len(errText) > maxAlertError {
		errText = errText[:maxAlertError] + "…"
	}
	return fmt.Sprintf("⚠️ Scheduled job failed: _%s_\n```\n%s\n```", job.Description, errText)
}

// notifyJobOutcome DMs the job's owner if this run changed whether it is
// failing. job is the row as claimed, so job.LastError is the previous run's.
// Sending is best effort: the outcome is already in last_error.
func (s *Scheduler) notifyJobOutcome(ctx context.Context, job models.Job, newErr *string) {
	kind := jobAlertFor(job.LastError, newErr)
	if kind == jobAlertNone {
		return
	}
	errText := ""
	if newErr != nil {
		errText = *newErr
	}
	if err := s.dmJobOwner(ctx, job, jobAlertText(kind, job, errText)); err != nil {
		slog.Warn("job alert not sent", "job_id", job.ID, "error", err)
	}
}

// dmJobOwner sends to the job's creator, or to the job's channel when the
// creator cannot be found.
func (s *Scheduler) dmJobOwner(ctx context.Context, job models.Job, text string) error {
	if s.enc == nil {
		return errors.New("slack is not configured on this deployment")
	}
	tenant, err := models.GetTenantByID(ctx, s.pool, job.TenantID)
	if err != nil {
		return fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return fmt.Errorf("tenant %s not found", job.TenantID)
	}
	token, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		return fmt.Errorf("decrypting bot token: %w", err)
	}
	slack := kitslack.NewClient(token)
	target := job.ChannelID
	if user, err := models.GetUserByID(ctx, s.pool, job.TenantID, job.CreatedBy); err == nil && user != nil {
		if target, err = slack.OpenConversation(ctx, user.SlackUserID); err != nil {
			return fmt.Errorf("opening DM: %w", err)
		}
	}
	if target == "" {
		return errors.New("job has no owner or channel to tell")
	}
	if err := slack.PostMessage(ctx, target, "", text); err != nil {
		return fmt.Errorf("posting alert: %w", err)
	}
	return nil
}
