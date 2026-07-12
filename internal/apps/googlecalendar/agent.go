package googlecalendar

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mrdon/kit/internal/tools"
)

func registerGoogleCalendarAgentTools(r *tools.Registry, a *App) {
	for _, meta := range googleCalendarTools {
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     googleCalendarAgentHandler(meta.Name, a),
		})
	}
}

func googleCalendarAgentHandler(name string, a *App) tools.HandlerFunc {
	switch name {
	case "gcal_check":
		return handleCheck(a)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown google calendar tool: %s", name)
		}
	}
}

func handleCheck(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		msg, err := a.CheckWriteAccess(ec.Ctx, ec.Caller().TenantID)
		if err != nil {
			if errors.Is(err, ErrNotConfigured) {
				return "Google Calendar isn't connected yet. Configure it first.", nil
			}
			if errors.Is(err, ErrNotReady) {
				return "Google Calendar app isn't configured on this server.", nil
			}
			return "Error: " + err.Error(), nil
		}
		return msg, nil
	}
}
