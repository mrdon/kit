package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools"
)

// InstallScriptRunDeps builds the production scriptRunDeps (Monty engine +
// shared services + Anthropic sender + per-tenant Slack factory) and wires
// them into the package-global used by run_script and the scheduled-script
// task runner. Call from main once apps.Init has run and the pool is live.
// Returns a close func that tears down the WASM runtime on shutdown.
//
// The Slack factory looks up the tenant's encrypted bot token on each run
// and constructs a fresh kitslack.Client. A missing tenant or undecryptable
// token disables Slack action builtins for that one run (they return their
// existing "not configured" error) without aborting the run.
func InstallScriptRunDeps(pool *pgxpool.Pool, svc *services.Services, enc *crypto.Encryptor, sender Sender) (func() error, error) {
	if pool == nil || svc == nil || enc == nil || sender == nil {
		return nil, errors.New("builder: install deps requires pool + services + encryptor + sender")
	}
	engine, err := runtime.NewMontyEngineOwned()
	if err != nil {
		return nil, fmt.Errorf("builder: monty engine: %w", err)
	}
	deps := &scriptRunDeps{
		Services:   svc,
		Engine:     engine,
		Sender:     sender,
		BuildSlack: tenantSlackFactory(pool, enc),
	}
	SetScriptRunDeps(deps)
	WireTaskRunners(pool, deps)
	return func() error {
		SetScriptRunDeps(nil)
		WireTaskRunners(nil, nil)
		return engine.Close()
	}, nil
}

// tenantSlackFactory returns a closure that builds a per-tenant Slack
// client. Returns (nil, nil) when the tenant has no bot token rather than
// surfacing an error — Slack action builtins already handle a nil client
// with a clear "not configured" message.
func tenantSlackFactory(pool *pgxpool.Pool, enc *crypto.Encryptor) func(context.Context, uuid.UUID) (*kitslack.Client, error) {
	return func(ctx context.Context, tenantID uuid.UUID) (*kitslack.Client, error) {
		tenant, err := models.GetTenantByID(ctx, pool, tenantID)
		if err != nil {
			return nil, fmt.Errorf("loading tenant: %w", err)
		}
		if tenant == nil || tenant.BotToken == "" {
			return nil, nil
		}
		token, err := enc.Decrypt(tenant.BotToken)
		if err != nil {
			return nil, fmt.Errorf("decrypting bot token: %w", err)
		}
		return kitslack.NewClient(token), nil
	}
}

func init() {
	apps.Register(&App{})
}

// App is the scriptable app substrate. Admins use it to build tenant-scoped
// apps via Claude Code + Kit's MCP. v0.1 scaffolding lived in Phase 1-3;
// Phase 4 lights up the meta-tools — create_app, list_apps, get_app,
// delete_app, purge_app_data today; add_script et al. in later subtasks.
type App struct {
	pool *pgxpool.Pool
}

// Init gets called after the pool is ready. We stash the pool so MCP tool
// registration (which receives the pool per call) and agent tool registration
// (which doesn't) can both build handlers that share one source of truth.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
}

func (a *App) Name() string { return "builder" }

// SystemPrompt is intentionally empty. Two reasons:
//   - Builder apps run as tenant-scoped scripts and should not leak into
//     every user's agent turn (prompt bloat, cross-tenant noise).
//   - Admin-side guidance for using the meta-tools (create_app /
//     create_script / expose_script_function_as_tool / ...) ships as an
//     admin-only built-in skill (`internal/skills/builtins/builder-admin-guide/`).
//     The skill catalog already role-filters its contributions to the
//     system prompt, so the admin guide lands in admin sessions and
//     stays out of everyone else's — no conditional code needed here.
func (a *App) SystemPrompt() string { return "" }

// ToolMetas surfaces the meta-tool metadata so the services layer and the
// tool catalog see a consistent shape across agent and MCP callers. All
// meta-tools carry AdminOnly=true.
func (a *App) ToolMetas() []services.ToolMeta {
	out := make([]services.ToolMeta, 0,
		len(metaAppTools)+len(metaScriptTools)+len(metaScheduleTools)+
			len(metaExposedTools)+len(metaDiagnosticTools)+len(metaExampleTools))
	out = append(out, metaAppTools...)
	out = append(out, metaScriptTools...)
	out = append(out, metaScheduleTools...)
	out = append(out, metaExposedTools...)
	out = append(out, metaDiagnosticTools...)
	out = append(out, metaExampleTools...)
	return out
}

