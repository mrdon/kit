package cards

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool" //nolint:goimports
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/chat"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
	"github.com/mrdon/kit/internal/transcribe"
)

// instance is the process-wide singleton — there's exactly one CardsApp
// and main.go calls Configure to attach HTTP-level deps.
var instance *CardsApp

func init() {
	instance = &CardsApp{}
	apps.Register(instance)
}

// Configure wires the PWA session signer, Slack OpenID config, base URL,
// and dev-mode flag into the cards app. Safe to call once at startup,
// before apps.RegisterAllRoutes.
func Configure(signer *auth.SessionSigner, slack auth.SlackOpenIDConfig, baseURL string, devMode bool) {
	if instance == nil {
		return
	}
	instance.signer = signer
	instance.slack = slack
	instance.baseURL = baseURL
	instance.devMode = devMode
}

// ConfigureChat wires the dependencies needed by the chat/transcribe and
// chat/execute SSE endpoints. Optional: if transcriber is nil the voice
// path returns a clear "not configured" error; if agent is nil both
// endpoints return 503.
func ConfigureChat(ag *agent.Agent, enc *crypto.Encryptor, transcriber transcribe.Transcriber) {
	if instance == nil {
		return
	}
	instance.agent = ag
	instance.enc = enc
	instance.transcriber = transcriber
	instance.chatLimiter = chat.NewExecuteLimiter()
}

// ConfigureKicker wires the scheduler's Kick callback so ResolveDecision
// can wake the task loop immediately on resume instead of waiting for
// the next poll tick.
func ConfigureKicker(k TaskKicker) {
	if instance == nil || instance.svc == nil {
		return
	}
	instance.svc.kicker = k
}

// ConfigurePolicyLookup wires the tool-policy lookup used by
// CreateDecision (to stamp is_gate_artifact) and ResolveDecision (to
// re-check the gate at approval time). Call once from main.go after
// all tool packages have registered their Defs.
func ConfigurePolicyLookup(lookup PolicyLookup) {
	if instance == nil || instance.svc == nil {
		return
	}
	instance.svc.ConfigurePolicyLookup(lookup)
}

// ConfigureToolExecutor wires the per-caller tool executor used by
// ResolveDecision to invoke a gated tool after approval. Call once
// from main.go alongside the other Configure* wiring.
func ConfigureToolExecutor(exec ToolExecutor) {
	if instance == nil || instance.svc == nil {
		return
	}
	instance.svc.ConfigureToolExecutor(exec)
}

// ServiceForGating returns the CardService so main.go can register it
// with tools.SetGateCreator. The CardService satisfies
// tools.GateCreator via its CreateGateCard method.
func ServiceForGating() *CardService {
	if instance == nil {
		return nil
	}
	return instance.svc
}

// SweepStuckResolvingCards flips any app_card_decisions row in
// 'resolving' past its deadline back to 'pending' with a last_error
// note, so the user can re-approve. The gated-tool handler's
// resolve_token dedupe prevents double-execution if the original call
// actually succeeded after timeout.
//
// Invoked by the scheduler every 60s (via the package-level adapter
// RegisterSweepWithScheduler). Tests call this directly with the
// test pool.
func SweepStuckResolvingCards(ctx context.Context, pool *pgxpool.Pool) error {
	n, err := sweepStuckResolvingCards(ctx, pool)
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("recovered stuck resolving cards", "count", n)
	}
	return nil
}

// sweepFromInstance is the periodic-sweep adapter registered with
// scheduler.RegisterPeriodicSweep. It pulls the pool off the package
// singleton; if the cards app was never initialized (e.g. during a
// misconfigured startup) it's a no-op.
func sweepFromInstance(ctx context.Context) error {
	if instance == nil || instance.pool == nil {
		return nil
	}
	return SweepStuckResolvingCards(ctx, instance.pool)
}

// PeriodicSweep returns the closure main.go should register with
// scheduler.RegisterPeriodicSweep. Kept as a function rather than a
// bare variable so startup ordering (which may Init the cards app
// after main registers sweeps) stays correct — the returned closure
// reads instance at invocation time.
func PeriodicSweep() func(context.Context) error {
	return sweepFromInstance
}

// CardsApp is the decisions/briefings swipe-stack app.
type CardsApp struct {
	svc         *CardService
	pool        *pgxpool.Pool
	signer      *auth.SessionSigner
	slack       auth.SlackOpenIDConfig
	baseURL     string
	devMode     bool
	agent       *agent.Agent
	enc         *crypto.Encryptor
	transcriber transcribe.Transcriber
	chatLimiter *chat.ExecuteLimiter
}

// Init sets up the service after DB is available and registers the
// CardProvider adapter. The provider wraps this app's own svc so stack
// fan-out sees decisions and briefings alongside items from other apps.
func (a *CardsApp) Init(pool *pgxpool.Pool) {
	a.svc = &CardService{pool: pool}
	a.pool = pool
	apps.RegisterCardProvider(&cardsProvider{app: a})
}

