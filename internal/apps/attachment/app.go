// Package attachment is Kit's general "files attached to a conversation"
// capability: the read_attachment tool (agent + MCP) that materializes an
// attachment as text on demand, plus a signed-token serve route for <img>
// thumbnails. The byte store itself lives in internal/attachment (imported
// here as store); this app is the tool + HTTP surface over it.
package attachment

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	store "github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App wires the read_attachment tool and the attachment serve route.
type App struct {
	pool      *pgxpool.Pool
	enc       *crypto.Encryptor
	llm       *anthropic.Client
	deepLinks *auth.DeepLinkSigner
}

// Configure wires the encryptor (for byte encryption), the deep-link signer
// (for repeatable, non-CSRF serve tokens), and the LLM client (for image
// transcription). Call once from main.go after these are constructed.
func Configure(enc *crypto.Encryptor, deepLinks *auth.DeepLinkSigner, llm *anthropic.Client) {
	if instance == nil {
		return
	}
	instance.enc = enc
	instance.deepLinks = deepLinks
	instance.llm = llm
}

// Instance returns the singleton app so other packages (chat orchestrator,
// console) can mint serve URLs via SignedURL.
func Instance() *App { return instance }

// Init records the DB pool once it's available.
func (a *App) Init(pool *pgxpool.Pool) { a.pool = pool }

func (a *App) Name() string                   { return "attachment" }
func (a *App) SystemPrompt() string           { return "" }
func (a *App) ToolMetas() []services.ToolMeta { return attachmentTools }
func (a *App) CronJobs() []apps.CronJob       { return nil }

// service builds an attachment store on demand. Returns nil until both the
// pool (Init) and encryptor (Configure) are wired.
func (a *App) service() *store.Service {
	if a.pool == nil || a.enc == nil {
		return nil
	}
	return store.NewService(a.pool, a.enc)
}

// sender returns a true-nil interface when no LLM is configured, so the
// nil-check in readAttachment is correct (a typed-nil *Client wrapped in an
// interface would not compare == nil).
func (a *App) sender() anthropic.Sender {
	if a.llm == nil {
		return nil
	}
	return a.llm
}

func (a *App) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, _ bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok {
		return
	}
	registerAgentTool(r, a)
}

func (a *App) RegisterMCPTools(pool *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	if a.pool == nil {
		a.pool = pool
	}
	return buildMCPTools(a)
}

func (a *App) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /{slug}/apps/attachment/{id}", http.HandlerFunc(a.handleServe))
}
