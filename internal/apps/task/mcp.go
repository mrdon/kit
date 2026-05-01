package task

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

func buildTaskMCPTools(svc *TaskService) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range taskTools {
		handler := taskMCPHandler(meta.Name, svc)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func taskMCPHandler(name string, svc *TaskService) mcpserver.ToolHandlerFunc {
	switch name {
	case "create_task":
		return mcpCreateTask(svc)
	case "list_tasks":
		return mcpListTasks(svc)
	case "get_task":
		return mcpGetTask(svc)
	case "update_task":
		return mcpUpdateTask(svc)
	case "add_task_comment":
		return mcpAddTaskComment(svc)
	case "complete_task":
		return mcpCompleteTask(svc)
	case "snooze_task":
		return mcpSnoozeTask(svc)
	default:
		return nil
	}
}

func mcpCreateTask(svc *TaskService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		title, _ := req.RequireString("title")
		assigneeRef := req.GetString("assignee", "")
		dueDateStr := req.GetString("due_date", "")

		in := CreateInput{
			Title:       title,
			Description: req.GetString("description", ""),
			Priority:    req.GetString("priority", ""),
			RoleName:    req.GetString("role_scope", ""),
		}

		if assigneeRef != "" {
			id, msg := svc.ResolveAssignee(ctx, caller, assigneeRef)
			if msg != "" {
				return mcp.NewToolResultError(msg), nil
			}
			in.AssigneeUserID = id
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
			if errors.Is(err, ErrPrimaryRoleNotSet) {
				return mcp.NewToolResultError(primaryRoleNotSetMessage(caller)), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			if errors.Is(err, ErrInvalidRole) {
				return mcp.NewToolResultError(fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", in.RoleName)), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText(fmt.Sprintf("Created task [%s]: %s", t.ID, t.Title)), nil
	})
}

func mcpListTasks(svc *TaskService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		args := req.GetArguments()

		f := TaskFilters{
			Status:   req.GetString("status", ""),
			Priority: req.GetString("priority", ""),
			RoleName: req.GetString("role_scope", ""),
			Search:   req.GetString("search", ""),
		}

		if b, ok := args["assigned_to_me"].(bool); ok {
			f.AssignedToMe = b
		}
		if b, ok := args["unassigned"].(bool); ok {
			f.Unassigned = b
		}
		if b, ok := args["overdue"].(bool); ok {
			f.Overdue = b
		}
		if v := req.GetString("assignee", ""); v != "" {
			id, msg := svc.ResolveAssignee(ctx, caller, v)
			if msg != "" {
				return mcp.NewToolResultError(msg), nil
			}
			f.AssigneeUserID = id
		}

		if cs := req.GetString("closed_since", ""); cs != "" {
			t, err := time.Parse("2006-01-02", cs)
			if err != nil {
				return mcp.NewToolResultError("Invalid closed_since format. Use YYYY-MM-DD."), nil
			}
			f.ClosedSince = &t
		}

		tasks, err := svc.List(ctx, caller, f)
		if err != nil {
			return nil, err
		}

		if len(tasks) == 0 {
			return mcp.NewToolResultText("No tasks found matching your filters."), nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d task(s):\n\n", len(tasks))
		for _, t := range tasks {
			b.WriteString(FormatTask(&t))
			b.WriteString("\n\n")
		}
		return mcp.NewToolResultText(b.String()), nil
	})
}

func mcpGetTask(svc *TaskService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("task_id")
		taskID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid task_id UUID."), nil
		}

		t, events, err := svc.Get(ctx, caller, taskID)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Task not found."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText(FormatTaskDetailed(t, events)), nil
	})
}

func mcpUpdateTask(svc *TaskService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("task_id")
		taskID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid task_id UUID."), nil
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
		if v := req.GetString("assignee", ""); v != "" {
			id, msg := svc.ResolveAssignee(ctx, caller, v)
			if msg != "" {
				return mcp.NewToolResultError(msg), nil
			}
			u.NewAssigneeUserID = id
		}
		if b, ok := args["clear_assignee"].(bool); ok && b {
			u.ClearAssignee = true
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
		if b, ok := args["clear_due_date"].(bool); ok && b {
			u.ClearDueDate = true
		}

		t, err := svc.Update(ctx, caller, taskID, u)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Task not found."), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			if errors.Is(err, ErrInvalidRole) {
				return mcp.NewToolResultError(fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", req.GetString("role_scope", ""))), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText("Updated task:\n" + FormatTask(t)), nil
	})
}

func mcpAddTaskComment(svc *TaskService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("task_id")
		content, _ := req.RequireString("content")

		taskID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid task_id UUID."), nil
		}

		if err := svc.AddComment(ctx, caller, taskID, content); err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Task not found."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText("Comment added."), nil
	})
}

func mcpCompleteTask(svc *TaskService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("task_id")
		taskID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid task_id UUID."), nil
		}

		t, err := svc.Complete(ctx, caller, taskID)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Task not found."), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText("Completed: " + t.Title), nil
	})
}

func mcpSnoozeTask(svc *TaskService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("task_id")
		taskID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid task_id UUID."), nil
		}

		args := req.GetArguments()
		var days int
		switch v := args["days"].(type) {
		case float64:
			days = int(v)
		case int:
			days = v
		}
		t, err := svc.SnoozeDays(ctx, caller, taskID, days)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Task not found."), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			if strings.Contains(err.Error(), "snooze days must be") {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return nil, err
		}

		return mcp.NewToolResultText(fmt.Sprintf("Snoozed %q for %d day(s). Visible again after %s.", t.Title, days, t.SnoozedUntil.Format("2006-01-02 15:04 MST"))), nil
	})
}
