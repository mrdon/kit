package menu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// Telling the team when a beer is 86'd.
//
// The person who finds the keg blown is rarely the one who reorders it, or
// the one about to promise it to a table. So an 86 is posted to the channel
// an admin picked, saying what and who. Only the 86 is posted, not bringing
// a beer back or Untappd clearing it on a sync: the post is the heads-up that
// something ran out, and the channel does not need a second line for every
// mistaken tick.
//
// A post that fails is logged and otherwise dropped. The 86 itself has saved
// by then, and the board is what matters mid-rush.

// EightySixChannel is a workspace's choice of where 86s are posted. An empty
// ID means nowhere. Name is for display.
type EightySixChannel struct {
	ID   string
	Name string
}

// Off reports whether 86s go nowhere.
func (c EightySixChannel) Off() bool { return c.ID == "" }

// GetEightySixChannel reads the workspace's setting; one never saved posts
// nowhere.
func (a *App) GetEightySixChannel(ctx context.Context, tenantID uuid.UUID) (EightySixChannel, error) {
	var c EightySixChannel
	err := a.pool.QueryRow(ctx, `
		SELECT channel_id, channel_name FROM app_menu_eighty_six
		 WHERE tenant_id = $1`, tenantID).Scan(&c.ID, &c.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return EightySixChannel{}, nil
	}
	if err != nil {
		return c, fmt.Errorf("loading 86 channel: %w", err)
	}
	return c, nil
}

// SaveEightySixChannel stores the workspace's choice, leaving the list alone.
func (a *App) SaveEightySixChannel(ctx context.Context, tenantID uuid.UUID, c EightySixChannel) error {
	const q = `INSERT INTO app_menu_eighty_six (tenant_id, channel_id, channel_name)
	           VALUES ($1, $2, $3)
	           ON CONFLICT (tenant_id) DO UPDATE
	             SET channel_id = EXCLUDED.channel_id,
	                 channel_name = EXCLUDED.channel_name,
	                 updated_at = NOW()`
	if _, err := a.pool.Exec(ctx, q, tenantID, c.ID, c.Name); err != nil {
		return fmt.Errorf("saving 86 channel: %w", err)
	}
	return nil
}

// saveEightySixBy replaces the list as SaveEightySix does, then posts the
// beers that were not on it before. prev is the list as it stood.
func (a *App) saveEightySixBy(ctx context.Context, tenantID uuid.UUID, caller *services.Caller, prev, next []string) error {
	if err := SaveEightySix(ctx, a.pool, tenantID, next); err != nil {
		return err
	}
	if added := newlyEightySixed(prev, next); len(added) > 0 {
		// Off the request: the bar iPad saves on every tick, and Slack
		// being slow should not make the checkbox feel stuck.
		who := eightySixWho(caller)
		go a.postEightySix(context.WithoutCancel(ctx), tenantID, added, who)
	}
	return nil
}

// newlyEightySixed is the beers in next that prev did not have.
func newlyEightySixed(prev, next []string) []string {
	had := glutenReducedKeys(prev)
	var added []string
	for _, b := range tidyBeerNames(next) {
		if !had[nameKey(b)] {
			added = append(added, b)
		}
	}
	return added
}

// eightySixWho says who did it: a person as a mention, a job by its name.
// Most 86s come from the taproom device, whose login is the device and not
// whoever is holding it, so the best there is says which device.
func eightySixWho(c *services.Caller) string {
	switch {
	case c == nil:
		return ""
	case c.IsUser() && c.Identity != "":
		return " by <@" + c.Identity + ">"
	case c.Label == "":
		return ""
	case c.Kind == services.CallerDevice:
		return " on " + slackEscape(c.Label)
	default:
		return " by " + slackEscape(c.Label)
	}
}

// eightySixText is the Slack message.
func eightySixText(beers []string, who string) string {
	names := make([]string, 0, len(beers))
	for _, b := range beers {
		names = append(names, "*"+slackEscape(b)+"*")
	}
	return strings.Join(names, ", ") + " 86'd" + who
}

// slackEscape neutralises the three characters Slack treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (a *App) postEightySix(ctx context.Context, tenantID uuid.UUID, beers []string, who string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	text := eightySixText(beers, who)
	target, err := a.GetEightySixChannel(ctx, tenantID)
	if err != nil || target.Off() {
		if err != nil {
			slog.Warn("loading 86 channel", "tenant_id", tenantID, "error", err)
		}
		return
	}
	client, err := kitslack.TenantClient(ctx, a.pool, a.enc, tenantID)
	if err == nil {
		err = client.PostMessage(ctx, target.ID, "", text)
	}
	if err != nil {
		slog.Warn("posting 86 to slack", "tenant_id", tenantID, "channel", target.Name, "text", text, "error", err)
	}
}
