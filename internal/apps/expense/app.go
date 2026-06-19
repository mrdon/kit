package expense

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

var instance *ExpenseApp

func init() {
	instance = &ExpenseApp{}
	apps.Register(instance)
}

// ExpenseApp is the expense-reports app: capture receipts (as chat
// attachments, read via the attachment app's vision tool), group them into a
// report, and route the report for approval via a decision card.
type ExpenseApp struct {
	svc           *ExpenseService
	llm           *anthropic.Client
	cards         CardSurface
	enc           *crypto.Encryptor
	signer        *auth.SessionSigner
	intakeLimiter *rateLimiter
}

// CardSurface raises the approval decision card when a report is submitted.
// Implemented by an adapter in cmd/kit that wraps *cards.CardService, so this
// app stays decoupled from the cards package (same pattern as vault).
type CardSurface interface {
	// CreateApprovalDecision raises a decision card scoped to the approver
	// role and returns the new card's id. The two options must carry the
	// approve/reject tool names + {"report_id": ...} so ResolveDecision can
	// fire ApproveReport/RejectReport.
	CreateApprovalDecision(ctx context.Context, tenantID uuid.UUID, in ApprovalDecisionInput) (uuid.UUID, error)
}

// ApprovalDecisionInput is the surface-agnostic payload for the approval card.
// Exactly one of ApproverUserID / ApproverRoleName targets the card: a specific
// assigned approver takes precedence; otherwise it routes to the owning role.
type ApprovalDecisionInput struct {
	Title            string
	Body             string
	ApproverUserID   *uuid.UUID
	ApproverRoleName string
	ReportID         uuid.UUID
}

// Configure wires the anthropic client (receipt categorizer), the card
// surface (approval decisions), the encryptor, and the session signer (console
// routes). Call once from main.go after services.New. Safe to omit in tests.
func Configure(llm *anthropic.Client, cards CardSurface, enc *crypto.Encryptor, signer *auth.SessionSigner) {
	if instance == nil {
		return
	}
	instance.llm = llm
	instance.cards = cards
	instance.enc = enc
	instance.signer = signer
}

// Init sets up the service once the DB is available.
func (a *ExpenseApp) Init(pool *pgxpool.Pool) {
	a.svc = &ExpenseService{pool: pool, app: a}
	// Public-intake spam guard: cap submissions per IP. Generous — the approval
	// swipe is the real control, this just blunts drive-by bots.
	a.intakeLimiter = newRateLimiter(20, time.Minute)
	apps.RegisterCardProvider(&cardProvider{app: a})
}

func (a *ExpenseApp) Name() string { return "expense" }

func (a *ExpenseApp) SystemPrompt() string {
	return mustRender("system_prompt.tmpl", nil)
}

func (a *ExpenseApp) ToolMetas() []services.ToolMeta {
	return expenseTools
}

func (a *ExpenseApp) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r := registerer.(*tools.Registry)
	registerExpenseAgentTools(r, isAdmin, a.svc)
}

func (a *ExpenseApp) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildExpenseMCPTools(a.svc)
}

func (a *ExpenseApp) RegisterRoutes(mux *http.ServeMux) {
	if a.svc == nil {
		return
	}
	// Public intake (tenant-from-slug, no session) only needs the pool + enc.
	registerIntakeRoutes(mux, a)
	if a.signer == nil {
		return
	}
	registerExpenseRoutes(mux, a)
}

func (a *ExpenseApp) CronJobs() []apps.CronJob {
	return nil
}
