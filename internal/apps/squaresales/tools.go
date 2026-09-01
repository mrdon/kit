package squaresales

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/services"
)

// squareSalesTools is the shared ToolMeta list for the agent + MCP surfaces.
//
// Two operational tools only. There is deliberately no "read yesterday's
// sales" tool: the card is the delivery mechanism, and because this app
// creates no agent session, these two plus audit_events and
// jobs.last_error are the entire debugging trail.
var squareSalesTools = []services.ToolMeta{
	{
		Name:        "squaresales_sync_now",
		Description: "Pull Square sales rollups for this workspace immediately and report what was stored. Use after connecting Square, or to fill a gap after an outage.",
		AdminOnly:   true,
		Schema:      services.Props(map[string]any{}),
	},
	{
		Name:        "squaresales_post_card_now",
		Description: "Build the daily sales card for one business day and post it to the feed immediately, instead of waiting for the morning run. Defaults to yesterday. Pass preview true to see the card text without posting. Posting the same day twice creates two cards.",
		AdminOnly:   true,
		Schema: services.Props(map[string]any{
			"date":    services.Field("string", "Business date to report, YYYY-MM-DD. Omit for yesterday."),
			"preview": services.Field("boolean", "Render the card without posting it."),
		}),
	},
	{
		Name:        "squaresales_status",
		Description: "Show the state of the Square sales sync: how far back the data goes, when it last ran, how many days are stored, and any error.",
		AdminOnly:   true,
		Schema:      services.Props(map[string]any{}),
	},
}

// postCardArgs is the shared input shape for squaresales_post_card_now,
// parsed identically by both surfaces.
type postCardArgs struct {
	Date    string `json:"date"`
	Preview bool   `json:"preview"`
}

// resolveCardDate parses a YYYY-MM-DD date, defaulting to yesterday.
// Shared so a malformed date reads the same on both surfaces.
func resolveCardDate(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return Yesterday(), nil
	}
	d, err := time.Parse(time.DateOnly, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("date must be YYYY-MM-DD: %w", err)
	}
	return d, nil
}

// postCard runs the shared post-or-preview path for both surfaces.
func postCard(ctx context.Context, a *App, tenantID uuid.UUID, args postCardArgs) (string, error) {
	date, err := resolveCardDate(args.Date)
	if err != nil {
		return "", err
	}
	if args.Preview {
		return a.PreviewCard(ctx, tenantID, date)
	}
	posted, err := a.PostCardFor(ctx, tenantID, date)
	if err != nil {
		return "", err
	}
	if !posted {
		return fmt.Sprintf("No card posted for %s — that day is closed or has no synced sales.", date.Format(time.DateOnly)), nil
	}
	return fmt.Sprintf("Posted the sales card for %s.", date.Format(time.DateOnly)), nil
}

// formatSyncSummary renders a sync result. Shared so both surfaces emit
// byte-identical text.
func formatSyncSummary(sum SyncSummary) string {
	if sum.Days == 0 {
		return "Sync complete: no new sales days to store."
	}
	return fmt.Sprintf("Sync complete: %d day(s), %d hourly buckets, %d item rows stored.",
		sum.Days, sum.Hours, sum.Items)
}

// formatStatus renders coverage plus last-run state.
func formatStatus(ctx context.Context, a *App, tenantID uuid.UUID) (string, error) {
	var b strings.Builder

	earliest, err := earliestDaily(ctx, a.pool, tenantID)
	if err != nil {
		return "", err
	}
	if earliest.IsZero() {
		b.WriteString("No sales data stored yet for this workspace.")
	} else {
		days, err := listDailyRange(ctx, a.pool, tenantID, earliest, time.Now().UTC())
		if err != nil {
			return "", err
		}
		var open int
		for _, d := range days {
			if d.Open() {
				open++
			}
		}
		fmt.Fprintf(&b, "Sales data covers %s to %s: %d day(s) stored, %d with sales.",
			earliest.Format(time.DateOnly), days[len(days)-1].Date.Format(time.DateOnly), len(days), open)
	}

	lr, ok, err := getLastRun(ctx, a, tenantID)
	if err != nil {
		return "", err
	}
	if !ok {
		b.WriteString("\nNo sync has run yet.")
		return b.String(), nil
	}
	ago := time.Since(lr.CreatedAt).Round(time.Minute)
	when := fmt.Sprintf("%s ago (%s)", ago, lr.CreatedAt.UTC().Format("2006-01-02 15:04 MST"))
	if lr.Action == actionSyncFailed {
		fmt.Fprintf(&b, "\nLast sync FAILED %s — %s", when, lr.Meta.Error)
		return b.String(), nil
	}
	fmt.Fprintf(&b, "\nLast sync %s: %d day(s) stored (%s trigger).", when, lr.Meta.Days, lr.Meta.TriggeredBy)
	return b.String(), nil
}

// salesErrorMessage turns a sync failure into something the reader can act
// on. The scope case is the one worth spelling out: Kit's integration page
// reports a scope-less token as a healthy green "Connected", so without
// this the symptom is simply an absent card.
func salesErrorMessage(err error) string {
	switch {
	case errors.Is(err, square.ErrNotConfigured):
		return "Square isn't connected for this workspace yet. Connect it on the Integrations page first."
	case errors.Is(err, square.ErrMissingScope):
		return "Square is connected, but its access token can't read sales reporting. In the Square Developer Console open Credentials → Production, copy the Production Access Token, then on Kit's Integrations page Disconnect and reconnect Square with that token. (Reconnect alone won't work — it can't accept new secrets for an existing integration.)"
	case errors.Is(err, square.ErrNotReady):
		return "Square app credentials aren't configured on this server."
	default:
		return fmt.Sprintf("Square sales sync failed: %v", err)
	}
}
