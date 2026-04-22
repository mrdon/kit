package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools"
)

func taskMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services, llm *anthropic.Client) mcpserver.ToolHandlerFunc {
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

			policy, perr := policyFromMCP(req)
			if perr != "" {
				return mcp.NewToolResultError(perr), nil
			}

			model := tools.ClassifyTaskModel(ctx, llm, desc)
			task, err := svc.Tasks.Create(ctx, caller, services.CreateInput{
				Description: desc,
				CronExpr:    cronExpr,
				Timezone:    caller.Timezone,
				ChannelID:   channelID,
				Scope:       scope,
				Model:       model,
				Policy:      policy,
			})
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Insufficient permissions for this scope."), nil
			}
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Task created (ID: %s, model: %s). Next run: %s",
				task.ID, task.Model, task.NextRunAt.In(caller.Location()).Format("Mon Jan 2 3:04 PM MST"))), nil
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
				status := string(t.Status)
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
	case "update_task":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("task_id")
			taskID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid task ID."), nil
			}
			if req.GetBool("delete", false) {
				err = svc.Tasks.Delete(ctx, caller, taskID)
				if errors.Is(err, services.ErrNotFound) {
					return mcp.NewToolResultError("Task not found."), nil
				}
				if err != nil {
					return nil, err
				}
				return mcp.NewToolResultText("Task deleted."), nil
			}
			desc := req.GetString("description", "")
			policy, perr := policyFromMCP(req)
			if perr != "" {
				return mcp.NewToolResultError(perr), nil
			}
			update := services.UpdateInput{}
			if desc != "" {
				update.Description = &desc
			}
			if policy != nil {
				update.Policy = policy
			}
			if update.Description == nil && update.Policy == nil {
				return mcp.NewToolResultError("Provide description, policy, or delete=true."), nil
			}
			err = svc.Tasks.Update(ctx, caller, taskID, update)
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("Task not found."), nil
			}
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Task updated."), nil
		})
	default:
		return nil
	}
}

// buildRunTaskTool creates the run_task MCP tool, which needs agent + encryptor
// beyond the standard handler signature.
func buildRunTaskTool(pool *pgxpool.Pool, svc *services.Services, a *agent.Agent, enc *crypto.Encryptor, sched *scheduler.Scheduler) mcpserver.ServerTool {
	schema := services.PropsReq(map[string]any{
		"task_id": services.Field("string", "The task UUID to run"),
		"dry_run": services.Field("boolean", "If true, capture messages instead of posting to Slack"),
	}, "task_id")
	schemaJSON, _ := json.Marshal(schema)
	tool := mcp.NewToolWithRawSchema("run_task", "Run a task immediately for testing. In dry_run mode, messages are captured and returned instead of posted to Slack. You can only run tasks you created.", schemaJSON)

	handler := mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("task_id")
		taskID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid task ID."), nil
		}
		dryRun := req.GetBool("dry_run", false)

		task, err := models.GetTask(ctx, pool, caller.TenantID, taskID)
		if err != nil {
			return nil, fmt.Errorf("getting task: %w", err)
		}
		if task == nil {
			return mcp.NewToolResultError("Task not found."), nil
		}

		// run_task acts as the task's creator — the scheduled agent loads
		// their integrations, memories, and email credentials. Admins don't
		// get to stand in for another user; cross-user debugging is an
		// operator/SRE concern, handled via DB/CLI, not the customer MCP.
		if task.CreatedBy != caller.UserID {
			return mcp.NewToolResultError("You can only run tasks you created."), nil
		}

		// Builtin tasks run native code, not the LLM agent
		if task.TaskType == models.TaskTypeBuiltin {
			if dryRun {
				return mcp.NewToolResultText(fmt.Sprintf("Dry run: builtin task %q would execute native handler.", task.Description)), nil
			}
			sched.ExecuteBuiltinTask(ctx, *task)
			return mcp.NewToolResultText(fmt.Sprintf("Builtin task %q executed.", task.Description)), nil
		}

		tenant, err := models.GetTenantByID(ctx, pool, task.TenantID)
		if err != nil || tenant == nil {
			return mcp.NewToolResultError("Tenant not found."), nil
		}

		botToken, err := enc.Decrypt(tenant.BotToken)
		if err != nil {
			return nil, fmt.Errorf("decrypting bot token: %w", err)
		}

		var slack *kitslack.Client
		if dryRun {
			slack = kitslack.NewDryRunClient(botToken)
		} else {
			slack = kitslack.NewClient(botToken)
		}

		user, err := models.GetUserByID(ctx, pool, tenant.ID, task.CreatedBy)
		if err != nil || user == nil {
			return mcp.NewToolResultError("Task author not found."), nil
		}

		authorName := user.SlackUserID
		if user.DisplayName != nil && *user.DisplayName != "" {
			authorName = *user.DisplayName
		}
		policy, perr := models.ParseConfigPolicy(task.Config)
		if perr != nil {
			return nil, fmt.Errorf("parsing task policy: %w", perr)
		}
		tc := &agent.TaskContext{
			ID:            task.ID,
			Description:   task.Description,
			AuthorSlackID: user.SlackUserID,
			AuthorName:    authorName,
			Policy:        policy,
		}

		threadTS := fmt.Sprintf("task-%s-%d", task.ID, time.Now().UnixMilli())
		session, err := models.CreateSession(ctx, pool, tenant.ID, task.ChannelID, threadTS, user.ID, true)
		if err != nil {
			return nil, fmt.Errorf("creating session: %w", err)
		}

		runErr := a.Run(ctx, agent.RunInput{
			Slack:    slack,
			Tenant:   tenant,
			User:     user,
			Session:  session,
			Channel:  task.ChannelID,
			UserText: task.Description,
			Task:     tc,
			Model:    task.Model,
		})

		if dryRun {
			var b strings.Builder
			fmt.Fprintf(&b, "Dry run complete for task %s\n\n", task.ID)
			if len(slack.Captured) == 0 {
				b.WriteString("No messages were sent.\n")
			} else {
				for i, msg := range slack.Captured {
					fmt.Fprintf(&b, "--- Message %d ---\n", i+1)
					fmt.Fprintf(&b, "Channel: %s\n", msg.Channel)
					if msg.ThreadTS != "" {
						fmt.Fprintf(&b, "Thread: %s\n", msg.ThreadTS)
					}
					fmt.Fprintf(&b, "Text:\n%s\n\n", msg.Text)
				}
			}
			if runErr != nil {
				fmt.Fprintf(&b, "Agent error: %s\n", runErr)
			}
			return mcp.NewToolResultText(b.String()), nil
		}

		if runErr != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Task ran with error: %s", runErr)), nil
		}
		return mcp.NewToolResultText("Task executed successfully."), nil
	})

	return mcpserver.ServerTool{Tool: tool, Handler: handler}
}

// policyFromMCP extracts and parses the optional "policy" argument from
// an MCP create_task / update_task request. Returns (nil, "") when no
// policy was supplied. Returns (nil, userMessage) on validation failure
// (syntactic only — tool-name validation is deferred to fire time at
// the MCP surface since this code path doesn't have a per-caller
// tools.Registry handy).
func policyFromMCP(req mcp.CallToolRequest) (*models.Policy, string) {
	raw, ok := req.GetArguments()["policy"]
	if !ok || raw == nil {
		return nil, ""
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, "Invalid policy: " + err.Error()
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var p models.Policy
	if err := dec.Decode(&p); err != nil {
		return nil, "Invalid policy: " + err.Error()
	}
	return &p, ""
}
