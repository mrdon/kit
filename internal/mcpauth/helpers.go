// Package mcpauth provides shared helpers for MCP tool handlers that need
// to resolve the authenticated caller from the request context.
//
// Tools are registered at the MCPServer level (not per-session), so each
// handler must read the caller from ctx on every invocation — this avoids
// losing tool visibility when the server restarts and session state is wiped.
package mcpauth

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// WithCaller wraps a caller-aware tool handler, extracting the Caller from
// the request context. Returns an auth error result if no caller is present.
func WithCaller(fn func(context.Context, mcp.CallToolRequest, *services.Caller) (*mcp.CallToolResult, error)) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		caller := auth.CallerFromContext(ctx)
		if caller == nil {
			return mcp.NewToolResultError("Not authenticated."), nil
		}
		return fn(ctx, req, caller)
	}
}
