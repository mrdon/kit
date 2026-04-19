package todo

import (
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

func init() {
	apps.Register(&TodoApp{})
}

// TodoApp is the todo/ticket tracking app.
type TodoApp struct {
	svc *TodoService
}

// Init sets up the service after DB is available and registers this
// app's CardProvider so its todos surface in the PWA stack.
func (a *TodoApp) Init(pool *pgxpool.Pool) {
	a.svc = &TodoService{pool: pool}
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

func (a *TodoApp) RegisterAgentTools(registerer any, isAdmin bool) {
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
		Description: "Create a new todo/ticket. Use role_scope to categorize by team. Set private=true to hide from others.",
		Schema: services.PropsReq(map[string]any{
			"title":       services.Field("string", "Short title for the todo"),
			"description": services.Field("string", "Detailed description"),
			"priority":    services.Field("string", "Priority: low, medium, high, urgent"),
			"assigned_to": services.Field("string", "User to assign to. Accepts a kit UUID, Slack user ID (e.g. U09AN7KJU3G), or a unique display-name fragment (e.g. 'matt'). Use find_user first if you're not sure who matches."),
			"role_scope":  services.Field("string", "Role name this todo belongs to (e.g. bartender)"),
			"due_date":    services.Field("string", "Due date in YYYY-MM-DD format"),
			"private":     map[string]any{"type": "boolean", "description": "If true, only creator and assignee can see this todo"},
		}, "title"),
	},
	{
		Name:        "list_todos",
		Description: "List todos with optional filters. Returns todos visible to you.",
		Schema: services.Props(map[string]any{
			"status":         services.Field("string", "Filter by status: open, in_progress, blocked, done"),
			"priority":       services.Field("string", "Filter by priority: low, medium, high, urgent"),
			"assigned_to_me": map[string]any{"type": "boolean", "description": "Only show todos assigned to me"},
			"role_scope":     services.Field("string", "Filter by role scope"),
			"search":         services.Field("string", "Full-text search on title and description"),
			"overdue":        map[string]any{"type": "boolean", "description": "Only show overdue todos (past due date, not done)"},
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
		Description: "Update a todo. Setting status to 'blocked' requires blocked_reason. Setting status to 'done' records closed_at.",
		Schema: services.PropsReq(map[string]any{
			"todo_id":        services.Field("string", "The todo UUID"),
			"title":          services.Field("string", "New title"),
			"description":    services.Field("string", "New description"),
			"status":         services.Field("string", "New status: open, in_progress, blocked, done"),
			"priority":       services.Field("string", "New priority: low, medium, high, urgent"),
			"blocked_reason": services.Field("string", "Reason for blocking (required when status=blocked)"),
			"assigned_to":    services.Field("string", "User UUID to assign to"),
			"role_scope":     services.Field("string", fmt.Sprintf("Role name this todo belongs to. Pass %q to clear the role scope.", ClearRoleScope)),
			"due_date":       services.Field("string", "Due date in YYYY-MM-DD format"),
			"private":        map[string]any{"type": "boolean", "description": "If true, only creator and assignee can see this todo"},
		}, "todo_id"),
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
