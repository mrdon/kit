// Test helpers for llm_builtins_test.go: the Sender stub, the tenant
// fixture, and small Postgres probes. Split out of llm_builtins_test.go
// so the main test file stays under the 500-line project rule.
package builder

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// stubSender is a minimal Sender that returns a canned response and
// records the request it saw. It is NOT thread-safe on purpose — LLM
// calls inside a single script run are serial anyway.
type stubSender struct {
	// respText is the text the stub returns in a single text block.
	respText string
	// inTokens/outTokens are the Usage numbers the stub reports back.
	inTokens  int
	outTokens int
	// model the stub claims to have served (echoed in Response.Model).
	model string
	// lastReq captures the request for assertions.
	lastReq *anthropic.Request
	// calls counts invocations across all tests sharing the stub.
	calls atomic.Int32
	// err, when set, is returned instead of a response.
	err error
}

func (s *stubSender) CreateMessage(_ context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	s.calls.Add(1)
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &anthropic.Response{
		Content: []anthropic.Content{{Type: "text", Text: s.respText}},
		Model:   s.model,
		Usage: anthropic.Usage{
			InputTokens:  s.inTokens,
			OutputTokens: s.outTokens,
		},
	}, nil
}

// llmFixture captures everything tests need to run an LLM builtin.
type llmFixture struct {
	pool      *pgxpool.Pool
	tenant    *models.Tenant
	appID     uuid.UUID
	userID    uuid.UUID
	capsRunID *uuid.UUID
}

// newLLMFixture spins up a tenant + user + builder_app + tenant_builder_config
// row and returns the pieces needed to drive BuildLLMBuiltins.
func newLLMFixture(t *testing.T) *llmFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_llm_" + uuid.NewString()
	slug := models.SanitizeSlug("llm-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "llm-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_llm_"+uuid.NewString()[:8], "LLM User", true)
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	var appID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO builder_apps (tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, tenant.ID, "llm-app-"+uuid.NewString()[:8], "llm test app", user.ID).Scan(&appID)
	if err != nil {
		t.Fatalf("creating builder_app: %v", err)
	}

	// Seed a tenant_builder_config row so the budget pre-check has data
	// to read. The default cap is high enough that normal tests never
	// trip it.
	_, err = pool.Exec(ctx, `
		INSERT INTO tenant_builder_config (tenant_id, llm_daily_cent_cap)
		VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO NOTHING
	`, tenant.ID, 10_000)
	if err != nil {
		t.Fatalf("seeding tenant_builder_config: %v", err)
	}

	// capsRunID left nil so llm_call_log inserts can proceed without a
	// matching script_runs row (fkey is ON DELETE SET NULL, NULL accepted).
	// Tests that need a real run create one themselves.
	return &llmFixture{
		pool:      pool,
		tenant:    tenant,
		appID:     appID,
		userID:    user.ID,
		capsRunID: nil,
	}
}

// countLogRows returns how many llm_call_log rows exist for the tenant.
func (f *llmFixture) countLogRows(t *testing.T) int {
	t.Helper()
	var n int
	err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM llm_call_log WHERE tenant_id = $1`, f.tenant.ID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("counting log rows: %v", err)
	}
	return n
}

// logRow mirrors the columns test cases read back from llm_call_log.
type logRow struct {
	fn    string
	tier  string
	in    int
	out   int
	cents int
}

// fetchLatestLogRow returns the most recent log row for the tenant.
func (f *llmFixture) fetchLatestLogRow(t *testing.T) logRow {
	t.Helper()
	var r logRow
	err := f.pool.QueryRow(context.Background(), `
		SELECT fn, model_tier, tokens_in, tokens_out, cost_cents
		FROM llm_call_log
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, f.tenant.ID).Scan(&r.fn, &r.tier, &r.in, &r.out, &r.cents)
	if err != nil {
		t.Fatalf("fetching log row: %v", err)
	}
	return r
}

// callHandler is shorthand for invoking the Handler directly.
func callHandler(t *testing.T, bundle *LLMBuiltins, name string, args map[string]any) (any, error) {
	t.Helper()
	call := &runtime.FunctionCall{Name: name, Args: args}
	return bundle.Handler(context.Background(), call)
}
