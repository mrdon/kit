// Package email contributes provider-agnostic IMAP + SMTP tools to the
// Kit agent. Credentials live in the integrations substrate (provider=
// "email", auth_type="imap_smtp") so the LLM never sees the password.
// send_email is PolicyGate — every send routes through a decision card.
package email

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/integrations"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
	integrations.RegisterTypeSpec(typeSpec())
}

// App is the email feature app. It ships four agent tools (three
// PolicyAllow reads + one PolicyGate send) and no MCP or HTTP surface —
// config happens through the integrations substrate.
type App struct {
	pool *pgxpool.Pool
	enc  *crypto.Encryptor
}

// Configure wires the encryptor the send/read handlers need to decrypt
// the user's password. Called once from main.go after crypto.NewEncryptor.
func Configure(enc *crypto.Encryptor) {
	if instance == nil {
		return
	}
	instance.enc = enc
}

// Init caches the pool. Called by apps.Init().
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
}

func (a *App) Name() string { return "email" }

func (a *App) SystemPrompt() string {
	return `## Email
You can read and send email for the current user when their email account is configured via the integrations flow.

- search_emails supports ` + "`from:`, `to:`, `subject:`, `since:YYYY-MM-DD`, `is:unread`, `is:read`" + ` — anything else is a full-text body search. It does NOT understand Gmail operators like ` + "`newer_than:`, `has:attachment`, `in:sent`, `in:anywhere`" + `; those fall through to a useless literal body search. Use ` + "`since:YYYY-MM-DD`" + ` for date filters and the ` + "`folder`" + ` parameter to switch mailboxes.
- search_emails defaults to INBOX. To find messages the user *sent* (e.g. "did I reply to X?"), pass ` + "`folder='[Gmail]/Sent Mail'`" + ` on Gmail accounts, or ` + "`folder='Sent'`" + ` elsewhere. Check both when unsure.
- search_emails returns one line per message with ` + "`uid=N`" + `. To see the full body, call ` + "`read_email(uid=N)`" + ` — don't guess from the snippet.
- send_email body is markdown. Prefer short paragraphs, bullet lists, and ` + "`**bold**`" + ` over ad-hoc HTML. The server renders to both plain text and sanitized HTML.
- send_email ALWAYS creates a decision card first — when its result starts with HALTED: tell the user you've queued it for their review. Never claim the email was sent.
- Threading: pass in_reply_to (and references) when replying so clients render a proper thread.
- If search_emails returns "email account not configured", suggest they run ` + "`configure_integration(provider=\"email\", auth_type=\"imap_smtp\")`" + ` to set it up.`
}

func (a *App) ToolMetas() []services.ToolMeta {
	return emailTools
}

// RegisterAgentTools only registers the email tools if the caller has
// an email integration row configured. An unconfigured user gets a
// registry with no search/read/send tools — they route through the
// always-available configure_integration flow instead.
func (a *App) RegisterAgentTools(ctx context.Context, registerer any, caller *services.Caller, _ bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok {
		return
	}
	if !a.callerHasAccount(ctx, caller) {
		return
	}
	registerEmailAgentTools(r, a)
}

// callerHasAccount checks whether the given caller has a configured
// email integration. Returns false on nil caller, unconfigured app, or
// DB error — safe-default: hide the tools if we can't be sure they'll
// work.
func (a *App) callerHasAccount(ctx context.Context, caller *services.Caller) bool {
	if caller == nil || a.pool == nil {
		return false
	}
	uid := caller.UserID
	integ, err := models.GetIntegration(ctx, a.pool, caller.TenantID, Provider, AuthType, &uid)
	if err != nil {
		slog.Warn("email: checking integration row for caller", "tenant_id", caller.TenantID, "user_id", caller.UserID, "error", err)
		return false
	}
	return integ != nil
}

// RegisterMCPTools deliberately returns nil. send_email is PolicyGate and
// per .claude/skills/gated-tools-guide.md must not be reachable except via
// tools.Registry.Execute. The reads have no MCP-side use case.
func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return nil
}

func (a *App) RegisterRoutes(_ *http.ServeMux) {}

func (a *App) CronJobs() []apps.CronJob { return nil }

