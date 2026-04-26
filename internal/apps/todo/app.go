package todo

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

var instance *TodoApp

func init() {
	instance = &TodoApp{}
	apps.Register(instance)
}

// TodoApp is the todo/ticket tracking app.
type TodoApp struct {
	svc     *TodoService
	llm     *anthropic.Client
	taskSvc *services.TaskService
	enc     *crypto.Encryptor
}

// Configure wires the anthropic client (for the resolution suggester),
// the TaskService (for spawning tasks when a user taps a resolution
// chip), and the encryptor (for decrypting the tenant bot token to open
// a DM at resolve time). Call once from main.go after services.New.
// Safe to omit in tests: missing llm silently skips the suggester;
// missing taskSvc/enc fails any chip tap with a clear error.
func Configure(llm *anthropic.Client, taskSvc *services.TaskService, enc *crypto.Encryptor) {
	if instance == nil {
		return
	}
	instance.llm = llm
	instance.taskSvc = taskSvc
	instance.enc = enc
}

// Init sets up the service after DB is available and registers this
// app's CardProvider so its todos surface in the PWA stack.
func (a *TodoApp) Init(pool *pgxpool.Pool) {
	a.svc = &TodoService{pool: pool, app: a}
	apps.RegisterCardProvider(&cardProvider{app: a})
}

func (a *TodoApp) Name() string { return "todo" }

func (a *TodoApp) SystemPrompt() string {
	return `## Todo Tracking
You manage a lightweight todo/ticket system for the team. When users mention work that needs doing, offer to create a todo. When they report progress or blockers, update the relevant todo and add a comment. Use add_todo_comment to record important context from conversations. When marking a todo as blocked, always include a blocked_reason explaining why. Proactively mention overdue todos when relevant.`
}

func (a *TodoApp) ToolMetas() []services.ToolMeta {
	return todoTools
}

func (a *TodoApp) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r := registerer.(*tools.Registry)
	registerTodoAgentTools(r, isAdmin, a.svc)
}

func (a *TodoApp) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildTodoMCPTools(a.svc)
}

func (a *TodoApp) RegisterRoutes(_ *http.ServeMux) {
	// No HTTP routes for todo app.
}

func (a *TodoApp) CronJobs() []apps.CronJob {
	return nil
}

var todoTools = []services.ToolMeta{
	{
		Name:        "create_todo",
		Description: "Create a new todo/ticket. Defaults to scoped to you. Pass assigned_to to delegate, role_scope to scope to a team, or visibility=public to broadcast tenant-wide.",
		Schema: services.PropsReq(map[string]any{
			"title":       services.Field("string", "Short title for the todo"),
			"description": services.Field("string", "Detailed description"),
			"priority":    services.Field("string", "Priority: low, medium, high, urgent"),
			"assigned_to": services.Field("string", "User to assign/scope to. Accepts a kit UUID, Slack user ID (e.g. U09AN7KJU3G), or a unique display-name fragment. Use find_user first if you're not sure who matches. Mutually exclusive with role_scope."),
			"role_scope":  services.Field("string", "Role name this todo belongs to (e.g. bartender). Mutually exclusive with assigned_to."),
			"due_date":    services.Field("string", "Due date in YYYY-MM-DD format"),
			"visibility":  services.Field("string", "'scoped' (default — only assignee/role members see it) or 'public' (visible to everyone in the tenant)."),
		}, "title"),
	},
	{
		Name:        "list_todos",
		Description: "List todos with optional filters. Returns todos visible to you. Includes cancelled todos (soft-deleted) — filter by status if you want to exclude them.",
		Schema: services.Props(map[string]any{
			"status":         services.Field("string", "Filter by status: open, in_progress, blocked, done, cancelled"),
			"priority":       services.Field("string", "Filter by priority: low, medium, high, urgent"),
			"assigned_to_me": map[string]any{"type": "boolean", "description": "Only show todos assigned to me"},
			"role_scope":     services.Field("string", "Filter by role scope"),
			"search":         services.Field("string", "Full-text search on title and description"),
			"overdue":        map[string]any{"type": "boolean", "description": "Only show overdue todos (past due date, not done or cancelled)"},
			"closed_since":   services.Field("string", "Show todos closed since this date (YYYY-MM-DD)"),
		}),
	},
	{
		Name:        "get_todo",
		Description: "Get full details of a todo including recent activity log.",
		Schema: services.PropsReq(map[string]any{
			"todo_id": services.Field("string", "The todo UUID"),
		}, "todo_id"),
	},
	{
		Name:        "update_todo",
		Description: "Update a todo. Setting status to 'blocked' requires blocked_reason. Setting status to 'done' or 'cancelled' records closed_at. Use 'cancelled' as a soft delete (recoverable via DB update).",
		Schema: services.PropsReq(map[string]any{
			"todo_id":        services.Field("string", "The todo UUID"),
			"title":          services.Field("string", "New title"),
			"description":    services.Field("string", "New description"),
			"status":         services.Field("string", "New status: open, in_progress, blocked, done, cancelled"),
			"priority":       services.Field("string", "New priority: low, medium, high, urgent"),
			"blocked_reason": services.Field("string", "Reason for blocking (required when status=blocked)"),
			"assigned_to":    services.Field("string", "User to re-scope to. Mutually exclusive with role_scope."),
			"role_scope":     services.Field("string", fmt.Sprintf("Role name to re-scope to. Pass %q to fall back to the caller's user-scope.", ClearRoleScope)),
			"due_date":       services.Field("string", "Due date in YYYY-MM-DD format"),
			"visibility":     services.Field("string", "'scoped' or 'public'."),
		}, "todo_id"),
	},
	{
		Name:        "snooze_todo",
		Description: "Hide a todo from the swipe feed for N days. The todo stays active and still appears in list_todos; it just drops out of the feed until the snooze expires (wakes at 03:00 local on the target day).",
		Schema: services.PropsReq(map[string]any{
			"todo_id": services.Field("string", "The todo UUID"),
			"days":    map[string]any{"type": "integer", "description": "Snooze duration in days, 1-365. Common picks: 1, 3, 7, 14, 30."},
		}, "todo_id", "days"),
	},
	{
		Name:        "add_todo_comment",
		Description: "Add a comment to a todo's activity log. Use this to record progress, context from conversations, or notes.",
		Schema: services.PropsReq(map[string]any{
			"todo_id": services.Field("string", "The todo UUID"),
			"content": services.Field("string", "Comment text"),
		}, "todo_id", "content"),
	},
	{
		Name:        "complete_todo",
		Description: "Mark a todo as done. Shortcut for setting status to 'done'.",
		Schema: services.PropsReq(map[string]any{
			"todo_id": services.Field("string", "The todo UUID"),
		}, "todo_id"),
	},
}
