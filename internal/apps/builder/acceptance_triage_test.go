// Package builder: acceptance_triage_test.go holds the Phase 5 task 5c
// review_triage showcase acceptance test plus its tailored stubSender.
// Split from acceptance_test.go so each file stays under the 500-LOC
// soft cap. The test exercises cron + llm_* + decisions + rollback +
// role-gated exposed-tool invocation.
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/tools"
)

// TestAcceptance_ReviewTriage_Showcase exercises scheduled + LLM +
// decision + rollback in one end-to-end flow, keyed on the
// review_triage example.
func TestAcceptance_ReviewTriage_Showcase(t *testing.T) {
	f := newAcceptanceFixture(t, "manager")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Smarter stubSender: categorise based on keywords in the prompt
	// text (which carries the review text we asked classify about) and
	// return a plausible reply draft for generate.
	triageSender := newTriageStubSender()
	deps := acceptanceDeps(t, f, triageSender)
	SetScriptRunDeps(deps)
	t.Cleanup(func() { SetScriptRunDeps(nil) })

	// 1. Fetch + replay the review_triage example.
	out, err := handleBuilderExamples(f.adminEC(ctx), json.RawMessage(`{"name":"review_triage"}`))
	if err != nil {
		t.Fatalf("builder_examples: %v", err)
	}
	var def exampleDefinition
	if err := json.Unmarshal([]byte(out), &def); err != nil {
		t.Fatalf("parse: %v", err)
	}
	appName := replayExample(t, f, def)

	// Verify the schedule meta-tool accepted the cron expression.
	schedOut, err := handleListSchedules(f.adminEC(ctx), mustJSON(map[string]any{"app": appName}))
	if err != nil {
		t.Fatalf("app_list_schedules: %v", err)
	}
	var schedules []scheduleDTO
	_ = json.Unmarshal([]byte(schedOut), &schedules)
	if len(schedules) != 1 || schedules[0].Fn != "triage" || schedules[0].Cron != "0 9 * * *" {
		t.Fatalf("schedules = %+v, want one triage@0 9 * * *", schedules)
	}

	// 2. Pre-populate 20 reviews in the `reviews` collection. 3 of them
	//    contain "terrible" / "awful" so the stubSender classifies them
	//    as complaints.
	app, err := loadBuilderAppByName(ctx, f.pool, f.tenant.ID, appName)
	if err != nil {
		t.Fatalf("load app: %v", err)
	}
	svc := NewItemService(f.pool)
	scope := Scope{TenantID: f.tenant.ID, BuilderAppID: app.ID, Collection: "reviews", CallerUserID: f.adminUser.ID}
	complaintTexts := map[int]bool{3: true, 7: true, 14: true}
	for i := range 20 {
		text := fmt.Sprintf("Review %02d: service was fine, will return", i)
		if complaintTexts[i] {
			text = fmt.Sprintf("Review %02d: terrible experience, staff was awful", i)
		}
		if _, err := svc.InsertOne(ctx, scope, map[string]any{
			"text":    text,
			"triaged": false,
		}); err != nil {
			t.Fatalf("seed review %d: %v", i, err)
		}
	}

	// 3. Kick the scheduled function via handleRunScript. This is the
	//    admin-driven equivalent of the cron tick; by running the same
	//    handler the scheduler invokes we keep the test off the wall-
	//    clock timer.
	runOut, err := handleRunScript(f.adminEC(ctx), mustJSON(map[string]any{
		"app":    appName,
		"script": "main",
		"fn":     "triage",
	}))
	if err != nil {
		t.Fatalf("app_run_script triage: %v", err)
	}
	var runResp runScriptResponse
	if err := json.Unmarshal([]byte(runOut), &runResp); err != nil {
		t.Fatalf("parse run resp: %v\nraw=%s", err, runOut)
	}
	if runResp.Status != RunStatusCompleted {
		t.Fatalf("triage status = %q, err=%q", runResp.Status, runResp.Error)
	}

	// 4. Every review should now be triaged with a category; complaints
	//    produced a decision card each; LLM call log + script_runs
	//    counters reflect the work.
	results, err := svc.Find(ctx, scope, nil, nil, 100, 0)
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(results) != 20 {
		t.Fatalf("reviews after triage = %d, want 20", len(results))
	}
	complaints := 0
	for _, r := range results {
		if tri, _ := r["triaged"].(bool); !tri {
			t.Errorf("review not triaged: %v", r)
		}
		if cat, _ := r["category"].(string); cat == "" {
			t.Errorf("review has no category: %v", r)
		} else if cat == "complaint" {
			complaints++
		}
	}
	if complaints != 3 {
		t.Errorf("complaint count = %d, want 3", complaints)
	}

	// Decision cards — one per complaint.
	var decisionCount int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_cards WHERE tenant_id = $1 AND kind = 'decision'
	`, f.tenant.ID).Scan(&decisionCount); err != nil {
		t.Fatalf("count decisions: %v", err)
	}
	if decisionCount != 3 {
		t.Errorf("decision cards = %d, want 3", decisionCount)
	}

	// LLM call log — at minimum 20 classify calls + 3 generate calls.
	var classifyLog, generateLog int
	if err := f.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE fn = 'classify'),
		  COUNT(*) FILTER (WHERE fn = 'generate')
		FROM llm_call_log WHERE tenant_id = $1
	`, f.tenant.ID).Scan(&classifyLog, &generateLog); err != nil {
		t.Fatalf("count llm_call_log: %v", err)
	}
	if classifyLog < 20 {
		t.Errorf("classify log rows = %d, want >= 20", classifyLog)
	}
	if generateLog < 3 {
		t.Errorf("generate log rows = %d, want >= 3", generateLog)
	}

	// script_runs row accounts for token/cost counters. Note:
	// mutation_summary only tracks ActionBuiltins counters
	// (create_todo, create_decision, ...); db_* mutations don't roll
	// into it. The 3 create_decision calls this run makes DO land in
	// mutation_summary.inserts.
	if got, _ := runResp.MutationSummary["inserts"].(float64); int(got) < 3 {
		t.Errorf("mutation_summary.inserts = %v, want >= 3 (one per complaint decision)",
			runResp.MutationSummary["inserts"])
	}
	if runResp.TokensUsed <= 0 {
		t.Errorf("tokens_used = %d, want > 0", runResp.TokensUsed)
	}
	if runResp.CostCents <= 0 {
		t.Errorf("cost_cents = %d, want > 0", runResp.CostCents)
	}

	// 5. Rollback the triage run and confirm every review is back to
	//    triaged=false with no category.
	rbOut, err := handleRollbackScriptRun(f.adminEC(ctx), mustJSON(map[string]any{
		"run_id":  runResp.RunID.String(),
		"confirm": true,
	}))
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var rb rollbackResponse
	if err := json.Unmarshal([]byte(rbOut), &rb); err != nil {
		t.Fatalf("parse rollback: %v", err)
	}
	if rb.Restored != 20 {
		t.Errorf("restored = %d, want 20", rb.Restored)
	}
	postRB, err := svc.Find(ctx, scope, nil, nil, 100, 0)
	if err != nil {
		t.Fatalf("list post-rollback: %v", err)
	}
	if len(postRB) != 20 {
		t.Fatalf("reviews post-rollback = %d, want 20", len(postRB))
	}
	for _, r := range postRB {
		if tri, _ := r["triaged"].(bool); tri {
			t.Errorf("review still triaged after rollback: %v", r)
		}
		if _, present := r["category"]; present {
			t.Errorf("review still has category after rollback: %v", r)
		}
	}

	// 6. Manager invokes the exposed read-only tool. We wire the
	//    exposed-tool runner into tools.NewRegistry and re-run triage
	//    so list_recent_complaints has data to return.
	tools.SetExposedToolRunner(&exposedToolRunner{pool: f.pool})
	t.Cleanup(func() { tools.SetExposedToolRunner(nil) })
	if _, err := handleRunScript(f.adminEC(ctx), mustJSON(map[string]any{
		"app": appName, "script": "main", "fn": "triage",
	})); err != nil {
		t.Fatalf("retriage: %v", err)
	}

	managerReg := tools.NewRegistry(ctx, f.roleCaller, false)
	if !registryHasToolFor(managerReg, f.roleCaller, "recent_complaints") {
		t.Fatalf("manager registry missing recent_complaints visible to manager")
	}
	ec := &tools.ExecContext{Ctx: ctx, Pool: f.pool}
	rawResult, err := invokeRegistryTool(managerReg, ec, "recent_complaints", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("recent_complaints: %v", err)
	}
	var listed []map[string]any
	if err := json.Unmarshal([]byte(rawResult), &listed); err != nil {
		t.Fatalf("parse recent_complaints: %v\nraw=%s", err, rawResult)
	}
	if len(listed) != 3 {
		t.Errorf("recent_complaints count = %d, want 3", len(listed))
	}
}

