package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func taskMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services) mcpserver.ToolHandlerFunc {
	switch name {
	case "create_task":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			desc, _ := req.RequireString("description")
			cronExpr := req.GetString("cron_expr", "")
			channelID := req.GetString("channel_id", "")
			scope := req.GetString("scope", "user")

			if cronExpr == "" {
				return mcp.NewToolResultError("cron_expr is required for MCP task creation."), nil
			}

			task, err := svc.Tasks.Create(ctx, caller, desc, cronExpr, caller.Timezone, channelID, scope, false, nil)
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Insufficient permissions for this scope."), nil
			}
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Task created (ID: %s). Next run: %s",
				task.ID, task.NextRunAt.In(caller.Location()).Format("Mon Jan 2 3:04 PM MST"))), nil
		})
	case "list_tasks":
		return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			tasks, err := svc.Tasks.List(ctx, caller)
			if err != nil {
				return nil, err
			}
			if len(tasks) == 0 {
				return mcp.NewToolResultText("No scheduled tasks."), nil
			}
			var b strings.Builder
			for _, t := range tasks {
				status := t.Status
				if t.LastError != nil {
					status += " (error: " + *t.LastError + ")"
				}
				schedule := "cron: " + t.CronExpr
				if t.RunOnce {
					schedule = "one-time"
				}
				fmt.Fprintf(&b, "- [%s] %s | %s | next: %s | status: %s\n",
					t.ID, t.Description, schedule, t.NextRunAt.In(caller.Location()).Format("Mon Jan 2 3:04 PM MST"), status)
			}
			return mcp.NewToolResultText(b.String()), nil
		})
	case "delete_task":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("task_id")
			taskID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid task ID."), nil
			}
			err = svc.Tasks.Delete(ctx, caller, taskID)
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Task not found."), nil
			}
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Task deleted."), nil
		})
	default:
		return nil
	}
}
