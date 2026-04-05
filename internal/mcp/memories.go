package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/services"
)

func memoryMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services, caller *services.Caller) mcpserver.ToolHandlerFunc {
	switch name {
	case "save_memory":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			content, _ := req.RequireString("content")
			scope := req.GetString("scope", "tenant")
			if err := svc.Memories.Save(ctx, caller, content, scope, uuid.Nil); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Memory saved."), nil
		}
	case "search_memories":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query, _ := req.RequireString("query")
			results, err := svc.Memories.Search(ctx, caller, query)
			if err != nil {
				return nil, err
			}
			if len(results) == 0 {
				return mcp.NewToolResultText("No relevant memories found."), nil
			}
			var b strings.Builder
			for _, m := range results {
				fmt.Fprintf(&b, "- [%s] %s\n", m.ID, m.Content)
			}
			return mcp.NewToolResultText(b.String()), nil
		}
	case "forget_memory":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("memory_id")
			memoryID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid memory ID."), nil
			}
			if err := svc.Memories.Forget(ctx, caller, memoryID); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Memory forgotten."), nil
		}
	default:
		return nil
	}
}
