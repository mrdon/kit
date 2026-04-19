// Package builder: action_builtins.go wraps Kit's existing agent-tool
// surface as a flat allowlist of Monty host functions that admin-authored
// scripts can call.
//
// Unlike the db_* bridge (which speaks MongoDB on top of app_items), these
// host functions route through the canonical services layer so tenant
// isolation, validation, and scope enforcement are handled by the same
// code that backs the LLM agent and MCP server.
//
// The flat-name convention mirrors db_builtins: each action is a top-level
// Python function whose kwargs match the service call's fields. Scripts
// look like:
//
//	todo = create_todo(title="Prep garnishes", priority="high")
//	update_todo(todo_id=todo["id"], status="in_progress")
//	send_slack_message(channel="#ops", text="Prepped and done")
//
// Every call returns a plain dict / list / scalar — nothing Go-struct
// shaped crosses the WASM boundary.
package builder

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/apps/todo"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// Canonical action-builtin names. Exported so tests and other wiring can
// reference them without hard-coding strings.
const (
	FnCreateTodo       = "create_todo"
	FnUpdateTodo       = "update_todo"
	FnCompleteTodo     = "complete_todo"
	FnAddTodoComment   = "add_todo_comment"
	FnCreateDecision   = "create_decision"
	FnCreateBriefing   = "create_briefing"
	FnCreateTask       = "create_task"
	FnAddMemory        = "add_memory"
	FnSendSlackMessage = "send_slack_message"
	FnPostToChannel    = "post_to_channel"
	FnDMUser           = "dm_user"
	FnFindUser         = "find_user"
)

// ActionBuiltins is the return bundle from BuildActionBuiltins: the
// FuncDefs to register with Monty (so positional args get named), a
// dispatcher ExternalFunc, per-name param metadata (for
// runtime.Capabilities.BuiltInParams), and a map keyed by function name
// (for runtime.Capabilities.BuiltIns).
type ActionBuiltins struct {
	Funcs    []runtime.FuncDef
	Handler  runtime.ExternalFunc
	BuiltIns map[string]runtime.GoFunc
	Params   map[string][]string

	// Mutation counters; bumped on successful create_* / update_* /
	// complete_* / add_* / delete_* calls. MutationSummary flattens them
	// to a map for ScriptRun.mutation_summary (Phase 4).
	insertCount int
	updateCount int
	deleteCount int
}

// MutationSummary reports per-run mutation counts for storage in the
// ScriptRun audit trail. The returned map copies the internal counters
// so later calls can keep incrementing safely.
func (a *ActionBuiltins) MutationSummary() map[string]int {
	return map[string]int{
		"inserts": a.insertCount,
		"updates": a.updateCount,
		"deletes": a.deleteCount,
	}
}

// actionDeps captures everything an individual action handler needs to
// route into the services layer. Built once per BuildActionBuiltins call
// and captured by each dispatch closure.
type actionDeps struct {
	pool     *pgxpool.Pool
	svc      *services.Services
	todoSvc  *todo.TodoService
	cardsSvc *cards.CardService
	slack    *kitslack.Client

	tenantID     uuid.UUID
	appID        uuid.UUID
	callerUserID uuid.UUID
	runID        *uuid.UUID

	// caller is the services.Caller constructed from the current tenant +
	// user. Rebuilt lazily on first use so cold path is free.
	callerCache *services.Caller
}

