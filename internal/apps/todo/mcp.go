package todo

import (
	"context"
	"errors"
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

func buildTodoMCPTools(svc *TodoService) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range todoTools {
		handler := todoMCPHandler(meta.Name, svc)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func todoMCPHandler(name string, svc *TodoService) mcpserver.ToolHandlerFunc {
	switch name {
	case "create_todo":
		return mcpCreateTodo(svc)
	case "list_todos":
		return mcpListTodos(svc)
	case "get_todo":
		return mcpGetTodo(svc)
	case "update_todo":
		return mcpUpdateTodo(svc)
	case "add_todo_comment":
		return mcpAddTodoComment(svc)
	case "complete_todo":
		return mcpCompleteTodo(svc)
	case "snooze_todo":
		return mcpSnoozeTodo(svc)
	default:
		return nil
	}
}

func mcpCreateTodo(svc *TodoService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		title, _ := req.RequireString("title")
		assignedToStr := req.GetString("assigned_to", "")
		dueDateStr := req.GetString("due_date", "")

		in := CreateInput{
			Title:       title,
			Description: req.GetString("description", ""),
			Priority:    req.GetString("priority", ""),
			RoleName:    req.GetString("role_scope", ""),
			Visibility:  req.GetString("visibility", ""),
		}

		if assignedToStr != "" {
			id, msg := svc.ResolveAssignee(ctx, caller, assignedToStr)
			if msg != "" {
				return mcp.NewToolResultError(msg), nil
			}
			in.AssignedTo = id
		}

		if dueDateStr != "" {
			d, err := time.Parse("2006-01-02", dueDateStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid due_date format. Use YYYY-MM-DD."), nil
			}
			in.DueDate = &d
		}

		t, err := svc.Create(ctx, caller, in)
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			if errors.Is(err, ErrInvalidRole) {
				return mcp.NewToolResultError(fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", in.RoleName)), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText(fmt.Sprintf("Created todo [%s]: %s", t.ID, t.Title)), nil
	})
}

func mcpListTodos(svc *TodoService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		args := req.GetArguments()

		f := TodoFilters{
			Status:   req.GetString("status", ""),
			Priority: req.GetString("priority", ""),
			RoleName: req.GetString("role_scope", ""),
			Search:   req.GetString("search", ""),
		}

		if b, ok := args["assigned_to_me"].(bool); ok {
			f.AssignedToMe = b
		}
		if b, ok := args["overdue"].(bool); ok {
			f.Overdue = b
		}

		if cs := req.GetString("closed_since", ""); cs != "" {
			t, err := time.Parse("2006-01-02", cs)
			if err != nil {
				return mcp.NewToolResultError("Invalid closed_since format. Use YYYY-MM-DD."), nil
			}
			f.ClosedSince = &t
		}

		todos, err := svc.List(ctx, caller, f)
		if err != nil {
			return nil, err
		}

		if len(todos) == 0 {
			return mcp.NewToolResultText("No todos found matching your filters."), nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d todo(s):\n\n", len(todos))
		for _, t := range todos {
			b.WriteString(FormatTodo(&t))
			b.WriteString("\n\n")
		}
		return mcp.NewToolResultText(b.String()), nil
	})
}

func mcpGetTodo(svc *TodoService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("todo_id")
		todoID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid todo_id UUID."), nil
		}

		t, events, err := svc.Get(ctx, caller, todoID)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Todo not found."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText(FormatTodoDetailed(t, events)), nil
	})
}

func mcpUpdateTodo(svc *TodoService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("todo_id")
		todoID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid todo_id UUID."), nil
		}

		args := req.GetArguments()
		u := UpdateInput{}

		if v := req.GetString("title", ""); v != "" {
			u.Title = &v
		}
		if v := req.GetString("description", ""); v != "" {
			u.Description = &v
		}
		if v := req.GetString("status", ""); v != "" {
			u.Status = &v
		}
		if v := req.GetString("priority", ""); v != "" {
			u.Priority = &v
		}
		if v := req.GetString("blocked_reason", ""); v != "" {
			u.BlockedReason = &v
		}
		if v := req.GetString("assigned_to", ""); v != "" {
			id, msg := svc.ResolveAssignee(ctx, caller, v)
			if msg != "" {
				return mcp.NewToolResultError(msg), nil
			}
			u.NewAssignee = id
		}
		if _, present := args["role_scope"]; present {
			v := req.GetString("role_scope", "")
			u.NewRoleName = &v
		}
		if v := req.GetString("due_date", ""); v != "" {
			d, err := time.Parse("2006-01-02", v)
			if err != nil {
				return mcp.NewToolResultError("Invalid due_date format. Use YYYY-MM-DD."), nil
			}
			u.DueDate = &d
		}
		if v := req.GetString("visibility", ""); v != "" {
			u.Visibility = &v
		}

		t, err := svc.Update(ctx, caller, todoID, u)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Todo not found."), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			if errors.Is(err, ErrInvalidRole) {
				return mcp.NewToolResultError(fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", req.GetString("role_scope", ""))), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText("Updated todo:\n" + FormatTodo(t)), nil
	})
}

func mcpAddTodoComment(svc *TodoService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("todo_id")
		content, _ := req.RequireString("content")

		todoID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid todo_id UUID."), nil
		}

		if err := svc.AddComment(ctx, caller, todoID, content); err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Todo not found."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText("Comment added."), nil
	})
}

func mcpCompleteTodo(svc *TodoService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("todo_id")
		todoID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid todo_id UUID."), nil
		}

		t, err := svc.Complete(ctx, caller, todoID)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Todo not found."), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText("Completed: " + t.Title), nil
	})
}

func mcpSnoozeTodo(svc *TodoService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("todo_id")
		todoID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid todo_id UUID."), nil
		}

		args := req.GetArguments()
		var days int
		switch v := args["days"].(type) {
		case float64:
			days = int(v)
		case int:
			days = v
		}
		until, err := SnoozeDaysToUntil(days)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		t, err := svc.Snooze(ctx, caller, todoID, until)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Todo not found."), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText(fmt.Sprintf("Snoozed %q for %d day(s). Visible again after %s.", t.Title, days, until.Format("2006-01-02 15:04"))), nil
	})
}
