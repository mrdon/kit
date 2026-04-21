// Integration tests for the decision-cards-as-gated-tool-calls flow.
// Exercises Registry.Execute's gate interception, ResolveDecision's
// re-check + intermediate resolving state + approved tool invocation,
// the stuck-resolving sweep, and handler-side idempotency — without
// waiting on the email-app PR that adds send_email.
//
// The test-only _test_gated_echo tool from internal/tools/testgated
// stands in for a real PolicyGate tool. It is never registered in
// production registries.
package cards_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
	"github.com/mrdon/kit/internal/tools"
	"github.com/mrdon/kit/internal/tools/approval"
	"github.com/mrdon/kit/internal/tools/testgated"
)

// gatedFixture bundles everything a gated-tool test needs: a fresh
// tenant+user, a CardService configured with policy/tool-executor
// pointing at the testgated tool, and a process-wide GateCreator
// restore. It intentionally does not touch cards.instance (the app
// singleton) so tests don't fight over it.
type gatedFixture struct {
	pool     *pgxpool.Pool
	ctx      context.Context
	tenantID uuid.UUID
	userID   uuid.UUID
	caller   *services.Caller
	svc      *cards.CardService
	dedupe   *testgated.Dedupe
}

// newFixture wires a gated-tool test. The returned fixture snapshots
// tools.currentGateCreator for t.Cleanup so parallel tests don't
// leak state.
func newFixture(t *testing.T) *gatedFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_gated_" + uuid.NewString()
	slug := models.SanitizeSlug("gated-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "gated-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})
	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_"+uuid.NewString()[:8], "Gated Tester", false)
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	roleNames, _ := models.GetUserRoleNames(ctx, pool, tenant.ID, user.ID, tenant.DefaultRoleID)
	roleIDs, _ := models.GetUserRoleIDs(ctx, pool, tenant.ID, user.ID, tenant.DefaultRoleID)
	caller := &services.Caller{
		TenantID: tenant.ID,
		UserID:   user.ID,
		Identity: user.SlackUserID,
		Roles:    roleNames,
		RoleIDs:  roleIDs,
	}

	svc := cards.NewService(pool)
	dedupe := testgated.NewDedupe()
	gatedDef := testgated.NewDef(dedupe)

	// Policy lookup only knows about the test tool; everything else
	// implicitly resolves to PolicyAllow via the zero-value default.
	svc.ConfigurePolicyLookup(func(name string) tools.Policy {
		if name == testgated.ToolName {
			return tools.PolicyGate
		}
		return tools.PolicyAllow
	})

	// ToolExecutor: attach approval token to ctx, build a minimal
	// registry with just the test tool, dispatch.
	svc.ConfigureToolExecutor(func(
		ctx context.Context, c *services.Caller,
		cardID, resolveToken uuid.UUID,
		toolName string, toolArguments json.RawMessage,
	) (string, bool, error) {
		approvedCtx := approval.WithToken(ctx, approval.Mint(cardID, resolveToken))
		reg := tools.NewRegistry(approvedCtx, c, false)
		reg.Register(gatedDef)
		ec := &tools.ExecContext{
			Ctx:    approvedCtx,
			Pool:   pool,
			Tenant: tenant,
			User:   user,
		}
		res, err := reg.ExecuteWithResult(ec, toolName, toolArguments)
		return res.Output, res.Halted, err
	})

	// GateCreator: use the test's svc as the sink. Restore whatever
	// was wired before (production wires cards.instance.svc) on
	// cleanup so later tests in the same run aren't poisoned.
	tools.SetGateCreator(svc)
	t.Cleanup(func() { tools.SetGateCreator(nil) })

	return &gatedFixture{
		pool:     pool,
		ctx:      ctx,
		tenantID: tenant.ID,
		userID:   user.ID,
		caller:   caller,
		svc:      svc,
		dedupe:   dedupe,
	}
}

// makeArgs marshals a map to json.RawMessage; helper to keep test
// bodies readable.
func makeArgs(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshaling args: %v", err)
	}
	return b
}

