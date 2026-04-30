package voting

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

var instance *VotingApp

func init() {
	instance = &VotingApp{}
	apps.Register(instance)
}

// VotingApp implements the standalone proposal-vote workflow. The
// participant ask is a decision card scoped to that participant; on
// resolve the verdict and any chat-attached reason land in
// app_vote_participants. Completion (all-resolved or deadline) surfaces
// a digest decision card to the organizer.
type VotingApp struct {
	pool   *pgxpool.Pool
	cards  *cards.CardService
	svc    *Service
	engine *Engine
}

// Configure wires the runtime card service. Call once from main.go
// after the cards app's service is constructed. CardService is the
// only external dependency — voting doesn't drive Slack DMs.
func Configure(cardSvc *cards.CardService) {
	if instance == nil {
		return
	}
	instance.cards = cardSvc
}

// Init sets up the service after the DB pool is available.
func (a *VotingApp) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.svc = newService(pool, a)
	a.engine = newEngine(pool, a)
}

func (a *VotingApp) Name() string { return "voting" }

func (a *VotingApp) SystemPrompt() string {
	return mustRender("system_prompt.tmpl", nil)
}

func (a *VotingApp) ToolMetas() []services.ToolMeta {
	return votingTools
}

func (a *VotingApp) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r := registerer.(*tools.Registry)
	registerVotingAgentTools(r, isAdmin, a.svc)
}

func (a *VotingApp) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildVotingMCPTools(a.svc)
}

func (a *VotingApp) RegisterRoutes(_ *http.ServeMux) {
	// No HTTP routes for voting.
}

func (a *VotingApp) CronJobs() []apps.CronJob {
	return []apps.CronJob{
		{
			Name:     "voting_sweep",
			Interval: 60 * time.Second,
			Run:      a.cronSweep,
		},
	}
}

func (a *VotingApp) cronSweep(ctx context.Context, _ *pgxpool.Pool, _ *crypto.Encryptor) error {
	if a.engine == nil {
		return nil
	}
	return a.engine.Tick(ctx)
}
