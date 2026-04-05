package mcp

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/services"
)

func tenantMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services, caller *services.Caller) mcpserver.ToolHandlerFunc {
	switch name {
	case "update_tenant":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			businessType, _ := req.RequireString("business_type")
			timezone := req.GetString("timezone", "UTC")
			if err := svc.Tenants.Update(ctx, caller, businessType, timezone); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Organization info updated."), nil
		}
	default:
		return nil
	}
}
