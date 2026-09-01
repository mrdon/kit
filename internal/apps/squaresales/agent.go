package squaresales

import (
	"encoding/json"
	"fmt"

	"github.com/mrdon/kit/internal/tools"
)

func registerSquareSalesAgentTools(r *tools.Registry, a *App) {
	for _, meta := range squareSalesTools {
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     squareSalesAgentHandler(meta.Name, a),
		})
	}
}

func squareSalesAgentHandler(name string, a *App) tools.HandlerFunc {
	switch name {
	case "squaresales_sync_now":
		return handleSyncNow(a)
	case "squaresales_post_card_now":
		return handlePostCard(a)
	case "squaresales_status":
		return handleStatus(a)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown square sales tool: %s", name)
		}
	}
}

func handleSyncNow(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		sum, err := a.RunSync(ec.Ctx, ec.Caller().TenantID, "manual")
		if err != nil {
			return salesErrorMessage(err), nil
		}
		return formatSyncSummary(sum), nil
	}
}

func handlePostCard(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var args postCardArgs
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parsing post-card args: %w", err)
			}
		}
		return postCard(ec.Ctx, a, ec.Caller().TenantID, args)
	}
}

func handleStatus(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		out, err := formatStatus(ec.Ctx, a, ec.Caller().TenantID)
		if err != nil {
			return "", err
		}
		return out, nil
	}
}
