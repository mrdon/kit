package squareshifts

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/tools"
)

func registerSquareShiftsAgentTools(r *tools.Registry, a *App) {
	for _, meta := range squareShiftsTools {
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     squareShiftsAgentHandler(meta.Name, a),
		})
	}
}

func squareShiftsAgentHandler(name string, a *App) tools.HandlerFunc {
	switch name {
	case "squareshifts_sync_now":
		return handleSyncNow(a)
	case "squareshifts_status":
		return handleStatus(a)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown square shifts tool: %s", name)
		}
	}
}

func handleSyncNow(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		sum, err := a.RunSync(ec.Ctx, ec.Caller().TenantID, "manual")
		if err != nil {
			return syncErrorMessage(err), nil
		}
		return formatSummary(sum), nil
	}
}

func handleStatus(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		msg, err := formatStatus(ec.Ctx, a, ec.Caller().TenantID)
		if err != nil {
			return "", fmt.Errorf("reading sync status: %w", err)
		}
		return msg, nil
	}
}

// syncErrorMessage turns the common not-configured errors into setup hints
// and everything else into a generic error line.
func syncErrorMessage(err error) string {
	switch {
	case errors.Is(err, square.ErrNotConfigured):
		return "Square isn't connected yet. Configure the Square integration first."
	case errors.Is(err, googlecalendar.ErrNotConfigured):
		return "Google Calendar isn't connected yet. Configure it first."
	case errors.Is(err, square.ErrNotReady):
		return "Square app credentials aren't configured on this server."
	case errors.Is(err, googlecalendar.ErrNotReady):
		return "Google Calendar app isn't configured on this server."
	default:
		return "Error: " + err.Error()
	}
}
