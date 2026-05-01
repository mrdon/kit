package cards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

func buildCardsMCPTools(svc *CardService) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range cardsTools {
		handler := cardsMCPHandler(meta.Name, svc)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func cardsMCPHandler(name string, svc *CardService) mcpserver.ToolHandlerFunc {
	switch name {
	case "create_decision":
		return mcpCreateDecision(svc)
	case "create_briefing":
		return mcpCreateBriefing(svc)
	case "update_decision":
		return mcpUpdateDecision(svc)
	case "update_briefing":
		return mcpUpdateBriefing(svc)
	case "list_decisions":
		return mcpListDecisions(svc)
	case "list_briefings":
		return mcpListBriefings(svc)
	case "ack_briefing":
		return mcpAckBriefing(svc)
	case "resolve_decision":
		return mcpResolveDecision(svc)
	default:
		return nil
	}
}

func mcpCreateDecision(svc *CardService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		title, _ := req.RequireString("title")
		body, _ := req.RequireString("context")
		priority := req.GetString("priority", "")
		recommended := req.GetString("recommended_option_id", "")
		options, err := parseOptions(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		roleScopes := parseStringArray(req, "role_scopes")
		card, err := svc.CreateDecision(ctx, caller, CardCreateInput{
			Title:      title,
			Body:       body,
			RoleScopes: roleScopes,
			Decision: &DecisionCreateInput{
				Priority:            DecisionPriority(priority),
				RecommendedOptionID: recommended,
				Options:             options,
			},
		})
		if err != nil {
			return mcpErrResult(err)
		}
		return mcp.NewToolResultText(fmt.Sprintf("Created decision [%s]: %s (%d options)", card.ID, card.Title, len(card.Decision.Options))), nil
	})
}

func mcpCreateBriefing(svc *CardService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		title, _ := req.RequireString("title")
		body, _ := req.RequireString("body")
		severity := req.GetString("severity", "")
		roleScopes := parseStringArray(req, "role_scopes")
		card, err := svc.CreateBriefing(ctx, caller, CardCreateInput{
			Title:      title,
			Body:       body,
			RoleScopes: roleScopes,
			Briefing:   &BriefingCreateInput{Severity: BriefingSeverity(severity)},
		})
		if err != nil {
			return mcpErrResult(err)
		}
		return mcp.NewToolResultText(fmt.Sprintf("Created briefing [%s]: %s", card.ID, card.Title)), nil
	})
}

func mcpUpdateDecision(svc *CardService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("card_id")
		cardID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid card_id."), nil
		}
		args := req.GetArguments()
		u := CardUpdates{}
		if v, ok := args["title"].(string); ok {
			u.Title = &v
		}
		if v, ok := args["context"].(string); ok {
			u.Body = &v
		}
		if v, ok := args["state"].(string); ok {
			s := CardState(v)
			u.State = &s
		}
		if v, ok := args["role_scopes"].([]any); ok {
			arr := toStringSlice(v)
			u.RoleScopes = &arr
		}

		if v, ok := args["priority"].(string); ok {
			p := DecisionPriority(v)
			if u.Decision == nil {
				u.Decision = &DecisionUpdates{}
			}
			u.Decision.Priority = &p
		}
		if v, ok := args["recommended_option_id"].(string); ok {
			if u.Decision == nil {
				u.Decision = &DecisionUpdates{}
			}
			u.Decision.RecommendedOptionID = &v
		}
		if _, ok := args["options"]; ok {
			opts, err := parseOptions(req)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if u.Decision == nil {
				u.Decision = &DecisionUpdates{}
			}
			u.Decision.Options = &opts
		}

		card, err := svc.Update(ctx, caller, cardID, u)
		if err != nil {
			return mcpErrResult(err)
		}
		return mcp.NewToolResultText(fmt.Sprintf("Updated decision [%s]: %s", card.ID, card.Title)), nil
	})
}

func mcpUpdateBriefing(svc *CardService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("card_id")
		cardID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid card_id."), nil
		}
		args := req.GetArguments()
		u := CardUpdates{}
		if v, ok := args["title"].(string); ok {
			u.Title = &v
		}
		if v, ok := args["body"].(string); ok {
			u.Body = &v
		}
		if v, ok := args["state"].(string); ok {
			s := CardState(v)
			u.State = &s
		}
		if v, ok := args["severity"].(string); ok {
			sev := BriefingSeverity(v)
			u.Briefing = &BriefingUpdates{Severity: &sev}
		}
		if v, ok := args["role_scopes"].([]any); ok {
			arr := toStringSlice(v)
			u.RoleScopes = &arr
		}
		card, err := svc.Update(ctx, caller, cardID, u)
		if err != nil {
			return mcpErrResult(err)
		}
		return mcp.NewToolResultText(fmt.Sprintf("Updated briefing [%s]: %s", card.ID, card.Title)), nil
	})
}

