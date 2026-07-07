package task

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

var instance *TaskApp

func init() {
	instance = &TaskApp{}
	apps.Register(instance)
}

// TaskApp is the task/ticket tracking app.
type TaskApp struct {
	svc     *TaskService
	llm     *anthropic.Client
	taskSvc *services.JobService
	enc     *crypto.Encryptor
	signer  *auth.SessionSigner
	agent   *agent.Agent
}

// Configure wires the anthropic client (for the resolution suggester),
// the JobService (for spawning jobs when a user taps a resolution
// chip), the encryptor (for decrypting the tenant bot token to open a DM
// at resolve time and to reach mailbox credentials for email intake), and
// the agent (for running the email-intake sweep). Call once from main.go
// after services.New. Safe to omit in tests: missing llm silently skips
// the suggester; missing agent disables the intake sweep.
func Configure(llm *anthropic.Client, taskSvc *services.JobService, enc *crypto.Encryptor, signer *auth.SessionSigner, ag *agent.Agent) {
	if instance == nil {
		return
	}
	instance.llm = llm
	instance.taskSvc = taskSvc
	instance.enc = enc
	instance.signer = signer
	instance.agent = ag
}

// Init sets up the service after DB is available and registers this
// app's CardProvider so its tasks surface in the PWA stack.
func (a *TaskApp) Init(pool *pgxpool.Pool) {
	a.svc = &TaskService{pool: pool, app: a}
	apps.RegisterCardProvider(&cardProvider{app: a})
}

func (a *TaskApp) Name() string { return "task" }

// DisplayName and Description feed the admin Apps settings page.
func (a *TaskApp) DisplayName() string { return "Tasks" }
func (a *TaskApp) Description() string { return "Role-owned tasks, briefings, and the swipe feed." }

func (a *TaskApp) SystemPrompt() string {
	return mustRender("system_prompt.tmpl", nil)
}

func (a *TaskApp) ToolMetas() []services.ToolMeta {
	return taskTools
}

func (a *TaskApp) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r := registerer.(*tools.Registry)
	registerTaskAgentTools(r, isAdmin, a.svc)
}

func (a *TaskApp) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildTaskMCPTools(a.svc)
}

func (a *TaskApp) RegisterRoutes(mux apps.Mux) {
	if a.svc == nil || a.signer == nil {
		return
	}
	registerTaskRoutes(mux, a)
}

func (a *TaskApp) CronJobs() []apps.CronJob {
	return []apps.CronJob{a.emailIntakeCron()}
}