// TestGateWrapsDirectCall exercises Registry.Execute's interception:
// an agent calling a PolicyGate tool without approval should trigger
// a decision card (not run the handler) and return HALTED.
func TestGateWrapsDirectCall(t *testing.T) {
	f := newFixture(t)

	reg := tools.NewRegistry(f.ctx, f.caller, false)
	reg.Register(testgated.NewDef(f.dedupe))

	// Build ec WITHOUT attaching an approval token.
	tenant, _ := models.GetTenantByID(f.ctx, f.pool, f.tenantID)
	user, _ := models.GetUserByID(f.ctx, f.pool, f.tenantID, f.userID)
	ec := &tools.ExecContext{
		Ctx:    f.ctx,
		Pool:   f.pool,
		Tenant: tenant,
		User:   user,
	}
	res, err := reg.ExecuteWithResult(ec, testgated.ToolName, makeArgs(t, map[string]any{"text": "hello"}))
	if err != nil {
		t.Fatalf("ExecuteWithResult returned error: %v", err)
	}
	if !res.Halted {
		t.Fatalf("expected Halted=true for gated call, got output=%q", res.Output)
	}
	if !strings.HasPrefix(res.Output, tools.HaltedPrefix) {
		t.Fatalf("expected output to start with %q, got %q", tools.HaltedPrefix, res.Output)
	}
	if f.dedupe.Replayed(uuid.Nil) {
		t.Fatalf("handler should not have executed on a gated call")
	}
}

