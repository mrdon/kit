package widget

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/services"
)

// buildWidgetMCPTools assembles the MCP-side ServerTools for both
// the widget-token and widget-analytics groups. Schemas come from
// services.ToolMeta, handlers from the per-group switch funcs in
// mcp_tokens.go / mcp_analytics.go. Schemas are re-marshalled with
// the require_approval field injected so the universal caller-gate
// flag works on this surface too.
func buildWidgetMCPTools(pool *pgxpool.Pool, svc *services.Services) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	groups := []struct {
		metas []services.ToolMeta
		fn    func(name string, pool *pgxpool.Pool, svc *services.Services) mcpserver.ToolHandlerFunc
	}{
		{services.WidgetTokenTools, widgetTokenMCPHandler},
		{services.WidgetAnalyticsTools, widgetAnalyticsMCPHandler},
	}
	for _, g := range groups {
		for _, meta := range g.metas {
			schema := services.InjectRequireApprovalSchema(meta.Schema)
			schemaJSON, _ := json.Marshal(schema)
			tool := mcp.NewToolWithRawSchema(meta.Name, meta.Description, schemaJSON)
			handler := g.fn(meta.Name, pool, svc)
			out = append(out, mcpserver.ServerTool{Tool: tool, Handler: handler})
		}
	}
	return out
}