func mcpListDecisions(svc *CardService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		cards, err := svc.ListDecisions(ctx, caller, CardFilters{
			State:    CardState(req.GetString("state", "")),
			Priority: DecisionPriority(req.GetString("priority", "")),
		})
		if err != nil {
			return mcpErrResult(err)
		}
		return mcp.NewToolResultText(formatCardList(cards, "decisions")), nil
	})
}

func mcpListBriefings(svc *CardService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		cards, err := svc.ListBriefings(ctx, caller, CardFilters{
			State:    CardState(req.GetString("state", "")),
			Severity: BriefingSeverity(req.GetString("severity", "")),
		})
		if err != nil {
			return mcpErrResult(err)
		}
		return mcp.NewToolResultText(formatCardList(cards, "briefings")), nil
	})
}

func mcpAckBriefing(svc *CardService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("card_id")
		cardID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid card_id."), nil
		}
		kindStr, _ := req.RequireString("kind")
		card, err := svc.AckBriefing(ctx, caller, cardID, BriefingAckKind(kindStr))
		if err != nil {
			if errors.Is(err, ErrAlreadyTerminal) {
				return mcp.NewToolResultError("Briefing already acknowledged."), nil
			}
			return mcpErrResult(err)
		}
		return mcp.NewToolResultText(fmt.Sprintf("Briefing [%s] marked %s.", card.ID, card.State)), nil
	})
}

func mcpResolveDecision(svc *CardService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("card_id")
		cardID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid card_id."), nil
		}
		optionID := req.GetString("option_id", "")

		slackClient, err := slackClientForCaller(ctx, svc, caller)
		if err != nil {
			return mcp.NewToolResultError("Cannot open Slack: " + err.Error()), nil
		}
		card, err := svc.ResolveDecision(ctx, caller, cardID, optionID, slackClient)
		if err != nil {
			if errors.Is(err, ErrAlreadyTerminal) {
				return mcp.NewToolResultError("Decision already resolved."), nil
			}
			if errors.Is(err, ErrOptionNotFound) {
				return mcp.NewToolResultError("Option not found on this decision."), nil
			}
			if errors.Is(err, ErrNoOptionPicked) {
				return mcp.NewToolResultError("No option chosen and no recommended option is set."), nil
			}
			return mcpErrResult(err)
		}
		msg := fmt.Sprintf("Decision [%s] resolved with option %q.", card.ID, card.Decision.ResolvedOptionID)
		if card.Decision.ResolvedJobID != nil {
			msg += fmt.Sprintf(" Kit queued task %s.", *card.Decision.ResolvedJobID)
		}
		return mcp.NewToolResultText(msg), nil
	})
}

// slackClientForCaller fetches the tenant's bot token, decrypts it, and
// builds a Slack client. Needed because MCP (and the HTTP layer later) lack
// the per-session slack client that Slack-originated agent calls have.
func slackClientForCaller(ctx context.Context, svc *CardService, caller *services.Caller) (*kitslack.Client, error) {
	if svc.enc == nil {
		return nil, errors.New("encryptor not wired (server misconfiguration)")
	}
	tenant, err := models.GetTenantByID(ctx, svc.pool, caller.TenantID)
	if err != nil {
		return nil, fmt.Errorf("looking up tenant: %w", err)
	}
	if tenant == nil {
		return nil, errors.New("tenant not found")
	}
	token, err := svc.enc.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting bot token: %w", err)
	}
	return kitslack.NewClient(token), nil
}

// parseOptions pulls the options array from the MCP request. The MCP SDK's
// GetArguments() returns map[string]any where arrays are []any; each entry
// is marshalled back to JSON and decoded into DecisionOption.
func parseOptions(req mcp.CallToolRequest) ([]DecisionOption, error) {
	args := req.GetArguments()
	raw, ok := args["options"].([]any)
	if !ok {
		return nil, errors.New("options must be an array")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("options: %w", err)
	}
	var opts []DecisionOption
	if err := json.Unmarshal(b, &opts); err != nil {
		return nil, fmt.Errorf("options: %w", err)
	}
	return opts, nil
}

func parseStringArray(req mcp.CallToolRequest, key string) []string {
	args := req.GetArguments()
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	return toStringSlice(raw)
}

func toStringSlice(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mcpErrResult(err error) (*mcp.CallToolResult, error) {
	if errors.Is(err, services.ErrForbidden) {
		return mcp.NewToolResultError("Permission denied."), nil
	}
	if errors.Is(err, services.ErrNotFound) || errors.Is(err, ErrCardNotFound) {
		return mcp.NewToolResultError("Card not found."), nil
	}
	return mcp.NewToolResultError(err.Error()), nil
}