func (a *CardsApp) Name() string { return "cards" }

func (a *CardsApp) SystemPrompt() string {
	return `## Decisions and briefings (card stack)
Kit surfaces agent-generated cards to the user via a swipeable mobile stack. Two kinds:
- **Decisions** — a judgment call with 2–4 options and a recommended default. Creating one pauses the workflow until a human picks. Each option carries a "prompt" string that gets handed to the agent as a one-shot task when that option is chosen. Use a noop option (empty prompt) for "skip / I'll handle it".
- **Briefings** — informational updates the user should see but don't require action. Use "severity" info/notable/important.

Prefer a decision when there's a concrete action, even if you're unsure which option is right — that's what the human is for. Keep cards scoped by role when the target audience is obvious (e.g. a barback decision → role: bartender).`
}

func (a *CardsApp) ToolMetas() []services.ToolMeta {
	return cardsTools
}

func (a *CardsApp) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r := registerer.(*tools.Registry)
	registerCardsAgentTools(r, isAdmin, a.svc)
}

func (a *CardsApp) RegisterMCPTools(_ *pgxpool.Pool, svc *services.Services) []mcpserver.ServerTool {
	a.svc.enc = svc.Enc
	return buildCardsMCPTools(a.svc)
}

func (a *CardsApp) RegisterRoutes(mux *http.ServeMux) {
	registerCardsRoutes(mux, a)
}

func (a *CardsApp) CronJobs() []apps.CronJob {
	return nil
}

// Tool names exposed by this app. Kept as constants so the switch in
// agent.go, the metadata here, the MCP wiring, and any tests reference
// the same string — rename surfaces everywhere through the compiler.
const (
	ToolCreateDecision        = "create_decision"
	ToolCreateBriefing        = "create_briefing"
	ToolUpdateDecision        = "update_decision"
	ToolUpdateBriefing        = "update_briefing"
	ToolListDecisions         = "list_decisions"
	ToolListBriefings         = "list_briefings"
	ToolAckBriefing           = "ack_briefing"
	ToolResolveDecision       = "resolve_decision"
	ToolReviseDecisionOption  = "revise_decision_option"
	ToolGetDecisionToolResult = "get_decision_tool_result"
)

