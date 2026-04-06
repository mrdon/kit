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
	"github.com/mrdon/kit/internal/services"
)

func buildTodoMCPTools(svc *TodoService, caller *services.Caller) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range todoTools {
		if meta.AdminOnly && !caller.IsAdmin {
			continue
		}
		handler := todoMCPHandler(meta.Name, svc, caller)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func todoMCPHandler(name string, svc *TodoService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	switch name {
	case "create_todo":
		return mcpCreateTodo(svc, caller)
	case "list_todos":
		return mcpListTodos(svc, caller)
	case "get_todo":
		return mcpGetTodo(svc, caller)
	case "update_todo":
		return mcpUpdateTodo(svc, caller)
	case "add_todo_comment":
		return mcpAddTodoComment(svc, caller)
	case "complete_todo":
		return mcpCompleteTodo(svc, caller)
	default:
		return nil
	}
}

func mcpCreateTodo(svc *TodoService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title, _ := req.RequireString("title")
		desc := req.GetString("description", "")
		priority := req.GetString("priority", "")
		assignedToStr := req.GetString("assigned_to", "")
		roleScope := req.GetString("role_scope", "")
		dueDateStr := req.GetString("due_date", "")

		t := &Todo{
			Title:       title,
			Description: desc,
			Priority:    priority,
			RoleScope:   roleScope,
		}

		args := req.GetArguments()
		if p, ok := args["private"].(bool); ok {
			t.Private = p
		}

		if assignedToStr != "" {
			id, err := uuid.Parse(assignedToStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid assigned_to UUID."), nil
			}
			t.AssignedTo = &id
		}

		if dueDateStr != "" {
			d, err := time.Parse("2006-01-02", dueDateStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid due_date format. Use YYYY-MM-DD."), nil
			}
			t.DueDate = &d
		}

		if err := svc.Create(ctx, caller, t); err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText(fmt.Sprintf("Created todo [%s]: %s", t.ID, t.Title)), nil
	}
}

func mcpListTodos(svc *TodoService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()

		f := TodoFilters{
			Status:    req.GetString("status", ""),
			Priority:  req.GetString("priority", ""),
			RoleScope: req.GetString("role_scope", ""),
			Search:    req.GetString("search", ""),
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
	}
}

func mcpGetTodo(svc *TodoService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	}
}

func mcpUpdateTodo(svc *TodoService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("todo_id")
		todoID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid todo_id UUID."), nil
		}

		args := req.GetArguments()
		u := TodoUpdates{}

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
			id, err := uuid.Parse(v)
			if err != nil {
				return mcp.NewToolResultError("Invalid assigned_to UUID."), nil
			}
			u.AssignedTo = &id
		}
		if v := req.GetString("role_scope", ""); v != "" {
			u.RoleScope = &v
		}
		if v := req.GetString("due_date", ""); v != "" {
			d, err := time.Parse("2006-01-02", v)
			if err != nil {
				return mcp.NewToolResultError("Invalid due_date format. Use YYYY-MM-DD."), nil
			}
			u.DueDate = &d
		}
		if b, ok := args["private"].(bool); ok {
			u.Private = &b
		}

		t, err := svc.Update(ctx, caller, todoID, u)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Todo not found."), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText("Updated todo:\n" + FormatTodo(t)), nil
	}
}

func mcpAddTodoComment(svc *TodoService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	}
}

func mcpCompleteTodo(svc *TodoService, caller *services.Caller) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	}
}
