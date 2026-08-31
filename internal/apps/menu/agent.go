package menu

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

func registerAgentTools(_ context.Context, registerer any, caller *services.Caller, isAdmin bool, a *App) {
	r, ok := registerer.(*tools.Registry)
	if !ok || !isAdmin || caller == nil || a.svc == nil {
		return
	}
	for _, meta := range toolMetas() {
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     agentHandler(meta.Name, a),
		})
	}
}

func agentHandler(name string, a *App) tools.HandlerFunc {
	switch name {
	case "set_menu_board":
		return handleSetBoard(a)
	case "get_menu_board":
		return handleGetBoard(a)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown menu tool: %s", name)
		}
	}
}

func handleSetBoard(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var args setBoardArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parsing set_menu_board arguments: %w", err)
		}
		return saveBoard(ec.Ctx, ec.Pool, a, ec.Tenant.ID, args)
	}
}

func handleGetBoard(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var args getBoardArgs
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parsing get_menu_board arguments: %w", err)
			}
		}
		return listBoards(ec.Ctx, ec.Pool, a, ec.Tenant.ID, args)
	}
}
