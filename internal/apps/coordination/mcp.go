package coordination

import (
	"context"
	"encoding/json"
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

func buildCoordinationMCPTools(svc *Service) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range coordinationTools {
		handler := coordinationMCPHandler(meta.Name, svc)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func coordinationMCPHandler(name string, svc *Service) mcpserver.ToolHandlerFunc {
	switch name {
	case "start_coordination":
		return mcpStartCoordination(svc)
	case "list_coordinations":
		return mcpListCoordinations(svc)
	case "get_coordination":
		return mcpGetCoordination(svc)
	case "cancel_coordination":
		return mcpCancelCoordination(svc)
	}
	return nil
}

func mcpStartCoordination(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		args := req.GetArguments()

		title, _ := req.RequireString("title")
		duration := req.GetInt("duration_minutes", 0)
		startStr, _ := req.RequireString("start_date")
		endStr, _ := req.RequireString("end_date")
		notes := req.GetString("notes", "")
		autoApprove := req.GetBool("auto_approve", false)
		deadlineDays := req.GetInt("deadline_days", 0)
		organizerTZ := req.GetString("organizer_tz", "")

		participantsRaw, _ := args["participants"].([]any)
		participants := make([]string, 0, len(participantsRaw))
		for _, p := range participantsRaw {
			if s, ok := p.(string); ok {
				participants = append(participants, s)
			}
		}

		slotsRaw, _ := args["candidate_slots"].([]any)
		slots, err := parseSlotsFromMCP(slotsRaw)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		startDate, err := parseISODate(startStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start_date: %v", err)), nil
		}
		endDate, err := parseISODate(endStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid end_date: %v", err)), nil
		}

		coord, err := svc.Start(ctx, caller, StartInput{
			Title:           title,
			DurationMinutes: duration,
			StartDate:       startDate,
			EndDate:         endDate,
			CandidateSlots:  slots,
			Participants:    participants,
			Notes:           notes,
			AutoApprove:     autoApprove,
			DeadlineDays:    deadlineDays,
			OrganizerTZ:     organizerTZ,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Coordination started: id=%s, %d participants.", coord.ID, len(participants))), nil
	})
}

func mcpListCoordinations(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		limit := req.GetInt("limit", 25)
		coords, err := svc.ListForCaller(ctx, caller, limit)
		if err != nil {
			return nil, err
		}
		if len(coords) == 0 {
			return mcp.NewToolResultText("No coordinations."), nil
		}
		var b strings.Builder
		for _, c := range coords {
			fmt.Fprintf(&b, "- %s [%s] %q (%s)\n",
				c.ID, c.Status, c.Config.Title, c.CreatedAt.Format(time.RFC3339))
		}
		return mcp.NewToolResultText(b.String()), nil
	})
}

func mcpGetCoordination(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("coordination_id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid coordination_id: %v", err)), nil
		}
		st, err := svc.GetStatus(ctx, caller, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatStatus(st)), nil
	})
}

func mcpCancelCoordination(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("coordination_id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid coordination_id: %v", err)), nil
		}
		if err := svc.Cancel(ctx, caller, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Coordination cancelled."), nil
	})
}

func parseSlotsFromMCP(raw []any) ([]Slot, error) {
	out := make([]Slot, 0, len(raw))
	for i, r := range raw {
		// Each entry can come in as a JSON object (map[string]any) or a
		// pre-marshaled string.
		switch v := r.(type) {
		case map[string]any:
			startStr, _ := v["start"].(string)
			endStr, _ := v["end"].(string)
			start, err := parseFlexibleTimestamp(startStr)
			if err != nil {
				return nil, fmt.Errorf("slot %d start: %w", i, err)
			}
			end, err := parseFlexibleTimestamp(endStr)
			if err != nil {
				return nil, fmt.Errorf("slot %d end: %w", i, err)
			}
			out = append(out, Slot{Start: start, End: end})
		case string:
			var s slotInput
			if err := json.Unmarshal([]byte(v), &s); err != nil {
				return nil, fmt.Errorf("slot %d: %w", i, err)
			}
			start, err := parseFlexibleTimestamp(s.Start)
			if err != nil {
				return nil, fmt.Errorf("slot %d start: %w", i, err)
			}
			end, err := parseFlexibleTimestamp(s.End)
			if err != nil {
				return nil, fmt.Errorf("slot %d end: %w", i, err)
			}
			out = append(out, Slot{Start: start, End: end})
		default:
			return nil, fmt.Errorf("slot %d: unexpected type %T", i, r)
		}
	}
	return out, nil
}
