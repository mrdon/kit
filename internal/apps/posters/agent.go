package posters

import (
	"context"
	"encoding/json"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// chatToolNames is the clamp for poster and template chat: the app's own
// tools plus what every chat needs (skills, attachments, the reply).
var chatToolNames = []string{
	"get_poster", "edit_poster_source", "set_poster_fields", "render_poster_formats",
	"search_photos", "find_stock_photos", "ask_for_photo", "update_poster_facts", "set_poster_on_event",
	"list_templates", "get_template", "edit_template_source", "create_template",
	"load_skill", "search_skills", "read_attachment", "reply_in_thread",
}

// registerAgentTools exposes the poster tools to the in-app agent. Each
// handler runs the SAME dispatchCore the MCP surface uses. Images a tool
// produced are attached to the result for the model to look at.
func (a *App) registerAgentTools(ctx context.Context, r *tools.Registry, caller *services.Caller, isAdmin bool) {
	stock := a.stockAllowed(ctx, caller)
	for _, meta := range posterTools {
		if mcpOnlyTools[meta.Name] || (meta.AdminOnly && !isAdmin) {
			continue
		}
		if meta.Name == "find_stock_photos" && !stock {
			continue
		}
		name := meta.Name
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler: func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
				res, err := a.dispatchCore(ec.Ctx, ec.Caller(), name, input)
				if err != nil {
					return "", err
				}
				for _, img := range res.Images {
					ec.AttachImage(img.Mime, img.Data)
				}
				return res.Text, nil
			},
		})
	}
}

// stockAllowed reads the tenant switch that decides whether the agent
// gets the Pixabay tool at all.
func (a *App) stockAllowed(ctx context.Context, caller *services.Caller) bool {
	if caller == nil || a.pool == nil || !a.pixabay.enabled() {
		return false
	}
	s, err := getSettings(ctx, a.pool, caller.TenantID)
	return err == nil && s.AllowStockPhotos
}