// typeSpec declares the integrations TypeSpec for ("email", "imap_smtp").
// Called at init(). The form that renders from this is handled entirely
// by the integrations substrate.
func typeSpec() integrations.TypeSpec {
	return integrations.TypeSpec{
		Provider:    Provider,
		AuthType:    AuthType,
		DisplayName: "Email (IMAP + SMTP)",
		Description: "Personal mailbox for reading and sending. For Gmail, enable 2FA then generate an app password at https://myaccount.google.com/apppasswords.",
		Scope:       integrations.ScopeUser,
		Fields: []integrations.FieldSpec{
			{Name: "email_address", Label: "Email address", InputType: "text", Target: integrations.TargetUsername, Required: true},
			{Name: "password", Label: "Password / app password", InputType: "password", Target: integrations.TargetPrimaryToken, Required: true, Help: "For Gmail this must be an app password — 16 characters, no spaces."},
			{Name: "imap_host", Label: "IMAP host", InputType: "text", Required: true, Help: "e.g. imap.gmail.com"},
			{Name: "imap_port", Label: "IMAP port", InputType: "text", Required: false, Help: "993 for TLS (Gmail), 143 for STARTTLS.", Default: "993"},
			{Name: "smtp_host", Label: "SMTP host", InputType: "text", Required: true, Help: "e.g. smtp.gmail.com"},
			{Name: "smtp_port", Label: "SMTP port", InputType: "text", Required: false, Help: "587 for STARTTLS (Gmail), 465 for implicit TLS.", Default: "587"},
			{
				Name: "from_name", Label: "From display name", InputType: "text", Required: false,
				DefaultBuilder: func(dc integrations.DefaultContext) string { return dc.UserDisplayName },
			},
			{
				Name: "signature", Label: "Signature", InputType: "textarea", Required: false,
				Help:           "Appended to every outgoing email. Edit or clear as you prefer.",
				DefaultBuilder: defaultSignature,
			},
			// Advanced: port heuristic covers real providers (993/465 → TLS,
			// everything else → STARTTLS). Override only for non-standard
			// setups — self-hosted / test servers without STARTTLS.
			{Name: "imap_security", Label: "IMAP security", InputType: "text", Required: false, Help: "Leave blank for auto. Values: tls, starttls, none.", Advanced: true},
			{Name: "smtp_security", Label: "SMTP security", InputType: "text", Required: false, Help: "Leave blank for auto. Values: tls, starttls, none.", Advanced: true},
		},
	}
}

// defaultSignature builds the prefill for the signature textarea. Uses
// the user's first name when we have it so the AI-disclosure line reads
// naturally; falls back to a generic phrasing otherwise.
func defaultSignature(dc integrations.DefaultContext) string {
	name := dc.UserFirstName
	if name == "" {
		name = "the sender"
	}
	return "---\nDrafted with Kit AI and sent with " + name + "'s approval."
}

var emailTools = []services.ToolMeta{
	{
		Name:        "search_emails",
		Description: "Search the current user's inbox (or another folder). Empty query returns the newest messages. On Gmail accounts, pass Gmail query syntax (e.g. \"has:attachment from:boss newer_than:7d\") — it's evaluated server-side. On non-Gmail accounts, supports `from:`, `to:`, `subject:`, `since:YYYY-MM-DD`, and `is:unread`; anything else falls back to full-text search.",
		Schema: services.Props(map[string]any{
			"query":       services.Field("string", "Search criteria. Empty returns newest messages."),
			"folder":      services.Field("string", "IMAP folder name. Defaults to INBOX."),
			"since":       services.Field("string", "Only messages on/after this date (YYYY-MM-DD)."),
			"unread_only": map[string]any{"type": "boolean", "description": "Only messages without the \\Seen flag."},
			"limit":       map[string]any{"type": "integer", "description": "Max rows (default 20, max 100)."},
		}),
	},
	{
		Name:        "read_email",
		Description: "Fetch the full plaintext body, headers, and attachment filenames for one message by UID.",
		Schema: services.PropsReq(map[string]any{
			"uid":    map[string]any{"type": "integer", "description": "The IMAP UID from search_emails."},
			"folder": services.Field("string", "IMAP folder name. Defaults to INBOX."),
		}, "uid"),
	},
	{
		Name:        "mark_read",
		Description: "Set or clear the \\Seen flag on a message.",
		Schema: services.PropsReq(map[string]any{
			"uid":    map[string]any{"type": "integer", "description": "The IMAP UID."},
			"read":   map[string]any{"type": "boolean", "description": "true to mark read (default), false to mark unread."},
			"folder": services.Field("string", "IMAP folder name. Defaults to INBOX."),
		}, "uid"),
	},
	{
		Name:        "send_email",
		Description: "Send an email from the current user's configured account. Body is markdown — rendered to both plain text and sanitized HTML at send time. REQUIRES HUMAN APPROVAL via a decision card; the call returns HALTED until the user approves.",
		Schema: services.PropsReq(map[string]any{
			"to":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Recipient addresses."},
			"cc":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "CC addresses."},
			"bcc":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "BCC addresses."},
			"subject":     services.Field("string", "Subject line."),
			"body":        services.Field("string", "Body in markdown. Prefer short paragraphs, bullet lists, and **bold** over raw HTML."),
			"in_reply_to": services.Field("string", "Message-ID of the email this replies to (include angle brackets)."),
			"references":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Full References header chain (each item includes angle brackets)."},
		}, "to", "subject", "body"),
	},
}