// cardsTools is the shared metadata. Schemas are intentionally tight so the
// LLM gets strong hints about valid enum values.
var cardsTools = []services.ToolMeta{
	{
		Name: ToolCreateDecision,
		Description: "Create a decision card. The user will see this in their swipe stack and pick an option. " +
			"An option can carry a concrete tool call (tool_name + tool_arguments) that Kit runs with the user's approved input " +
			"when they tap Approve. It can also carry a 'prompt' — but note: prompt is a POST-execution follow-up, fed to the agent " +
			"AFTER the tool runs. Use prompt for chained work (\"after sending, mark todo X complete\"). Leave both empty for a 'Skip' / noop option. " +
			"Set recommended_option_id to the option the user should swipe-right to approve.",
		Schema: services.PropsReq(map[string]any{
			"title":   services.Field("string", "Short heading, <=60 chars"),
			"context": services.Field("string", "Markdown framing the user needs to decide"),
			"options": map[string]any{
				"type": "array",
				"description": "2-4 options. Each has option_id (stable string), label (button text), " +
					"optional tool_name + tool_arguments (the concrete action to run on approval), and optional prompt (POST-execution follow-up for chained agent work). " +
					"A noop 'Skip' option leaves all of tool_name / tool_arguments / prompt empty.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"option_id": services.Field("string", "Stable id (e.g. 'send', 'skip')"),
						"label":     services.Field("string", "Button text"),
						"tool_name": services.Field("string",
							"Name of a registered tool to execute when this option is approved. Empty for no tool call. "+
								"PolicyGate tools (e.g. send_email) auto-populate this; PolicyAllow tools can be gated voluntarily for uncertain cases."),
						"tool_arguments": map[string]any{
							"type":        "object",
							"description": "JSON object of arguments matching the tool's schema. Required when tool_name is set.",
						},
						"prompt": services.Field("string",
							"POST-execution follow-up: markdown instructions run by a one-shot agent AFTER tool_name executes. "+
								"Use for chained work (e.g. 'after sending, mark todo {id} complete'). Empty = no follow-up."),
					},
					"required": []string{"option_id", "label"},
				},
			},
			"recommended_option_id": services.Field("string", "Which option the user should swipe-right for"),
			"priority":              services.Field("string", "Priority: low, medium, high"),
			"role_scopes": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Roles allowed to see this card. Empty = visible to everyone in tenant.",
			},
		}, "title", "context", "options"),
	},
	{
		Name:        ToolCreateBriefing,
		Description: "Create a briefing card — an informational update. Put any links in the markdown body for now. Use severity info (default), notable, or important.",
		Schema: services.PropsReq(map[string]any{
			"title":    services.Field("string", "Short heading"),
			"body":     services.Field("string", "Markdown body"),
			"severity": services.Field("string", "Severity: info, notable, important"),
			"role_scopes": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Roles allowed to see this briefing. Empty = everyone in tenant.",
			},
		}, "title", "body"),
	},
	{
		Name: ToolUpdateDecision,
		Description: "Update non-option fields on a decision card (title, context, priority, state transitions). " +
			"Options cannot be replaced via this tool on a pending card — use revise_decision_option to edit per-option tool_arguments or prompt. " +
			"Options replacement via this tool is allowed only on non-pending (e.g. resolved, admin-rewritten) cards.",
		Schema: services.PropsReq(map[string]any{
			"card_id":               services.Field("string", "Card UUID"),
			"title":                 services.Field("string", "New title"),
			"context":               services.Field("string", "New markdown context"),
			"priority":              services.Field("string", "low, medium, high"),
			"recommended_option_id": services.Field("string", "New recommended option id"),
			"options": map[string]any{
				"type":        "array",
				"description": "Full replacement list of options",
				"items":       map[string]any{"type": "object"},
			},
			"state": services.Field("string", "pending, resolved, cancelled"),
			"role_scopes": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		}, "card_id"),
	},
	{
		Name:        ToolUpdateBriefing,
		Description: "Update fields on a briefing. Supply only fields you want to change.",
		Schema: services.PropsReq(map[string]any{
			"card_id":  services.Field("string", "Card UUID"),
			"title":    services.Field("string", "New title"),
			"body":     services.Field("string", "New markdown body"),
			"severity": services.Field("string", "info, notable, important"),
			"state":    services.Field("string", "pending, archived, dismissed, saved"),
			"role_scopes": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		}, "card_id"),
	},
	{
		Name:        ToolListDecisions,
		Description: "List decisions visible to the caller, with optional state/priority filters.",
		Schema: services.Props(map[string]any{
			"state":    services.Field("string", "pending, resolved, cancelled"),
			"priority": services.Field("string", "low, medium, high"),
		}),
	},
	{
		Name:        ToolListBriefings,
		Description: "List briefings visible to the caller, with optional state/severity filters.",
		Schema: services.Props(map[string]any{
			"state":    services.Field("string", "pending, archived, dismissed, saved"),
			"severity": services.Field("string", "info, notable, important"),
		}),
	},
	{
		Name:        ToolAckBriefing,
		Description: "Acknowledge a briefing, moving it out of the pending stack. kind is archived (seen, useful), dismissed (seen, not useful), or saved (flag for later).",
		Schema: services.PropsReq(map[string]any{
			"card_id": services.Field("string", "Briefing card UUID"),
			"kind":    services.Field("string", "archived, dismissed, saved"),
		}, "card_id", "kind"),
	},
	{
		Name: ToolResolveDecision,
		Description: "Resolve a decision card by picking one of its options. If option_id is omitted, the recommended_option_id is used. " +
			"If the option has a tool_name, Kit executes that tool with the (possibly user-revised) tool_arguments. If the option also " +
			"has a prompt, a follow-up agent task is queued with the tool result in context. Prompt-only options (no tool_name) keep " +
			"the legacy behavior of queuing an agent task with the prompt as input.",
		Schema: services.PropsReq(map[string]any{
			"card_id":   services.Field("string", "Decision card UUID"),
			"option_id": services.Field("string", "Option to pick (defaults to recommended_option_id)"),
		}, "card_id"),
	},
	{
		Name: ToolReviseDecisionOption,
		Description: "Revise an option's tool_arguments and/or prompt on a pending decision card. Use this when the user gives " +
			"feedback on the proposed action (e.g. 'change the email subject', 'drop the last paragraph'). tool_name, label, " +
			"option_id, and sort_order cannot be changed — only tool_arguments and prompt. Omit a field to leave it unchanged.",
		Schema: services.PropsReq(map[string]any{
			"card_id":   services.Field("string", "Decision card UUID"),
			"option_id": services.Field("string", "Which option to revise (must already exist on the card)"),
			"tool_arguments": map[string]any{
				"type":        "object",
				"description": "Revised JSON arguments for the option's existing tool_name. Omit to leave unchanged.",
			},
			"prompt": services.Field("string", "Revised post-execution follow-up prompt. Omit to leave unchanged; empty string clears it."),
		}, "card_id", "option_id"),
	},
	{
		Name: ToolGetDecisionToolResult,
		Description: "Fetch the full tool output from a resolved decision card. Use this when the session-replay 2KB-truncated " +
			"version is insufficient — e.g. the follow-up agent needs the full list returned by a gated tool.",
		Schema: services.PropsReq(map[string]any{
			"card_id": services.Field("string", "Resolved decision card UUID"),
		}, "card_id"),
	},
}
