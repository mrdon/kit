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
	case "set_menu_source":
		return handleSetSource(a)
	case "set_menu_board":
		return handleSetBoard(a)
	case "set_menu_asset":
		return handleSetAsset(a)
	case "get_menu_board":
		return handleGetBoard(a)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown menu tool: %s", name)
		}
	}
}

// decode is lenient about an absent body: several of these tools take no
// required arguments, and an agent calling them with nothing is correct.
func decode(raw json.RawMessage, into any, tool string) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("parsing %s arguments: %w", tool, err)
	}
	return nil
}

func handleSetSource(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var args setSourceArgs
		if err := decode(raw, &args, "set_menu_source"); err != nil {
			return "", err
		}
		return applySource(ec.Ctx, ec.Pool, a, ec.Tenant.ID, args)
	}
}

func handleSetBoard(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var args setBoardArgs
		if err := decode(raw, &args, "set_menu_board"); err != nil {
			return "", err
		}
		return saveBoard(ec.Ctx, ec.Pool, a, ec.Tenant.ID, args)
	}
}

func handleSetAsset(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var args setAssetArgs
		if err := decode(raw, &args, "set_menu_asset"); err != nil {
			return "", err
		}
		// Prefer the fetcher on the exec context: it is the one the agent
		// runtime built for this request.
		f := ec.Fetcher
		if f == nil {
			f = a.fetcher
		}
		return saveAsset(ec.Ctx, ec.Pool, f, ec.Tenant.ID, args)
	}
}

func handleGetBoard(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		return describeBoard(ec.Ctx, ec.Pool, a, ec.Tenant.ID)
	}
}