// TestApproveRunsToolWithApprovedCtx walks the full loop: gate a call
// (producing a card with is_gate_artifact=true), approve it via the
// service, assert the tool ran with the original args and wrote a
// resolved result onto the card.
func TestApproveRunsToolWithApprovedCtx(t *testing.T) {
	f := newFixture(t)

	reg := tools.NewRegistry(f.ctx, f.caller, false)
	reg.Register(testgated.NewDef(f.dedupe))

	tenant, _ := models.GetTenantByID(f.ctx, f.pool, f.tenantID)
	user, _ := models.GetUserByID(f.ctx, f.pool, f.tenantID, f.userID)
	ec := &tools.ExecContext{Ctx: f.ctx, Pool: f.pool, Tenant: tenant, User: user}

	// Step 1: trigger the gate. Output carries the card id.
	args := makeArgs(t, map[string]any{"text": "hi jim", "marker": "one"})
	gateRes, err := reg.ExecuteWithResult(ec, testgated.ToolName, args)
	if err != nil {
		t.Fatalf("gate execute: %v", err)
	}
	if !gateRes.Halted {
		t.Fatalf("expected halted; got %q", gateRes.Output)
	}
	cardID := parseCardIDFromHalted(t, gateRes.Output)

	// Confirm is_gate_artifact stamped by CreateDecision.
	card, err := f.svc.Get(f.ctx, f.caller, cardID)
	if err != nil {
		t.Fatalf("loading gate card: %v", err)
	}
	if card.Decision == nil || !card.Decision.IsGateArtifact {
		t.Fatalf("expected is_gate_artifact=true on gate card")
	}
	if card.Decision.RecommendedOptionID != "approve" {
		t.Fatalf("expected recommended_option_id=approve, got %q", card.Decision.RecommendedOptionID)
	}

	// Step 2: approve (optionID="approve"). ResolveDecision re-checks
	// policy, flips to resolving, runs tool outside tx, writes result.
	if _, err := f.svc.ResolveDecision(f.ctx, f.caller, cardID, "approve", &stubDM{}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Step 3: inspect the card — state resolved, tool result recorded.
	resolved, err := f.svc.Get(f.ctx, f.caller, cardID)
	if err != nil {
		t.Fatalf("loading resolved card: %v", err)
	}
	if resolved.State != cards.CardStateResolved {
		t.Fatalf("expected state=resolved, got %s", resolved.State)
	}
	wantOutput := "echo: hi jim [marker=one]"
	if resolved.Decision.ResolvedToolResult != wantOutput {
		t.Fatalf("resolved_tool_result = %q, want %q", resolved.Decision.ResolvedToolResult, wantOutput)
	}
	if resolved.Decision.ResolveToken == nil {
		t.Fatalf("expected resolve_token set after approve")
	}
}

// TestReviseThenApprove: user revises tool_arguments via the narrow
// tool, then approves. Tool runs with the REVISED args verbatim.
func TestReviseThenApprove(t *testing.T) {
	f := newFixture(t)

	reg := tools.NewRegistry(f.ctx, f.caller, false)
	reg.Register(testgated.NewDef(f.dedupe))
	tenant, _ := models.GetTenantByID(f.ctx, f.pool, f.tenantID)
	user, _ := models.GetUserByID(f.ctx, f.pool, f.tenantID, f.userID)
	ec := &tools.ExecContext{Ctx: f.ctx, Pool: f.pool, Tenant: tenant, User: user}

	gateRes, err := reg.ExecuteWithResult(ec, testgated.ToolName, makeArgs(t, map[string]any{"text": "original", "marker": "old"}))
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	cardID := parseCardIDFromHalted(t, gateRes.Output)

	// Revise: only tool_arguments change; tool_name/label/option_id preserved.
	revised := makeArgs(t, map[string]any{"text": "revised", "marker": "new"})
	rawPtr := json.RawMessage(revised)
	if _, err := f.svc.ReviseDecisionOption(f.ctx, f.caller, cardID, "approve", &rawPtr, nil); err != nil {
		t.Fatalf("revise: %v", err)
	}

	// Approve; expect the revised text to show up in the output.
	if _, err := f.svc.ResolveDecision(f.ctx, f.caller, cardID, "approve", &stubDM{}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	resolved, _ := f.svc.Get(f.ctx, f.caller, cardID)
	want := "echo: revised [marker=new]"
	if resolved.Decision.ResolvedToolResult != want {
		t.Fatalf("got %q, want %q", resolved.Decision.ResolvedToolResult, want)
	}
}

// TestReviseCannotChangeToolName: the service layer enforces that
// tool_name is write-once at creation, even if a caller tries to
// sneak it through the tool_arguments blob.
func TestReviseCannotChangeToolName(t *testing.T) {
	f := newFixture(t)

	reg := tools.NewRegistry(f.ctx, f.caller, false)
	reg.Register(testgated.NewDef(f.dedupe))
	tenant, _ := models.GetTenantByID(f.ctx, f.pool, f.tenantID)
	user, _ := models.GetUserByID(f.ctx, f.pool, f.tenantID, f.userID)
	ec := &tools.ExecContext{Ctx: f.ctx, Pool: f.pool, Tenant: tenant, User: user}

	gateRes, _ := reg.ExecuteWithResult(ec, testgated.ToolName, makeArgs(t, map[string]any{"text": "x"}))
	cardID := parseCardIDFromHalted(t, gateRes.Output)

	// Verify tool_name didn't change post-revise by re-reading the card.
	revised := json.RawMessage(makeArgs(t, map[string]any{"text": "y"}))
	if _, err := f.svc.ReviseDecisionOption(f.ctx, f.caller, cardID, "approve", &revised, nil); err != nil {
		t.Fatalf("revise: %v", err)
	}
	card, _ := f.svc.Get(f.ctx, f.caller, cardID)
	var approveOpt *cards.DecisionOption
	for i := range card.Decision.Options {
		if card.Decision.Options[i].OptionID == "approve" {
			approveOpt = &card.Decision.Options[i]
			break
		}
	}
	if approveOpt == nil {
		t.Fatalf("approve option missing")
	}
	if approveOpt.ToolName != testgated.ToolName {
		t.Fatalf("tool_name changed to %q after revise", approveOpt.ToolName)
	}
}

// TestTamperRefusedAtResolve: swap tool_name on a non-gate-artifact
// card to a gated tool via direct DB. ResolveDecision re-checks
// is_gate_artifact and refuses.
func TestTamperRefusedAtResolve(t *testing.T) {
	f := newFixture(t)

	// Create a regular (non-gate-artifact) decision with one option
	// whose tool_name points at a PolicyAllow tool — we'll tamper it.
	card, err := f.svc.CreateDecision(f.ctx, f.caller, cards.CardCreateInput{
		Kind:  cards.CardKindDecision,
		Title: "harmless",
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: "go",
			Options: []cards.DecisionOption{
				{OptionID: "go", SortOrder: 0, Label: "Go"},
				{OptionID: "no", SortOrder: 1, Label: "No"},
			},
		},
	})
	if err != nil {
		t.Fatalf("creating card: %v", err)
	}
	if card.Decision.IsGateArtifact {
		t.Fatalf("baseline card should not be a gate artifact")
	}

	// Tamper: swap tool_name to the gated tool name via direct DB.
	// The service layer refuses to do this through Update on a
	// pending card; bypassing to DB is what we're testing the
	// resolve-time re-check against.
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_card_decision_options SET tool_name = $1 WHERE tenant_id = $2 AND card_id = $3 AND option_id = $4`,
		testgated.ToolName, f.tenantID, card.ID, "go",
	); err != nil {
		t.Fatalf("tamper update: %v", err)
	}

	// Resolve should refuse because is_gate_artifact=false.
	_, err = f.svc.ResolveDecision(f.ctx, f.caller, card.ID, "go", &stubDM{})
	if err == nil {
		t.Fatalf("expected resolve to refuse tampered card")
	}
	if !strings.Contains(err.Error(), "not a gate artifact") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSweepRecoversStuckResolving: push a card into 'resolving' with
// a past deadline, run the sweep, assert it's flipped back to pending
// with last_error recorded.
func TestSweepRecoversStuckResolving(t *testing.T) {
	f := newFixture(t)

	// Build a pending card.
	card, err := f.svc.CreateDecision(f.ctx, f.caller, cards.CardCreateInput{
		Kind:  cards.CardKindDecision,
		Title: "stuck",
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: "go",
			Options: []cards.DecisionOption{
				{OptionID: "go", SortOrder: 0, Label: "Go"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Force state=resolving with a past deadline, bypassing the
	// normal service path. Direct UPDATE is fine for this simulation.
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_cards SET state = 'resolving' WHERE tenant_id = $1 AND id = $2`,
		f.tenantID, card.ID,
	); err != nil {
		t.Fatalf("forcing state: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_card_decisions SET resolving_deadline = $1, resolve_token = $2 WHERE tenant_id = $3 AND card_id = $4`,
		time.Now().Add(-time.Minute), uuid.New(), f.tenantID, card.ID,
	); err != nil {
		t.Fatalf("forcing deadline: %v", err)
	}

	// Run the sweep the scheduler would run.
	if err := cards.SweepStuckResolvingCards(f.ctx, f.pool); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	recovered, err := f.svc.Get(f.ctx, f.caller, card.ID)
	if err != nil {
		t.Fatalf("reloading swept card: %v", err)
	}
	if recovered.State != cards.CardStatePending {
		t.Fatalf("expected state=pending after sweep, got %s", recovered.State)
	}
	if !strings.Contains(recovered.Decision.LastError, "timed out") {
		t.Fatalf("expected timeout message in last_error, got %q", recovered.Decision.LastError)
	}
	if recovered.Decision.ResolveToken != nil {
		t.Fatalf("resolve_token should be cleared after sweep")
	}
}

// TestUpdatePendingRejectsOptionsReplacement: the old broad Update
// path cannot replace options on a pending card. Ensures all revises
// flow through ReviseDecisionOption (which can't change tool_name).
func TestUpdatePendingRejectsOptionsReplacement(t *testing.T) {
	f := newFixture(t)

	card, err := f.svc.CreateDecision(f.ctx, f.caller, cards.CardCreateInput{
		Kind:  cards.CardKindDecision,
		Title: "p",
		Decision: &cards.DecisionCreateInput{
			Priority:            cards.DecisionPriorityMedium,
			RecommendedOptionID: "a",
			Options: []cards.DecisionOption{
				{OptionID: "a", SortOrder: 0, Label: "A"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newOpts := []cards.DecisionOption{
		{OptionID: "b", SortOrder: 0, Label: "B"},
	}
	_, err = f.svc.Update(f.ctx, f.caller, card.ID, cards.CardUpdates{
		Decision: &cards.DecisionUpdates{Options: &newOpts},
	})
	if err == nil {
		t.Fatalf("expected Update to refuse options replacement on pending card")
	}
	if !strings.Contains(err.Error(), "cannot replace options") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// parseCardIDFromHalted extracts the UUID from
// "HALTED: xxx requires human approval. Decision card <uuid> was created. ..."
func parseCardIDFromHalted(t *testing.T, s string) uuid.UUID {
	t.Helper()
	_, rest, ok := strings.Cut(s, "Decision card ")
	if !ok {
		t.Fatalf("HALTED string missing card id: %q", s)
	}
	idStr, _, ok := strings.Cut(rest, " ")
	if !ok {
		t.Fatalf("couldn't find space after card id in %q", s)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatalf("parsing card id %q: %v", idStr, err)
	}
	return id
}

// stubDM is a no-op DMOpener for tests. The gated-tool path (Branch
// A/B) normally doesn't open a DM — but the ResolveDecision
// signature requires one, so we supply a no-op for the non-gated
// prompt-only path.
type stubDM struct{}

func (s *stubDM) OpenConversation(context.Context, string) (string, error) {
	return "D_stub", nil
}
