package vault

import (
	"context"
	"errors"
	"fmt"

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
	case "set_secret_role", "delete_secret":
		// Agent path runs these through PolicyGate, which mints a
		// decision card a human approves in the swipe stack before the
		// tool executes. The MCP path has no equivalent enforced gate
		// today (the require_approval flag is opt-in, not mandatory),
		// so an MCP harness operator could otherwise wholesale delete
		// or rescope entries in one call. Until MCP gets a forced-gate
		// wrapper, refuse here and point the caller at the surfaces
		// that do enforce approval.
		return mcpRefuseGated(name)
	case "setup_vault":
		return mcpSetupVault(svc)
	case "rotate_vault_password":
		return mcpRotateVaultPassword(svc)
	case "nuke_vault":
		return mcpNukeVault(svc)
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
		var roleID *uuid.UUID
		if rs := req.GetString("role_id", ""); rs != "" {
			rid, err := uuid.Parse(rs)
			if err != nil {
				return mcp.NewToolResultError("invalid role_id"), nil
			}
			roleID = &rid
		}
		rows, err := svc.ListEntries(ctx, caller, q, tag, roleID, limit)
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
		rows, err := svc.ListEntries(ctx, caller, q, "", nil, 5)
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
		return mcp.NewToolResultText("Reveal URL: " + svc.absURL(fmt.Sprintf("/%s/web/vault/%s", slug, entryID))), nil
	})
}

// adminVaultURL is the common pattern for the admin-only URL-returning
// tools (setup / rotate / nuke). Returns a clean error string when the
// caller isn't admin, since MCP tool results need to be user-friendly
// strings rather than Go errors.
func adminVaultURL(svc *Service, action string) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		if !caller.IsAdmin {
			return mcp.NewToolResultError("only admins can " + action + " the vault"), nil
		}
		slug, err := svc.tenantSlug(ctx, caller.TenantID)
		if err != nil || slug == "" {
			return mcp.NewToolResultError("could not build " + action + " URL"), nil
		}
		return mcp.NewToolResultText("Open in your browser: " + svc.absURL(fmt.Sprintf("/%s/web/vault", slug))), nil
	})
}

func mcpSetupVault(svc *Service) mcpserver.ToolHandlerFunc {
	return adminVaultURL(svc, "setup")
}

func mcpRotateVaultPassword(svc *Service) mcpserver.ToolHandlerFunc {
	return adminVaultURL(svc, "rotate")
}

func mcpNukeVault(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		if !caller.IsAdmin {
			return mcp.NewToolResultError("only admins can destroy the vault"), nil
		}
		slug, err := svc.tenantSlug(ctx, caller.TenantID)
		if err != nil || slug == "" {
			return mcp.NewToolResultError("could not build nuke URL"), nil
		}
		// Lead with the warning so a careless MCP-driven workflow surfaces
		// the destructiveness before the link.
		body := "**WARNING:** opening this URL will permanently delete every secret in the tenant vault. There is no undo. Use only if the master password is unrecoverable.\n\n"
		body += svc.absURL(fmt.Sprintf("/%s/web/vault", slug))
		return mcp.NewToolResultText(body), nil
	})
}

func mcpStartAddSecret(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		slug, err := svc.tenantSlug(ctx, caller.TenantID)
		if err != nil || slug == "" {
			return mcp.NewToolResultError("could not build add URL"), nil
		}
		return mcp.NewToolResultText("Add URL: " + svc.absURL(fmt.Sprintf("/%s/web/vault", slug))), nil
	})
}
