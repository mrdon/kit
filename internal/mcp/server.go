package mcp

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// ServerHolder wraps the MCPServer so the hook closure can reference it.
type ServerHolder struct {
	Server *mcpserver.MCPServer
	pool   *pgxpool.Pool
	svc    *services.Services
}

// NewServer creates an MCP server that uses per-session tool filtering.
// No tools are registered at the server level — they're added per session
// based on the authenticated caller's permissions.
func NewServer(pool *pgxpool.Pool, svc *services.Services) *ServerHolder {
	sh := &ServerHolder{pool: pool, svc: svc}

	hooks := &mcpserver.Hooks{}
	hooks.AddOnRegisterSession(func(ctx context.Context, session mcpserver.ClientSession) {
		caller := auth.CallerFromContext(ctx)
		if caller == nil {
			slog.Warn("mcp session registered without caller", "session_id", session.SessionID())
			return
		}
		tools := buildSessionTools(pool, svc, caller)
		if err := sh.Server.AddSessionTools(session.SessionID(), tools...); err != nil {
			slog.Error("adding session tools", "error", err, "session_id", session.SessionID())
		}
	})

	sh.Server = mcpserver.NewMCPServer(
		"kit",
		"1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithResourceCapabilities(true, false),
		mcpserver.WithHooks(hooks),
	)

	registerResources(sh.Server, pool, svc)

	return sh
}

// buildSessionTools returns the tools this caller is authorized to use.
func buildSessionTools(pool *pgxpool.Pool, svc *services.Services, caller *services.Caller) []mcpserver.ServerTool {
	allMetas := []struct {
		metas   []services.ToolMeta
		handler func(string, *pgxpool.Pool, *services.Services, *services.Caller) mcpserver.ToolHandlerFunc
	}{
		{services.SkillTools, skillMCPHandler},
		{services.RuleTools, ruleMCPHandler},
		{services.MemoryTools, memoryMCPHandler},
		{services.RoleTools, roleMCPHandler},
		{services.TaskTools, taskMCPHandler},
		{services.TenantTools, tenantMCPHandler},
	}

	var tools []mcpserver.ServerTool
	for _, group := range allMetas {
		for _, meta := range group.metas {
			if meta.AdminOnly && !caller.IsAdmin {
				continue
			}
			schemaJSON, _ := json.Marshal(meta.Schema)
			tool := mcp.NewToolWithRawSchema(meta.Name, meta.Description, schemaJSON)
			tools = append(tools, mcpserver.ServerTool{
				Tool:    tool,
				Handler: group.handler(meta.Name, pool, svc, caller),
			})
		}
	}
	return tools
}
