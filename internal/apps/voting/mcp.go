package voting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func buildVotingMCPTools(svc *Service) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range votingTools {
		handler := votingMCPHandler(meta.Name, svc)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func votingMCPHandler(name string, svc *Service) mcpserver.ToolHandlerFunc {
	switch name {
	case "start_vote":
		return mcpStartVote(svc)
	case "list_votes":
		return mcpListVotes(svc)
	case "get_vote":
		return mcpGetVote(svc)
	case "cancel_vote":
		return mcpCancelVote(svc)
	}
	return nil
}

func mcpStartVote(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		args := req.GetArguments()

		title, _ := req.RequireString("title")
		proposal, _ := req.RequireString("proposal_text")
		contextNotes := req.GetString("context_notes", "")
		deadlineHours := req.GetInt("deadline_hours", 0)

		participantsRaw, _ := args["participants"].([]any)
		participants := make([]string, 0, len(participantsRaw))
		for _, p := range participantsRaw {
			if s, ok := p.(string); ok {
				participants = append(participants, s)
			}
		}

		v, err := svc.StartVote(ctx, caller, StartVoteInput{
			Title:         title,
			ProposalText:  proposal,
			ContextNotes:  contextNotes,
			Participants:  participants,
			DeadlineHours: deadlineHours,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Vote started: id=%s, %d participants.", v.ID, len(participants))), nil
	})
}

func mcpListVotes(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		limit := req.GetInt("limit", 25)
		votes, err := svc.ListForCaller(ctx, caller, limit)
		if err != nil {
			return nil, err
		}
		if len(votes) == 0 {
			return mcp.NewToolResultText("No votes."), nil
		}
		var b strings.Builder
		for _, v := range votes {
			fmt.Fprintf(&b, "- %s [%s] %q (%s)\n",
				v.ID, v.Status, v.Title, v.CreatedAt.Format(time.RFC3339))
		}
		return mcp.NewToolResultText(b.String()), nil
	})
}

func mcpGetVote(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("vote_id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid vote_id: %v", err)), nil
		}
		st, err := svc.GetStatus(ctx, caller, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatStatus(st)), nil
	})
}

func mcpCancelVote(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("vote_id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid vote_id: %v", err)), nil
		}
		if err := svc.Cancel(ctx, caller, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Vote cancelled."), nil
	})
}
