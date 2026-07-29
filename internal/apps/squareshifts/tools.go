package squareshifts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/services"
)

// squareShiftsTools is the shared ToolMeta list for the agent + MCP surfaces.
var squareShiftsTools = []services.ToolMeta{
	{
		Name:        "squareshifts_sync_now",
		Description: "Run the Square → Google Calendar shift sync immediately for this workspace and report what changed. Requires the Square and Google Calendar integrations to be connected.",
		AdminOnly:   true,
		Schema:      services.Props(map[string]any{}),
	},
	{
		Name:        "squareshifts_reconcile",
		Description: "Run the drift-repair reconciliation for this workspace: recreate shift events deleted out-of-band in Google, and delete events this sync owns that no longer back a published shift. Pass dry_run true first to preview exactly what would change — this is the only operation that deletes calendar events.",
		AdminOnly:   true,
		Schema: services.Props(map[string]any{
			"dry_run": services.Field("boolean", "Preview the changes without touching the calendar. Strongly recommended before a real run."),
		}),
	},
	{
		Name:        "squareshifts_status",
		Description: "Show the result of the most recent Square shift sync (when it ran, how many events changed, any error).",
		AdminOnly:   true,
		Schema:      services.Props(map[string]any{}),
	},
}

// reconcileArgs is the shared input shape for squareshifts_reconcile, parsed
// by both the agent and MCP handlers.
type reconcileArgs struct {
	DryRun bool `json:"dry_run"`
}

// formatReconcilePlan renders a dry run. Deletions are itemized rather than
// counted: this is the one sweep that removes entries from a calendar real
// people rely on, so an operator should see which ones before approving.
func formatReconcilePlan(p reconcilePlan) string {
	if p.empty() {
		return "Dry run: no drift — the calendar already matches Square's published schedule. Nothing would change."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: %d event(s) would be created, %d deleted. Nothing has changed yet.\n", len(p.Create), len(p.Delete))
	if len(p.Create) > 0 {
		fmt.Fprintf(&b, "\nWould create (missing from the calendar):\n")
		for _, d := range p.Create {
			fmt.Fprintf(&b, "  + %s — %s\n", eventDateLabel(d.event), d.event.Summary)
		}
	}
	if len(p.Delete) > 0 {
		fmt.Fprintf(&b, "\nWould DELETE (owned by this sync, no longer a published shift):\n")
		for _, e := range p.Delete {
			fmt.Fprintf(&b, "  - %s — %s\n", eventDateLabel(&e), e.Summary)
		}
	}
	return b.String()
}

// eventDateLabel is the event's start date for operator-facing listings,
// handling both the all-day (Date) and timed (DateTime) shapes.
func eventDateLabel(e *googlecalendar.Event) string {
	if e == nil || e.Start == nil {
		return "(no date)"
	}
	if e.Start.Date != "" {
		return e.Start.Date
	}
	if len(e.Start.DateTime) >= 10 {
		return e.Start.DateTime[:10]
	}
	return "(no date)"
}

// formatSummary renders a sync result. Shared by both surfaces.
func formatSummary(sum SyncSummary) string {
	return fmt.Sprintf("Sync complete: %d created, %d updated, %d deleted.", sum.Created, sum.Updated, sum.Deleted)
}

// formatStatus renders the last-run status. Shared by both surfaces.
func formatStatus(ctx context.Context, a *App, tenantID uuid.UUID) (string, error) {
	lr, ok, err := getLastRun(ctx, a, tenantID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "No sync has run yet for this workspace.", nil
	}
	ago := time.Since(lr.CreatedAt).Round(time.Minute)
	when := fmt.Sprintf("%s ago (%s)", ago, lr.CreatedAt.UTC().Format("2006-01-02 15:04 MST"))
	if lr.Action == actionSyncFailed {
		return fmt.Sprintf("Last sync FAILED %s — %s", when, lr.Meta.Error), nil
	}
	return fmt.Sprintf("Last sync %s: %d created, %d updated, %d deleted (%s trigger).",
		when, lr.Meta.Created, lr.Meta.Updated, lr.Meta.Deleted, lr.Meta.TriggeredBy), nil
}
