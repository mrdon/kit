package squareshifts

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

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
		Name:        "squareshifts_status",
		Description: "Show the result of the most recent Square shift sync (when it ran, how many events changed, any error).",
		AdminOnly:   true,
		Schema:      services.Props(map[string]any{}),
	},
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