// caller returns (and caches) the services.Caller for the run. Uses the
// default-role membership path so role-scoped actions respect the same
// role list the agent loop would see for this user.
func (d *actionDeps) caller(ctx context.Context) (*services.Caller, error) {
	if d.callerCache != nil {
		return d.callerCache, nil
	}
	// User lookup — we need display name / admin flag / timezone to
	// mirror what ExecContext.Caller() builds for agent tools.
	user, err := models.GetUserByID(ctx, d.pool, d.tenantID, d.callerUserID)
	if err != nil {
		return nil, fmt.Errorf("loading caller user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("caller user %s not found in tenant %s", d.callerUserID, d.tenantID)
	}
	tenant, err := models.GetTenantByID(ctx, d.pool, d.tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return nil, fmt.Errorf("tenant %s not found", d.tenantID)
	}
	roles, err := models.GetUserRoleNames(ctx, d.pool, d.tenantID, d.callerUserID, tenant.DefaultRoleID)
	if err != nil {
		return nil, fmt.Errorf("loading user roles: %w", err)
	}
	d.callerCache = &services.Caller{
		TenantID: d.tenantID,
		UserID:   d.callerUserID,
		Identity: user.SlackUserID,
		Roles:    roles,
		IsAdmin:  user.IsAdmin,
		Timezone: services.ResolveTimezone(user.Timezone, tenant.Timezone),
	}
	return d.callerCache, nil
}

// BuildActionBuiltins wires Kit's services layer into a batch of Monty
// host functions. Script authors see a flat allowlist of calls
// (create_todo, send_slack_message, find_user, etc.); every call is
// tenant-scoped to tenantID via the services.Caller built from
// callerUserID.
//
// pool is read from svc.Users.pool via the services layer; todoSvc and
// cardsSvc are constructed here so callers don't have to plumb the whole
// app-registry through the runtime. slackClient may be nil when the host
// doesn't have messaging capability for this run (tests, dry runs) —
// send_slack_message / dm_user / post_to_channel then error at dispatch
// time rather than silently no-op.
func BuildActionBuiltins(
	pool *pgxpool.Pool,
	svc *services.Services,
	slack *kitslack.Client,
	tenantID, appID, callerUserID uuid.UUID,
	runID *uuid.UUID,
) *ActionBuiltins {
	a := &ActionBuiltins{}

	deps := &actionDeps{
		pool:         pool,
		svc:          svc,
		todoSvc:      todo.NewService(pool),
		cardsSvc:     cards.NewService(pool),
		slack:        slack,
		tenantID:     tenantID,
		appID:        appID,
		callerUserID: callerUserID,
		runID:        runID,
	}

	handler := func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
		switch call.Name {
		case FnCreateTodo:
			return dispatchCreateTodo(ctx, a, deps, call)
		case FnUpdateTodo:
			return dispatchUpdateTodo(ctx, a, deps, call)
		case FnCompleteTodo:
			return dispatchCompleteTodo(ctx, a, deps, call)
		case FnAddTodoComment:
			return dispatchAddTodoComment(ctx, a, deps, call)
		case FnCreateDecision:
			return dispatchCreateDecision(ctx, a, deps, call)
		case FnCreateBriefing:
			return dispatchCreateBriefing(ctx, a, deps, call)
		case FnCreateTask:
			return dispatchCreateTask(ctx, a, deps, call)
		case FnAddMemory:
			return dispatchAddMemory(ctx, a, deps, call)
		case FnSendSlackMessage, FnPostToChannel:
			return dispatchSendSlackMessage(ctx, deps, call)
		case FnDMUser:
			return dispatchDMUser(ctx, deps, call)
		case FnFindUser:
			return dispatchFindUser(ctx, deps, call)
		default:
			return nil, fmt.Errorf("action_builtins: unknown function %q", call.Name)
		}
	}

	params := map[string][]string{
		FnCreateTodo:       {"title", "description", "priority", "due_date", "role_scope", "assigned_to", "private"},
		FnUpdateTodo:       {"todo_id", "status", "priority", "due_date", "role_scope", "blocked_reason", "assigned_to"},
		FnCompleteTodo:     {"todo_id", "note"},
		FnAddTodoComment:   {"todo_id", "content"},
		FnCreateDecision:   {"title", "body", "options", "priority", "role_scopes"},
		FnCreateBriefing:   {"title", "body", "severity", "role_scopes"},
		FnCreateTask:       {"description", "cron", "timezone", "channel", "run_once"},
		FnAddMemory:        {"content", "scope_type", "scope_value"},
		FnSendSlackMessage: {"channel", "text", "thread_ts"},
		FnPostToChannel:    {"channel", "text", "thread_ts"},
		FnDMUser:           {"user_id", "text"},
		FnFindUser:         {"name_or_mention"},
	}

	// FuncDefs in stable (alphabetical) order for deterministic logs.
	funcs := []runtime.FuncDef{
		runtime.Func(FnAddMemory, params[FnAddMemory]...),
		runtime.Func(FnAddTodoComment, params[FnAddTodoComment]...),
		runtime.Func(FnCompleteTodo, params[FnCompleteTodo]...),
		runtime.Func(FnCreateBriefing, params[FnCreateBriefing]...),
		runtime.Func(FnCreateDecision, params[FnCreateDecision]...),
		runtime.Func(FnCreateTask, params[FnCreateTask]...),
		runtime.Func(FnCreateTodo, params[FnCreateTodo]...),
		runtime.Func(FnDMUser, params[FnDMUser]...),
		runtime.Func(FnFindUser, params[FnFindUser]...),
		runtime.Func(FnPostToChannel, params[FnPostToChannel]...),
		runtime.Func(FnSendSlackMessage, params[FnSendSlackMessage]...),
		runtime.Func(FnUpdateTodo, params[FnUpdateTodo]...),
	}

	builtIns := map[string]runtime.GoFunc{}
	for name := range params {
		builtIns[name] = func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
			if call.Name != name {
				return nil, fmt.Errorf("action_builtins: name mismatch %q != %q", call.Name, name)
			}
			return handler(ctx, call)
		}
	}

	a.Funcs = funcs
	a.Handler = handler
	a.BuiltIns = builtIns
	a.Params = params
	return a
}