// triageStubSender is a stubSender variant that inspects the request's
// prompt text and returns a category (for llm_classify) or a reply
// draft (for llm_generate) based on simple keyword rules — enough to
// make review_triage deterministic without touching a real LLM.
type triageStubSender struct {
	mu    sync.Mutex
	calls int
}

func newTriageStubSender() *triageStubSender { return &triageStubSender{} }

func (s *triageStubSender) CreateMessage(_ context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	prompt := ""
	if len(req.Messages) > 0 && len(req.Messages[0].Content) > 0 {
		prompt = req.Messages[0].Content[0].Text
	}
	var text string
	lower := strings.ToLower(prompt)
	// Classify prompts start with "Classify the following text..." (see
	// llm_builtins.go). Generate prompts start with the admin-provided
	// text; review_triage uses "Draft an empathetic ..." for complaint
	// replies. Route by distinctive phrasing.
	switch {
	case strings.Contains(prompt, "Classify the following text"):
		if strings.Contains(lower, "terrible") || strings.Contains(lower, "awful") {
			text = "complaint"
		} else {
			text = "noise"
		}
	case strings.Contains(prompt, "Draft an empathetic"):
		text = "We are very sorry to hear this. We would love to make it right — please contact us."
	default:
		text = "ok"
	}
	return &anthropic.Response{
		Content: []anthropic.Content{{Type: "text", Text: text}},
		Model:   "haiku",
		Usage: anthropic.Usage{
			InputTokens:  50,
			OutputTokens: 10,
		},
	}, nil
}
