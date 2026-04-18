package cards

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
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

func (a *CardsApp) RegisterAgentTools(registerer any, isAdmin bool) {
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

// cardsTools is the shared metadata. Schemas are intentionally tight so the
// LLM gets strong hints about valid enum values.
var cardsTools = []services.ToolMeta{
	{
		Name:        "create_decision",
		Description: "Create a decision card. The user will see this in their swipe stack and pick an option. Each option carries a 'prompt' fed to the agent as a one-shot task when chosen; leave prompt empty for a noop option (e.g. 'skip'). Set recommended_option_id to the option the user should swipe-right to approve.",
		Schema: services.PropsReq(map[string]any{
			"title":   services.Field("string", "Short heading, <=60 chars"),
			"context": services.Field("string", "Markdown framing the user needs to decide"),
			"options": map[string]any{
				"type":        "array",
				"description": "2-4 options. Each has option_id (stable string), label (button text), and prompt (markdown description for the agent, empty = noop).",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"option_id": services.Field("string", "Stable id (e.g. 'send', 'skip')"),
						"label":     services.Field("string", "Button text"),
						"prompt":    services.Field("string", "Markdown instruction for the agent when chosen. Empty = noop."),
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
		Name:        "create_briefing",
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
		Name:        "update_decision",
		Description: "Update fields on a pending decision. Supply only the fields you want to change. Setting options replaces the full list.",
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
		Name:        "update_briefing",
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
		Name:        "list_decisions",
		Description: "List decisions visible to the caller, with optional state/priority filters.",
		Schema: services.Props(map[string]any{
			"state":    services.Field("string", "pending, resolved, cancelled"),
			"priority": services.Field("string", "low, medium, high"),
		}),
	},
	{
		Name:        "list_briefings",
		Description: "List briefings visible to the caller, with optional state/severity filters.",
		Schema: services.Props(map[string]any{
			"state":    services.Field("string", "pending, archived, dismissed, saved"),
			"severity": services.Field("string", "info, notable, important"),
		}),
	},
	{
		Name:        "ack_briefing",
		Description: "Acknowledge a briefing, moving it out of the pending stack. kind is archived (seen, useful), dismissed (seen, not useful), or saved (flag for later).",
		Schema: services.PropsReq(map[string]any{
			"card_id": services.Field("string", "Briefing card UUID"),
			"kind":    services.Field("string", "archived, dismissed, saved"),
		}, "card_id", "kind"),
	},
	{
		Name:        "resolve_decision",
		Description: "Resolve a decision card by picking one of its options. If option_id is omitted, the recommended_option_id is used. If the chosen option has a non-empty prompt, Kit queues a one-shot agent task that runs the prompt and posts output to the caller's Slack DM.",
		Schema: services.PropsReq(map[string]any{
			"card_id":   services.Field("string", "Decision card UUID"),
			"option_id": services.Field("string", "Option to pick (defaults to recommended_option_id)"),
		}, "card_id"),
	},
}
