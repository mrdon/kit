package coordination

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
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

// MessengerOrigin is the value used for messenger.SendRequest.Origin
// (and the corresponding RegisterReplyHandler key). Centralized so
// the engine, reply handler, decision-card payloads, and tests all
// reference the same string.
const MessengerOrigin = "coordination"

// participantSessionThreadKey is the messenger.SendRequest.SessionThreadKey
// prefix that gives each (coord, participant) its own session. Format:
// "participant:<participant_id>" — chosen so per-participant isolation
// doesn't accidentally collide with anything else writing to sessions.
func participantSessionThreadKey(participantID uuid.UUID) string {
	return "participant:" + participantID.String()
}

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
// the reply handler with Messenger. Inbound routing is handled by
// Messenger's generic "most recent bot outbound with await_reply=true"
// query, which finds coord's per-(participant) session via the
// message_sent event and routes to handleInboundReply.
func (a *CoordinationApp) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.svc = newService(pool, a)
	a.engine = newEngine(pool, a)
	if a.msg != nil {
		a.msg.RegisterReplyHandler(MessengerOrigin, a.handleInboundReply)
	}
}

func (a *CoordinationApp) Name() string { return "coordination" }

func (a *CoordinationApp) SystemPrompt() string {
	return `## Coordination
For ANY meeting-scheduling outreach to one or more people, you MUST use
start_coordination — never dm_user, post_to_channel, or any other tool
to send scheduling messages to participants. The participants list is
"who to DM" and does NOT include the organizer (their availability is
implicit via candidate_slots). For a 1:1 between the organizer and
Alice, pass participants=["U_ALICE"]. For a meeting Alice ↔ Bob arranged
by the organizer, pass ["U_ALICE","U_BOB"].

If start_coordination errors, READ THE ERROR and fix the call. Do NOT
fall back to manually sending DMs via dm_user — that bypasses the
organizer's per-message approval cards and is treated as an unauthorized
outbound. If you genuinely can't recover, ask the organizer in chat
what to do instead.

After start_coordination succeeds, the engine handles outreach,
reminders, and convergence on its own. The organizer will see an
approval card per drafted DM, then a convergence card when a slot is
agreed. Use get_coordination if asked for status; cancel_coordination
to abort.`
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
