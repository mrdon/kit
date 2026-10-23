package menu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// A failed Square push used to show only on the settings page, so the wall
// said $5 while the register rang $6.50 and nobody knew until a customer
// did. Now a failure DMs the admin the workspace's system jobs belong to.
//
// Once per failure, not once per retry: the minute check retries a failed
// push every ten minutes, and the same broken beer name should not ping
// twelve times over a two-hour happy hour. A new attempt -- the next day's
// happy hour, a changed setting -- that fails again does ping, and a retry
// that finally succeeds says so, so the alert is never left standing.

type hhAlert int

const (
	hhAlertNone hhAlert = iota
	hhAlertFailed
	hhAlertRecovered
)

// hhAlertFor decides what a push that aimed Square at key and came out ok
// says, given the state recorded before it.
func hhAlertFor(prev *HappyHourState, key string, ok bool) hhAlert {
	prevFailed := prev.SyncedAt != nil && !prev.SyncOK
	switch {
	case !ok && (!prevFailed || prev.SyncedHash != key):
		return hhAlertFailed
	case ok && prevFailed && sameSide(prev.SyncedHash, key):
		// Happy hour ending is not a fix: removing nothing succeeds even while
		// the beer that broke it is still misnamed.
		return hhAlertRecovered
	}
	return hhAlertNone
}

// sameSide reports whether two square keys both mean on, or both off. A
// changed setting while on is the same side: that is how a fix arrives.
func sameSide(a, b string) bool {
	return (a == "off") == (b == "off")
}

// alertHappyHour sends the DM. Failing to send is logged, never returned: the
// push's own outcome is already recorded and is what the caller reports.
func (a *App) alertHappyHour(ctx context.Context, tenantID uuid.UUID, kind hhAlert, key, log string) {
	if kind == hhAlertNone {
		return
	}
	if err := a.dmHappyHourAdmin(ctx, tenantID, hhAlertText(kind, key, log)); err != nil {
		slog.Warn("happy hour alert not sent", "tenant_id", tenantID, "error", err)
	}
}

func hhAlertText(kind hhAlert, key, log string) string {
	if kind == hhAlertRecovered {
		return "✅ Square's happy hour is working again.\n```\n" + log + "\n```"
	}
	head := "⚠️ Happy hour did not reach Square, so the register is ringing full price."
	if key == "off" {
		head = "⚠️ Happy hour is over, but Kit could not take the discount back off Square."
	}
	return head + "\n```\n" + log + "\n```"
}

// dmHappyHourAdmin DMs the admin that system jobs are attributed to (see
// scheduler.registryCreator), with the workspace's own bot.
func (a *App) dmHappyHourAdmin(ctx context.Context, tenantID uuid.UUID, text string) error {
	if a.enc == nil {
		return errors.New("slack is not configured on this deployment")
	}
	tenant, err := models.GetTenantByID(ctx, a.pool, tenantID)
	if err != nil {
		return fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return fmt.Errorf("tenant %s not found", tenantID)
	}
	admin, err := models.FindAdminUser(ctx, a.pool, tenantID)
	if err != nil {
		return err
	}
	if admin == nil {
		return errors.New("workspace has no admin to tell")
	}
	token, err := a.enc.Decrypt(tenant.BotToken)
	if err != nil {
		return fmt.Errorf("decrypting bot token: %w", err)
	}
	client := kitslack.NewClient(token)
	ch, err := client.OpenConversation(ctx, admin.SlackUserID)
	if err != nil {
		return fmt.Errorf("opening DM: %w", err)
	}
	if err := client.PostMessage(ctx, ch, "", strings.TrimSpace(text)); err != nil {
		return fmt.Errorf("posting DM: %w", err)
	}
	return nil
}
