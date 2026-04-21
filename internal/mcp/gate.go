package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// CallerGateCreator is the minimal surface the MCP adapter needs to
// mint an approval card when a caller sets `require_approval: true` on
// a tool call. Implemented by cards.CardService so the MCP package
// doesn't pull in cards directly (keeps imports narrow).
type CallerGateCreator interface {
	CreateGateCardForCaller(
		ctx context.Context, c *services.Caller,
		toolName string, toolArguments json.RawMessage,
		preview tools.GateCardPreview,
	) (cardID uuid.UUID, cardURL string, err error)
}

// currentGateCreator is the process-wide caller-gate sink, wired once
// at startup from cmd/kit/main.go after the cards app initializes.
// Nil is tolerated: the middleware falls through to the inner handler
// with a user-facing error so misconfigured startups are visible.
var currentGateCreator CallerGateCreator

// SetGateCreator wires the gate-card creator used by the MCP
// require_approval middleware. Call once at startup.
func SetGateCreator(c CallerGateCreator) { currentGateCreator = c }

// gatedMCP wraps an MCP tool handler so that `require_approval: true`
// in the request creates an approval card via currentGateCreator and
// returns a HALTED text result instead of running the inner handler.
// When the flag is absent or false, the request falls through unchanged.
//
// The wrapper runs BEFORE the inner handler's caller-resolution path,
// pulling the caller from the HTTP request's auth context. If no caller
// is present (unauthenticated request), the wrapper defers to the
// inner handler so auth errors surface consistently.
//
// The preview is a plain empty GateCardPreview so the card service
// renders the generic "Run <tool>?" wording — MCP calls come from
// external harnesses without a registered per-tool preview.
func gatedMCP(toolName string, inner mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
	preview := tools.GateCardPreview{}
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !req.GetBool(services.RequireApprovalField, false) {
			return inner(ctx, req)
		}
		caller := auth.CallerFromContext(ctx)
		if caller == nil {
			// Let the inner handler surface its own auth error. Falling
			// through keeps behaviour identical to today when the flag
			// is absent.
			return inner(ctx, req)
		}
		if currentGateCreator == nil {
			return mcp.NewToolResultError("gating is not configured on this server; retry without require_approval"), nil
		}
		argsJSON, err := argsWithoutRequireApproval(req)
		if err != nil {
			return nil, fmt.Errorf("re-marshaling args for gate card: %w", err)
		}
		cardID, cardURL, err := currentGateCreator.CreateGateCardForCaller(ctx, caller, toolName, argsJSON, preview)
		if err != nil {
			return nil, fmt.Errorf("creating approval card for %q: %w", toolName, err)
		}
		urlClause := ""
		if cardURL != "" {
			urlClause = fmt.Sprintf(" Approve it here: %s.", cardURL)
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"%s%s requires human approval. Decision card %s was created.%s Do NOT tell the user the action happened; say it's queued for their review and share the approval URL if one is provided.",
			tools.HaltedPrefix, toolName, cardID, urlClause,
		)), nil
	}
}

// argsWithoutRequireApproval re-marshals the request's arguments with
// the require_approval field stripped so the card's tool_arguments
// match what a plain call would look like.
func argsWithoutRequireApproval(req mcp.CallToolRequest) (json.RawMessage, error) {
	// req.GetArguments returns the already-decoded arguments map. Clone
	// before deleting so we don't mutate the caller's view of the
	// request (mcp-go reuses the map in some code paths).
	args := req.GetArguments()
	if args == nil {
		return json.Marshal(map[string]any{})
	}
	cleaned := make(map[string]any, len(args))
	for k, v := range args {
		if k == services.RequireApprovalField {
			continue
		}
		cleaned[k] = v
	}
	return json.Marshal(cleaned)
}
