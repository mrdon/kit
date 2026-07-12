package square

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mrdon/kit/internal/tools"
)

func registerSquareAgentTools(r *tools.Registry, a *App) {
	for _, meta := range squareTools {
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     squareAgentHandler(meta.Name, a),
		})
	}
}

func squareAgentHandler(name string, a *App) tools.HandlerFunc {
	switch name {
	case "square_list_shifts":
		return handleListShifts(a)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown square tool: %s", name)
		}
	}
}

func handleListShifts(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Start string `json:"start"`
			End   string `json:"end"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		caller := ec.Caller()
		start, end, err := resolveRange(caller.Timezone, inp.Start, inp.End)
		if err != nil {
			return "Error: " + err.Error(), nil
		}
		shifts, err := a.ListPublishedShifts(ec.Ctx, caller.TenantID, start, end)
		if err != nil {
			if errors.Is(err, ErrNotConfigured) {
				return "Square isn't connected yet. Configure the Square integration first.", nil
			}
			if errors.Is(err, ErrNotReady) {
				return "Square app credentials aren't configured on this server.", nil
			}
			return "Error: " + err.Error(), nil
		}
		return formatShifts(shifts), nil
	}
}
