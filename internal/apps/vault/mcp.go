package vault

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// buildVaultMCPTools mirrors the agent registration: same metadata, same
// service calls, surface-specific I/O. Per CLAUDE.md "agent and MCP tool
// parity" — both must be updated in the same commit.
func buildVaultMCPTools(svc *Service) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range vaultToolMetas {
		h := mcpHandlerFor(meta.Name, svc)
		if h == nil {
			continue
		}
		out = append(out, apps.MCPToolFromMeta(meta, h))
	}
	return out
}

func mcpHandlerFor(name string, svc *Service) mcpserver.ToolHandlerFunc {
	switch name {
	case "list_secrets":
		return mcpListSecrets(svc)
	case "find_secret":
		return mcpFindSecret(svc)
	case "view_secret":
		return mcpViewSecret(svc)
	case "start_add_secret":
		return mcpStartAddSecret(svc)
	case "update_secret_scopes", "delete_secret":
		// Agent path runs these through PolicyGate, which mints a
		// decision card a human approves in the swipe stack before the
		// tool executes. The MCP path has no equivalent enforced gate
		// today (the require_approval flag is opt-in, not mandatory),
		// so an MCP harness operator could otherwise wholesale delete
		// or rescope entries in one call. Until MCP gets a forced-gate
		// wrapper, refuse here and point the caller at the surfaces
		// that do enforce approval. CLAUDE.md gated-tool rule: "tool
		// handler [is] the only entry point to the underlying
		// dangerous operation."
		return mcpRefuseGated(name)
	}
	return nil
}

func mcpRefuseGated(name string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError(fmt.Sprintf(
			"%s is not available via MCP — it requires human approval through a decision card. "+
				"Ask Kit (the chat agent) to run it, or use the web UI.", name,
		)), nil
	}
}

func mcpAudit(svc *Service, caller *services.Caller) auditCtx {
	id := caller.UserID
	return auditCtx{
		pool:      svc.pool,
		tenantID:  caller.TenantID,
		actorID:   &id,
		userAgent: "mcp",
	}
}

func mcpListSecrets(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		q := req.GetString("q", "")
		tag := req.GetString("tag", "")
		limit := req.GetInt("limit", 50)
		rows, err := svc.ListEntries(ctx, caller, q, tag, limit)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(formatEntryList(caller, rows)), nil
	})
}

func mcpFindSecret(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		q, err := req.RequireString("q")
		if err != nil {
			return mcp.NewToolResultError("q is required"), nil
		}
		rows, err := svc.ListEntries(ctx, caller, q, "", 5)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(formatEntryList(caller, rows)), nil
	})
}

func mcpViewSecret(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("id")
		entryID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("invalid id"), nil
		}
		// Authz check; never returns ciphertext to MCP.
		_, err = svc.GetEntry(ctx, caller, entryID, mcpAudit(svc, caller))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				return mcp.NewToolResultError("not found or no access"), nil
			}
			return nil, err
		}
		// Caller's tenant slug isn't on the Caller struct; mcpauth-derived
		// callers should resolve it from the tenant model. Use the
		// service to do the lookup.
		slug, err := svc.tenantSlug(ctx, caller.TenantID)
		if err != nil || slug == "" {
			return mcp.NewToolResultError("could not build reveal URL"), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Reveal URL: /%s/apps/vault/reveal/%s", slug, entryID)), nil
	})
}

func mcpStartAddSecret(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		title := req.GetString("title", "")
		url := req.GetString("url", "")
		slug, err := svc.tenantSlug(ctx, caller.TenantID)
		if err != nil || slug == "" {
			return mcp.NewToolResultError("could not build add URL"), nil
		}
		out := fmt.Sprintf("/%s/apps/vault/add", slug)
		params := []string{}
		if title != "" {
			params = append(params, "title="+queryEscape(title))
		}
		if url != "" {
			params = append(params, "url="+queryEscape(url))
		}
		if len(params) > 0 {
			out += "?" + strings.Join(params, "&")
		}
		return mcp.NewToolResultText("Add URL: " + out), nil
	})
}
