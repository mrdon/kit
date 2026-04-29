package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools"
	"github.com/mrdon/kit/internal/tools/approval"
	"github.com/mrdon/kit/internal/web"
)

// snapshotToolPolicies builds a static snapshot of tool-name ->
// DefaultPolicy by constructing a throwaway non-admin registry at
// startup. DefaultPolicy is set on each Def by its registering
// package, so it's independent of the caller — we only need one
// snapshot. Tools registered only for admins aren't in this map, but
// no admin-only tool is expected to be PolicyGate in MVP (admins
// already have broad privileges; gating is for operations that cross
// a trust boundary regardless of who invokes them).
//
// Dynamic (builder-exposed) tools never set DefaultPolicy so they're
// implicitly PolicyAllow — safe to miss here.
func snapshotToolPolicies(ctx context.Context) map[string]tools.Policy {
	// Build a throwaway registry using an admin caller so we see every
	// registered tool (AdminOnly defs are included). DefaultPolicy
	// lives on Def and doesn't vary by caller, so one snapshot is
	// sufficient. Dynamic (builder-exposed) tools never set
	// DefaultPolicy so they're implicitly PolicyAllow — safe to miss.
	dummy := &services.Caller{IsAdmin: true}
	reg := tools.NewRegistry(ctx, dummy, false)
	return reg.Policies()
}

// buildResolveToolExecutor returns the callback CardService invokes
// when a user approves a gated decision option. It builds a
// per-caller tools.Registry, constructs an ExecContext with
// approval.WithToken attached to ctx, and dispatches the tool through
// the normal Registry.ExecuteWithResult path. The approval token
// makes the PolicyGate check pass; the resolve token threads through
// to the handler as the idempotency key.
//
// We load tenant + user from the pool to populate ExecContext.
// Session is nil — handlers for PolicyGate tools shouldn't depend on
// per-session state (they're approved one-shots). If a future gated
// tool needs a session for logging, introduce a synthetic resolve
// session at that point.
func buildResolveToolExecutor(pool *pgxpool.Pool, svc *services.Services, enc *crypto.Encryptor, fetcher *web.Fetcher, llm *anthropic.Client) func(
	ctx context.Context, caller *services.Caller,
	cardID, resolveToken uuid.UUID,
	toolName string, toolArguments json.RawMessage,
) (string, bool, error) {
	return func(
		ctx context.Context, caller *services.Caller,
		cardID, resolveToken uuid.UUID,
		toolName string, toolArguments json.RawMessage,
	) (string, bool, error) {
		tenant, err := models.GetTenantByID(ctx, pool, caller.TenantID)
		if err != nil {
			return "", false, fmt.Errorf("loading tenant: %w", err)
		}
		if tenant == nil {
			return "", false, fmt.Errorf("tenant %s not found", caller.TenantID)
		}
		user, err := models.GetUserByID(ctx, pool, caller.TenantID, caller.UserID)
		if err != nil {
			return "", false, fmt.Errorf("loading user: %w", err)
		}
		if user == nil {
			return "", false, fmt.Errorf("user %s not found", caller.UserID)
		}

		// Attach approval token BEFORE building the registry so any
		// tool that inspects ctx (future gate operations should) sees
		// the approval marker too.
		approvedCtx := approval.WithToken(ctx, approval.Mint(cardID, resolveToken))

		botToken, err := enc.Decrypt(tenant.BotToken)
		if err != nil {
			return "", false, fmt.Errorf("decrypting bot token: %w", err)
		}

		reg := tools.NewRegistry(approvedCtx, caller, false /* not bot-initiated */)
		ec := &tools.ExecContext{
			Ctx:     approvedCtx,
			Pool:    pool,
			Slack:   kitslack.NewClient(botToken),
			Fetcher: fetcher,
			Tenant:  tenant,
			User:    user,
			Svc:     svc,
			LLM:     llm,
			// Session intentionally nil — see comment above.
		}
		res, err := reg.ExecuteWithResult(ec, toolName, toolArguments)
		if err != nil {
			slog.Warn("gated tool execution failed", "tool", toolName, "card_id", cardID, "error", err)
			return "", res.Halted, err
		}
		return res.Output, res.Halted, nil
	}
}
