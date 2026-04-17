package mcp

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/scheduler"
	"github.com/mrdon/kit/internal/services"
)

// ServerHolder wraps the MCPServer so external code can reach the registered server.
type ServerHolder struct {
	Server *mcpserver.MCPServer
	pool   *pgxpool.Pool
	svc    *services.Services
	agent  *agent.Agent
	enc    *crypto.Encryptor
	sched  *scheduler.Scheduler
}

// NewServer creates an MCP server with all tools registered at the server level.
// Each handler resolves the authenticated caller from ctx at call time via the
// mcpauth.WithCaller wrapper, so restarts don't wipe client-visible tool state.
// Admin-only tools are hidden from non-admins via a ToolFilter on tools/list;
// the service layer still enforces authorization independently.
func NewServer(pool *pgxpool.Pool, svc *services.Services, a *agent.Agent, enc *crypto.Encryptor, sched *scheduler.Scheduler) *ServerHolder {
	sh := &ServerHolder{pool: pool, svc: svc, agent: a, enc: enc, sched: sched}

	adminOnly := collectAdminOnlyToolNames()

	sh.Server = mcpserver.NewMCPServer(
		"kit",
		"1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithResourceCapabilities(true, false),
		mcpserver.WithToolFilter(func(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
			caller := auth.CallerFromContext(ctx)
			if caller != nil && caller.IsAdmin {
				return tools
			}
			filtered := make([]mcp.Tool, 0, len(tools))
			for _, t := range tools {
				if adminOnly[t.Name] {
					continue
				}
				filtered = append(filtered, t)
			}
			return filtered
		}),
	)

	registerResources(sh.Server, pool, svc)

	tools := buildAllTools(pool, svc, a, enc, sched)
	sh.Server.AddTools(tools...)

	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Tool.Name
	}
	slog.Info("mcp server tools registered", "tool_count", len(tools), "tools", toolNames)

	return sh
}

// buildAllTools collects every MCP tool — core + app-contributed — into a single
// slice for server-level registration. Handlers resolve the caller per request.
func buildAllTools(pool *pgxpool.Pool, svc *services.Services, a *agent.Agent, enc *crypto.Encryptor, sched *scheduler.Scheduler) []mcpserver.ServerTool {
	allMetas := []struct {
		metas   []services.ToolMeta
		handler func(string, *pgxpool.Pool, *services.Services) mcpserver.ToolHandlerFunc
	}{
		{services.SkillTools, skillMCPHandler},
		{services.RuleTools, ruleMCPHandler},
		{services.MemoryTools, memoryMCPHandler},
		{services.RoleTools, roleMCPHandler},
		{services.TaskTools, taskMCPHandler},
		{services.TenantTools, tenantMCPHandler},
		{services.UserTools, userMCPHandler},
		{services.SessionTools, sessionMCPHandler},
	}

	var tools []mcpserver.ServerTool
	for _, group := range allMetas {
		for _, meta := range group.metas {
			schemaJSON, _ := json.Marshal(meta.Schema)
			tool := mcp.NewToolWithRawSchema(meta.Name, meta.Description, schemaJSON)
			tools = append(tools, mcpserver.ServerTool{
				Tool:    tool,
				Handler: group.handler(meta.Name, pool, svc),
			})
		}
	}
	tools = append(tools, apps.BuildMCPTools(pool, svc)...)

	// run_task needs agent + enc + scheduler, registered separately from the standard loop
	tools = append(tools, buildRunTaskTool(pool, svc, a, enc, sched))

	return tools
}

// collectAdminOnlyToolNames builds the set of tool names flagged AdminOnly,
// across both core service ToolMetas and each registered app's ToolMetas.
// Used by the tools/list filter to hide admin tools from non-admins.
func collectAdminOnlyToolNames() map[string]bool {
	out := map[string]bool{}
	groups := [][]services.ToolMeta{
		services.SkillTools, services.RuleTools, services.MemoryTools,
		services.RoleTools, services.TaskTools, services.TenantTools,
		services.UserTools, services.SessionTools,
	}
	for _, g := range groups {
		for _, m := range g {
			if m.AdminOnly {
				out[m.Name] = true
			}
		}
	}
	for _, a := range apps.All() {
		for _, m := range a.ToolMetas() {
			if m.AdminOnly {
				out[m.Name] = true
			}
		}
	}
	return out
}
