package mcp

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

func registerResources(s *mcpserver.MCPServer, pool *pgxpool.Pool, _ *services.Services) {
	s.AddResource(
		mcp.Resource{
			URI:         "kit://context",
			Name:        "Kit Knowledge Context",
			Description: "Business info, rules, skill catalog, and memories for this tenant — scoped to your roles.",
			MIMEType:    "text/plain",
		},
		func(ctx context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			caller := auth.CallerFromContext(ctx)
			if caller == nil {
				return nil, nil
			}
			tenant, err := models.GetTenantByID(ctx, pool, caller.TenantID)
			if err != nil || tenant == nil {
				return nil, err
			}
			content := services.BuildKnowledgeContext(ctx, pool, caller, tenant)
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      "kit://context",
					MIMEType: "text/plain",
					Text:     content,
				},
			}, nil
		},
	)
}
