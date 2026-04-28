package coordination

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/services/messenger"
	"github.com/mrdon/kit/internal/tools"
)

var instance *CoordinationApp

func init() {
	instance = &CoordinationApp{}
	apps.Register(instance)
}

// CoordinationApp is the multi-party coordination engine. Phase 1
// supports meeting scheduling on Slack; later phases add email/SMS
// channels and approval/quorum kinds.
type CoordinationApp struct {
	pool    *pgxpool.Pool
	llm     *anthropic.Client
	msg     *messenger.Default
	cards   *cards.CardService
	taskSvc *services.TaskService
	svc     *Service
	engine  *Engine
}

// Configure wires the app's runtime dependencies. Call once from main.go
// after services.New.
func Configure(llm *anthropic.Client, msg *messenger.Default, cardSvc *cards.CardService, taskSvc *services.TaskService) {
	if instance == nil {
		return
	}
	instance.llm = llm
	instance.msg = msg
	instance.cards = cardSvc
	instance.taskSvc = taskSvc
}

// Init sets up the service after the DB pool is available and registers
// the reply handler with Messenger so inbound replies route to the
// engine.
func (a *CoordinationApp) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.svc = newService(pool, a)
	a.engine = newEngine(pool, a)
	if a.msg != nil {
		a.msg.RegisterReplyHandler("coordination", a.handleInboundReply)
	}
}

func (a *CoordinationApp) Name() string { return "coordination" }

func (a *CoordinationApp) SystemPrompt() string {
	return `## Coordination
You can run multi-party coordinations on the user's behalf — finding
meeting times across multiple people. Use start_coordination when the
user wants to schedule something with two or more attendees and lacks
a fixed time. Provide participants as a list of Slack user IDs you've
already resolved with find_user. After starting, the engine handles
outreach, reminders, and convergence on its own; the user will see a
decision card when a slot is agreed. Use get_coordination if the user
asks for status. Don't try to do scheduling outreach via direct DMs
yourself — coordinations exist precisely to handle the multi-day async
case the agent loop can't.`
}

func (a *CoordinationApp) ToolMetas() []services.ToolMeta {
	return coordinationTools
}

func (a *CoordinationApp) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r := registerer.(*tools.Registry)
	registerCoordinationAgentTools(r, isAdmin, a.svc)
}

func (a *CoordinationApp) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildCoordinationMCPTools(a.svc)
}

func (a *CoordinationApp) RegisterRoutes(_ *http.ServeMux) {
	// No HTTP routes for coordination.
}

func (a *CoordinationApp) CronJobs() []apps.CronJob {
	return []apps.CronJob{
		{
			Name:     "coordination_sweep",
			Interval: 60 * time.Second,
			Run:      a.cronSweep,
		},
	}
}

// cronSweep is the cron handler. Loads active coordinations across all
// tenants and ticks the engine for each.
func (a *CoordinationApp) cronSweep(ctx context.Context, _ *pgxpool.Pool, _ *crypto.Encryptor) error {
	if a.engine == nil {
		return nil
	}
	return a.engine.Tick(ctx)
}