// allMetaTools returns the combined metadata list used by agent/MCP
// registration loops.
func (a *App) allMetaTools() []services.ToolMeta {
	return a.ToolMetas()
}

// metaHandler resolves a handler by name across all meta-tool categories.
// Nil for unknown names so the registration loop skips them.
func metaHandler(name string) func(*execContextLike, json.RawMessage) (string, error) {
	if h := metaAppAgentHandler(name); h != nil {
		return h
	}
	if h := metaScriptAgentHandler(name); h != nil {
		return h
	}
	if h := metaScheduleAgentHandler(name); h != nil {
		return h
	}
	if h := metaExposedAgentHandler(name); h != nil {
		return h
	}
	if h := metaDiagnosticAgentHandler(name); h != nil {
		return h
	}
	if h := metaExampleAgentHandler(name); h != nil {
		return h
	}
	return nil
}

// RegisterAgentTools wires the Phase 4 meta-tools into the agent registry.
// Skips registration entirely for non-admins — the MCP side keeps the admin
// check inside the handler (tools are registered once at server startup).
func (a *App) RegisterAgentTools(registerer any, isAdmin bool) {
	if !isAdmin || a.pool == nil {
		return
	}
	r, ok := registerer.(*tools.Registry)
	if !ok {
		return
	}
	for _, meta := range a.allMetaTools() {
		handler := metaHandler(meta.Name)
		if handler == nil {
			continue
		}
		r.Register(tools.Def{
			Name:           meta.Name,
			Description:    meta.Description,
			Schema:         meta.Schema,
			AdminOnly:      meta.AdminOnly,
			VisibleToRoles: meta.VisibleToRoles,
			Handler:        wrapAgentHandler(a.pool, handler),
		})
	}
}

// wrapAgentHandler adapts our execContextLike-taking handler to the
// tools.HandlerFunc signature. The agent's ExecContext already has Ctx +
// Pool + a Caller factory, so the adapter is a one-liner.
func wrapAgentHandler(
	pool *pgxpool.Pool,
	fn func(*execContextLike, json.RawMessage) (string, error),
) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		ctx := &execContextLike{
			Ctx:    ec.Ctx,
			Pool:   pool,
			Caller: ec.Caller(),
		}
		out, err := fn(ctx, input)
		if err != nil {
			return friendlyErr(err), nil
		}
		return out, nil
	}
}

// RegisterMCPTools builds the MCP ServerTool entries for each meta-tool.
// Unlike the agent side there is no admin pre-check here — the handler
// resolves the caller from ctx via mcpauth.WithCaller and passes through
// guardAdmin. A non-admin user sees the tool in the catalog but gets an
// ErrForbidden result when they call it, mirroring how other apps handle
// admin-only MCP tools.
func (a *App) RegisterMCPTools(pool *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	if pool == nil {
		return nil
	}
	// If Init hasn't run yet (tests), fall back to the explicit pool.
	if a.pool == nil {
		a.pool = pool
	}
	metas := a.allMetaTools()
	result := make([]mcpserver.ServerTool, 0, len(metas))
	for _, meta := range metas {
		handler := buildMetaAppMCPHandler(pool, meta.Name)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

// buildMetaAppMCPHandler constructs the MCP handler for one meta-tool. The
// raw arguments go through the same parseInput/argString helpers as the
// agent side — one contract, one set of error messages.
func buildMetaAppMCPHandler(pool *pgxpool.Pool, name string) mcpserver.ToolHandlerFunc {
	handler := metaHandler(name)
	if handler == nil {
		return nil
	}
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		raw, err := json.Marshal(args)
		if err != nil {
			return mcp.NewToolResultError("Invalid arguments: " + err.Error()), nil
		}
		ec := &execContextLike{
			Ctx:    ctx,
			Pool:   pool,
			Caller: caller,
		}
		out, err := handler(ec, raw)
		if err != nil {
			return mcp.NewToolResultError(friendlyErr(err)), nil
		}
		return mcp.NewToolResultText(out), nil
	})
}

// RegisterRoutes is a no-op for now; builder admin UI routes land later.
func (a *App) RegisterRoutes(_ *http.ServeMux) {}

// CronJobs returns nil; scheduled scripts plug in once the runtime is
// wired to the scheduler.
func (a *App) CronJobs() []apps.CronJob { return nil }