var taskTools = []services.ToolMeta{
	{
		Name:        "create_task",
		Description: "Create a new task. Every task belongs to a role (the team/project that owns it); pass role_scope or rely on the caller's primary role. Anyone in the role can see and edit; assignee is optional and orthogonal — it's just whose desk the task is on. When creating from an email-intake summary, pass source_email_uid so repeat scans don't create duplicates.",
		Schema: services.PropsReq(map[string]any{
			"title":            services.Field("string", "Short title for the task"),
			"description":      services.Field("string", "Detailed description"),
			"priority":         services.Field("string", "Priority: blocker, high, normal (default normal). A topical category is assigned automatically — don't pass one here."),
			"assignee":         services.Field("string", "Optional assignee. Accepts a kit UUID, Slack user ID (e.g. U09AN7KJU3G), or a unique display-name fragment. Leave empty for the team backlog. Use find_user if unsure."),
			"role_scope":       services.Field("string", "Role name that owns this task (e.g. bartender). Required unless caller has only one role or has a primary role set. Anyone in this role can see and edit the task."),
			"due_date":         services.Field("string", "Due date in YYYY-MM-DD format"),
			"source_email_uid": map[string]any{"type": "integer", "description": "Email-intake only: the uid of the source email summary this task comes from. The system derives a per-user dedup key from it and refuses to create a second task for the same email — even if a prior one was cancelled — so recurring scans don't pile up duplicates. Always pass it when creating a task from an email."},
		}, "title"),
	},
	{
		Name:        "list_tasks",
		Description: "List tasks visible to you (in your roles). By default returns only active tasks (open, in_progress, blocked) — done and cancelled are hidden to keep results small. To see closed tasks, pass `status` (e.g. 'done') or `include_closed: true`, or use `closed_since` for a date window.",
		Schema: services.Props(map[string]any{
			"status":         services.Field("string", "Filter by status: open, in_progress, blocked, done, cancelled"),
			"priority":       services.Field("string", "Filter by priority: blocker, high, normal"),
			"category":       services.Field("string", "Filter by topical category (e.g. brewing, sales)"),
			"assigned_to_me": map[string]any{"type": "boolean", "description": "Only show tasks assigned to me"},
			"assignee":       services.Field("string", "Filter by exact assignee (UUID, Slack ID, or name fragment)"),
			"unassigned":     map[string]any{"type": "boolean", "description": "Only show tasks with no assignee (the role backlog)"},
			"role_scope":     services.Field("string", "Filter by role scope"),
			"search":         services.Field("string", "Full-text search on title and description"),
			"overdue":        map[string]any{"type": "boolean", "description": "Only show overdue tasks (past due date, not done or cancelled)"},
			"closed_since":   services.Field("string", "Show tasks closed since this date (YYYY-MM-DD)"),
			"include_closed": map[string]any{"type": "boolean", "description": "Include done/cancelled tasks alongside active ones. Default false. Only needed when no `status` is specified — an explicit status (or `closed_since`) already controls inclusion."},
		}),
	},
	{
		Name:        "get_task",
		Description: "Get full details of a task including recent activity log.",
		Schema: services.PropsReq(map[string]any{
			"task_id": services.Field("string", "The task UUID"),
		}, "task_id"),
	},
	{
		Name:        "update_task",
		Description: "Update a task. Setting status to 'blocked' requires blocked_reason. Setting status to 'done' or 'cancelled' records closed_at. Use 'cancelled' as a soft delete (recoverable via DB update). Reassigning is independent of re-scoping: 'assignee' changes whose desk it's on; 'role_scope' moves the task to a different team.",
		Schema: services.PropsReq(map[string]any{
			"task_id":        services.Field("string", "The task UUID"),
			"title":          services.Field("string", "New title"),
			"description":    services.Field("string", "New description"),
			"status":         services.Field("string", "New status: open, in_progress, blocked, done, cancelled. Setting in_progress on an unassigned task claims it for you."),
			"priority":       services.Field("string", "New priority: blocker, high, normal"),
			"category":       services.Field("string", "Override the topical category (e.g. brewing, sales). Normally assigned automatically; set this to correct it. Empty string clears it."),
			"blocked_reason": services.Field("string", "Reason for blocking (required when status=blocked)"),
			"assignee":       services.Field("string", "New assignee (UUID, Slack ID, or name fragment). Doesn't change visibility — anyone in the role still sees the task."),
			"clear_assignee": map[string]any{"type": "boolean", "description": "Unset the assignee, returning the task to the team backlog."},
			"role_scope":     services.Field("string", "Move the task to a different role. Empty/'none' is rejected — every task must belong to a role."),
			"due_date":       services.Field("string", "Due date in YYYY-MM-DD format"),
			"clear_due_date": map[string]any{"type": "boolean", "description": "Remove the due date."},
		}, "task_id"),
	},
	{
		Name:        "snooze_task",
		Description: "Hide a task from your swipe feed for N days. The task stays active and still appears in list_tasks; it just drops out of the feed until the snooze expires (wakes at 03:00 local on the target day).",
		Schema: services.PropsReq(map[string]any{
			"task_id": services.Field("string", "The task UUID"),
			"days":    map[string]any{"type": "integer", "description": "Snooze duration in days, 1-365. Common picks: 1, 3, 7, 14, 30."},
		}, "task_id", "days"),
	},
	{
		Name:        "add_task_comment",
		Description: "Add a comment to a task's activity log. Use this to record progress, context from conversations, or notes.",
		Schema: services.PropsReq(map[string]any{
			"task_id": services.Field("string", "The task UUID"),
			"content": services.Field("string", "Comment text"),
		}, "task_id", "content"),
	},
	{
		Name:        "complete_task",
		Description: "Mark a task as done. Shortcut for setting status to 'done'.",
		Schema: services.PropsReq(map[string]any{
			"task_id": services.Field("string", "The task UUID"),
		}, "task_id"),
	},
}
