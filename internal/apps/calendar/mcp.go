package calendar

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/services"
)

func buildCalendarMCPTools(svc *CalendarService, caller *services.Caller) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range calendarTools {
		if meta.AdminOnly && !caller.IsAdmin {
			continue
		}
		handler := calendarMCPHandler(meta.Name, svc, caller)
		if handler == nil {
			continue
		}
		out = append(out, apps.MCPToolFromMeta(meta, handler))
	}
	return out
}

func calendarMCPHandler(name string, svc *CalendarService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	switch name {
	case "configure_calendar":
		return mcpConfigureCalendar(svc, caller)
	case "delete_calendar":
		return mcpDeleteCalendar(svc, caller)
	case "list_calendars":
		return mcpListCalendars(svc, caller)
	case "get_calendar_events":
		return mcpGetCalendarEvents(svc, caller)
	default:
		return nil
	}
}

func mcpConfigureCalendar(svc *CalendarService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, _ := req.RequireString("name")
		rawURL, _ := req.RequireString("url")
		args := req.GetArguments()
		var roleScopes []string
		if rs, ok := args["role_scopes"].([]any); ok {
			for _, r := range rs {
				if s, ok := r.(string); ok {
					roleScopes = append(roleScopes, s)
				}
			}
		}
		cal, err := svc.Configure(ctx, caller, ConfigureOpts{
			Name:       name,
			URL:        rawURL,
			Timezone:   req.GetString("timezone", ""),
			RoleScopes: roleScopes,
		})
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Configured calendar " + cal.Name + " (" + cal.ID.String() + ")."), nil
	}
}

func mcpDeleteCalendar(svc *CalendarService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("calendar_id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid calendar_id."), nil
		}
		if err := svc.Delete(ctx, caller, id); err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Not found."), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Calendar deleted."), nil
	}
}

func mcpListCalendars(svc *CalendarService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cals, err := svc.List(ctx, caller)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(formatCalendarList(cals)), nil
	}
}

func mcpGetCalendarEvents(svc *CalendarService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		limit := 0
		if l, ok := args["limit"].(float64); ok {
			limit = int(l)
		}
		events, err := svc.GetEvents(ctx, caller, GetEventsOpts{
			CalendarID: req.GetString("calendar_id", ""),
			After:      req.GetString("after", ""),
			Before:     req.GetString("before", ""),
			Query:      req.GetString("query", ""),
			Limit:      limit,
		})
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("You don't have access to that calendar."), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatEvents(events, caller.Location())), nil
	}
}