// ---- find_user, messaging, memory, task dispatchers --------------------

// dispatchFindUser resolves a user reference by display name, Slack user
// ID, or kit UUID. Returns a flat dict {id, display_name, slack_user_id}
// on unique match, or nil (Python None) when nothing matches.
//
// Per the Phase-3 isolation boundary, find_user is the ONLY cross-app
// read exposed to scripts — everything else is same-app or tenant-global
// (memories, tasks). See internal/apps/builder/runtime/README.md.
func dispatchFindUser(ctx context.Context, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	ref, err := argString(call.Args, "name_or_mention")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	u, err := deps.svc.Users.Resolve(ctx, c, ref)
	if err != nil {
		// Not-found is not an error at the script level; ambiguity is.
		var ambig *models.ErrAmbiguousUser
		if errors.As(err, &ambig) {
			return nil, fmt.Errorf("%s: %w", call.Name, err)
		}
		if errors.Is(err, services.ErrNotFound) {
			return nil, nil //nolint:nilnil
		}
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	display := ""
	if u.DisplayName != nil {
		display = *u.DisplayName
	}
	return map[string]any{
		"id":            u.ID.String(),
		"display_name":  display,
		"slack_user_id": u.SlackUserID,
	}, nil
}

// dispatchSendSlackMessage wraps both send_slack_message and post_to_channel
// (aliased because v0.1 semantics are identical — post to a channel id or
// name, optionally inside a thread).
func dispatchSendSlackMessage(ctx context.Context, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	if deps.slack == nil {
		return nil, fmt.Errorf("%s: slack client not configured for this run", call.Name)
	}
	channel, err := argString(call.Args, "channel")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	text, err := argString(call.Args, "text")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	threadTS, err := argOptionalString(call.Args, "thread_ts")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	ts, err := deps.slack.PostMessageReturningTS(ctx, channel, threadTS, text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	return map[string]any{
		"ts":      ts,
		"channel": channel,
	}, nil
}

// dispatchDMUser opens (or reuses) a DM channel with the given Slack user
// id and posts text there. Returns the channel id and message ts.
func dispatchDMUser(ctx context.Context, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	if deps.slack == nil {
		return nil, fmt.Errorf("%s: slack client not configured for this run", call.Name)
	}
	userID, err := argString(call.Args, "user_id")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	text, err := argString(call.Args, "text")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	channelID, err := deps.slack.OpenConversation(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("%s: opening DM: %w", call.Name, err)
	}
	ts, err := deps.slack.PostMessageReturningTS(ctx, channelID, "", text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	return map[string]any{
		"ts":      ts,
		"channel": channelID,
	}, nil
}

// dispatchAddMemory saves a memory with scope resolution. scope_type
// defaults to "user"; scope_value is ignored for "user"/"tenant" and used
// as the role name for role-scoped memories.
func dispatchAddMemory(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	content, err := argString(call.Args, "content")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	scopeType, err := argOptionalString(call.Args, "scope_type")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	if scopeType == "" {
		scopeType = "tenant"
	}
	scopeValue, err := argOptionalString(call.Args, "scope_value")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	// Map scope_type/scope_value into the single "scope" string the
	// service.Save expects: "user", "tenant", or a role name.
	scope := scopeType
	if scopeType != "user" && scopeType != "tenant" && scopeValue != "" {
		scope = scopeValue
	}

	if err := deps.svc.Memories.Save(ctx, c, content, scope, uuid.Nil); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.insertCount++
	// Memories.Save doesn't currently return the row ID, so we report a
	// minimal success shape. Future enhancement: thread the id back.
	return map[string]any{"ok": true}, nil
}
