package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools/approval"
	"github.com/mrdon/kit/internal/web"
)

// Policy governs whether a tool's handler runs directly or requires
// human approval via a decision card. Embedded on Def so the check can
// happen inside Registry.Execute itself — the single enforcement point.
type Policy string

const (
	// PolicyAllow is the default: the tool's handler runs whenever
	// Registry.Execute is called. Fine for reads, user-initiated
	// writes, and anything the caller can easily undo.
	PolicyAllow Policy = ""

	// PolicyGate means a call to this tool without an approval token in
	// ctx causes Registry.Execute to mint a decision card and return
	// HALTED instead of invoking the handler. The user approves the
	// card via the swipe UI; CardService.ResolveDecision then re-enters
	// Registry.Execute with approval.WithToken(ctx, ...) set, and the
	// handler runs with the same args. Use for side-effectful
	// operations that cross a trust boundary: sending email, posting
	// to external systems, destructive edits.
	PolicyGate Policy = "gate"
)

// HaltedPrefix marks the string returned to the agent when a tool call
// was intercepted by the gate instead of executing. The agent loop
// detects this so it can short-circuit remaining tool_use blocks in the
// same turn and so the system prompt's HALTED rule can instruct the LLM
// not to claim the action happened.
const HaltedPrefix = "HALTED: "

// ExecContext holds everything a tool needs to execute.
//
// Responder, OnToolCall, and OnIteration are observability/redirection
// seams used by the chat SSE path. Slack and scheduled-task callers
// leave them unset; agent.go treats nil values as no-op so there is one
// code path through the loop regardless of caller.
type ExecContext struct {
	Ctx      context.Context
	Pool     *pgxpool.Pool
	Slack    *kitslack.Client
	Fetcher  *web.Fetcher
	Tenant   *models.Tenant
	User     *models.User
	Session  *models.Session
	Channel  string
	ThreadTS string
	Svc      *services.Services

	// LLM is the shared Anthropic client. Populated by agent.buildExecContext
	// and the gated-tool resolve executor; tool tests leave it nil. Handlers
	// that need a one-off Claude call (e.g. create_task's Haiku triage over
	// task.description) read it. A nil value means "this ExecContext was
	// built by a test / path that doesn't supply an LLM" — handlers should
	// fall back to a safe default rather than panicking.
	LLM *anthropic.Client

	// TaskID is set when this run is executing a scheduled task. Tools
	// that want to link artifacts back to the originating task (e.g.
	// create_decision stamping origin_task_id) read this. Nil for
	// user-initiated Slack or chat runs.
	TaskID *uuid.UUID

	// TaskPolicy, when non-nil, is the capability manifest the scheduler
	// injected for this task run. The registry consults it inside
	// ExecuteWithResult to enforce allow-list, argument pinning, and
	// force-gate independently of what the LLM put in the tool input.
	// Nil for interactive Slack / chat runs and for tasks without a
	// policy — enforcement short-circuits and today's behaviour is
	// unchanged. ExecContext is treated as single-goroutine; callers
	// must not mutate TaskPolicy mid-run.
	TaskPolicy *models.Policy

	// Responder is where reply_in_thread sends its output. When nil, the
	// handler constructs a SlackResponder on demand (default behavior).
	Responder Responder

	// OnToolCall fires just before a tool's handler runs. Used by the
	// chat path to emit a "tool" SSE event.
	OnToolCall func(name string)

	// OnIteration fires at the top of each agent-loop iteration, before
	// the LLM call. Used by the chat path to flip the UI status line
	// back to "thinking" between tool calls.
	OnIteration func()
}

// Caller builds a services.Caller from the current execution context.
func (ec *ExecContext) Caller() *services.Caller {
	roles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
	roleIDs, _ := models.GetUserRoleIDs(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
	return &services.Caller{
		TenantID: ec.Tenant.ID,
		UserID:   ec.User.ID,
		Identity: ec.User.SlackUserID,
		Roles:    roles,
		RoleIDs:  roleIDs,
		IsAdmin:  slices.Contains(roles, models.RoleAdmin),
		Timezone: services.ResolveTimezone(ec.User.Timezone, ec.Tenant.Timezone),
	}
}

// HandlerFunc executes a tool and returns a string result.
type HandlerFunc func(ec *ExecContext, input json.RawMessage) (string, error)

// Def defines a single tool.
//
// VisibleToRoles gates catalog visibility independent of AdminOnly:
//   - empty slice → visible to every caller (subject to AdminOnly)
//   - non-empty  → only callers who hold at least one listed role see it
//
// Builder-exposed tools use VisibleToRoles to scope tenant-specific scripts
// to the roles their author chose (e.g. a "lookup" tool visible only to
// "bartender"). Static Kit tools leave it empty.
type Def struct {
	Name           string
	Description    string
	Schema         map[string]any
	Handler        HandlerFunc
	AdminOnly      bool
	VisibleToRoles []string
	Terminal       bool // if true, calling this tool ends the agent loop

	// DefaultPolicy controls whether Registry.Execute dispatches the
	// handler directly (PolicyAllow, the default) or intercepts the
	// call into an approval decision card (PolicyGate). See
	// .claude/skills/gated-tools-guide.md for when to use Gate.
	DefaultPolicy Policy

	// DenyCallerGate opts this tool out of the universal
	// `require_approval` input parameter that the registry injects into
	// every tool's schema and honours at execute time. Use for tools
	// where gating is nonsensical (e.g. reply_in_thread — the live
	// conversational reply, whose channel is bound to the session).
	// PolicyGate tools are gated unconditionally regardless of this flag.
	DenyCallerGate bool

	// GateCardPreview returns the user-facing framing for this tool's
	// approval card — title, optional body, custom option labels.
	// Applies both to PolicyGate tools and to caller-gated PolicyAllow
	// tools (when the agent sets require_approval). A nil func falls
	// back to a synthesized default based on the tool name. Fields
	// left empty on the returned struct also fall back individually.
	//
	// Receives the ExecContext so preview helpers can resolve friendly
	// names from the DB (e.g. Slack channel id → #name) instead of
	// dumping raw identifiers into the card title. Previews that don't
	// need lookup ignore the ec arg.
	GateCardPreview func(ec *ExecContext, args json.RawMessage) GateCardPreview
}

// Registry holds all registered tools.
type Registry struct {
	defs     []Def
	handlers map[string]HandlerFunc
}

// ExposedToolDef describes one tenant-published script function surfaced
// through the generic tool registry. Registry construction asks the
// registered ExposedToolRunner to enumerate these per caller, then wraps
// each with a Def whose handler invokes the backing script.
type ExposedToolDef struct {
	ToolName       string
	Description    string
	ArgsSchema     map[string]any
	VisibleToRoles []string
	// Invoke is a closure that runs the exposed tool with the supplied
	// keyword args. Implementations are responsible for enforcing stale
	// flags, role checks at invocation time, and child audit rows. The
	// registry treats the returned string as the tool result.
	Invoke func(ctx context.Context, ec *ExecContext, args map[string]any) (string, error)
}

// ExposedToolRunner is implemented by the builder app (or any future
// source of dynamic tools). The registry calls List at construction time
// to enumerate the caller's tenant-published tools. A nil runner makes
// dynamic registration a no-op — static Kit tools still register
// normally, so a mis-wired startup doesn't break the agent.
//
// Split out into its own interface to dodge the import cycle: the
// builder package imports tools (to register Defs); if tools imported
// builder to call RunScript directly we'd have a cycle. The interface
// hop means the builder registers an implementation during app Init and
// the tools package only depends on the narrow contract declared here.
type ExposedToolRunner interface {
	// List returns the exposed tools for the given caller's tenant.
	// Implementations should filter out stale rows so the registry can
	// trust what it receives (registry applies the role-visibility check
	// centrally via Def.VisibleToRoles).
	List(ctx context.Context, caller *services.Caller) ([]ExposedToolDef, error)
}

// currentExposedToolRunner is wired once at startup from cmd/kit/main.go
// (after apps Init). A nil value is allowed — dynamic registration simply
// short-circuits and the static tool set is returned.
var currentExposedToolRunner ExposedToolRunner

// SetExposedToolRunner wires the tenant-exposed tool source used by
// NewRegistry. Call once during startup, after apps have initialized.
// Passing nil disables dynamic registration (tests reset via t.Cleanup).
func SetExposedToolRunner(r ExposedToolRunner) {
	currentExposedToolRunner = r
}

// GateCardPreview is the human-readable framing a gated tool provides
// for its approval card. Empty fields fall back to the generic
// "Approve <tool>?" wording so tools without a Preview func still work.
type GateCardPreview struct {
	// Title is the card headline the user sees. Short and concrete —
	// e.g. "Send email to bob@example.com?" — not the tool name.
	Title string
	// Body is optional supporting context in markdown. The option's own
	// preview component (e.g. SendEmailPreview on the web) renders the
	// full argument payload inline, so Body is usually short or empty.
	Body string
	// ApproveLabel overrides the "Approve" button label. E.g. "Send".
	ApproveLabel string
	// SkipLabel overrides the "Skip" button label. E.g. "Don't send".
	SkipLabel string
}

// GateCreator builds the decision card that wraps a PolicyGate tool
// call intercepted by Registry.Execute. Implemented by CardService so
// the tools package doesn't need to import cards (cycle risk). Returns
// the created card id for the audit trail / HALTED message, plus a
// user-facing URL the agent can share so the user can find the card
// to approve (empty string if no baseURL is configured).
type GateCreator interface {
	CreateGateCard(ctx context.Context, ec *ExecContext, toolName string, toolArguments json.RawMessage, preview GateCardPreview) (uuid.UUID, string, error)
}

// currentGateCreator is the process-wide gate-card sink, wired once at
// startup from cmd/kit/main.go after the cards app initializes. When
// nil (tests, misconfigured startup), a PolicyGate tool call returns a
// user-safe error rather than silently executing or panicking.
var currentGateCreator GateCreator

// SetGateCreator wires the gate-card creator used by Registry.Execute
// when a PolicyGate tool is invoked without an approval token. Pass nil
// to disable gating (e.g. test cleanup).
func SetGateCreator(c GateCreator) {
	currentGateCreator = c
}

// NewRegistry creates a registry and runs all register functions for the
// given caller. botInitiated toggles which messaging tools are registered:
// live Slack conversations get reply_in_thread; bot-initiated runs
// (scheduled tasks, decision resolves) do not — the agent must pick a
// named channel or user explicitly.
//
// The caller determines two things: admin gating for admin-only static
// tools (preserved from the pre-4d signature via caller.IsAdmin) and the
// pool+tenant used to fetch tenant-specific dynamic tools. Passing a nil
// caller is allowed in tests that don't exercise dynamic tools; admin
// tools are simply skipped in that case.
func NewRegistry(ctx context.Context, caller *services.Caller, botInitiated bool) *Registry {
	r := &Registry{handlers: make(map[string]HandlerFunc)}

	isAdmin := caller != nil && caller.IsAdmin

	// Each tool group registers itself here.
	// To add a new tool: create a file, add a Register call below.
	registerCoreTools(r, botInitiated)
	registerSkillTools(r, isAdmin)
	registerRoleTools(r, isAdmin)
	registerRuleTools(r, isAdmin)
	registerMemoryTools(r, isAdmin)
	registerTenantTools(r, isAdmin)
	registerWebTools(r)
	registerTaskTools(r, isAdmin)
	registerUserTools(r)

	// App tools
	for _, a := range apps.All() {
		a.RegisterAgentTools(ctx, r, caller, isAdmin)
	}

	// Dynamic per-tenant exposed tools. Skipped silently if no runner is
	// wired (tests, or a startup ordering slip). Failures to enumerate are
	// logged but non-fatal — the agent still works with the static set.
	if currentExposedToolRunner != nil && caller != nil {
		defs, err := currentExposedToolRunner.List(ctx, caller)
		if err != nil {
			slog.Warn("listing exposed tools", "tenant_id", caller.TenantID, "error", err)
		} else {
			for _, d := range defs {
				r.Register(buildExposedDef(d))
			}
		}
	}

	return r
}

// buildExposedDef wraps an ExposedToolDef into a tools.Def whose handler
// unmarshals the raw input, forwards to the runner-supplied Invoke, and
// returns the string result. The closure captures d by value so repeated
// registry builds don't share a mutable reference.
func buildExposedDef(d ExposedToolDef) Def {
	captured := d
	return Def{
		Name:           captured.ToolName,
		Description:    captured.Description,
		Schema:         captured.ArgsSchema,
		VisibleToRoles: captured.VisibleToRoles,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			args := map[string]any{}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &args); err != nil {
					return "", fmt.Errorf("exposed tool %s: invalid arguments: %w", captured.ToolName, err)
				}
			}
			return captured.Invoke(ec.Ctx, ec, args)
		},
	}
}

// Register adds a tool to the registry. Unless the Def opts out via
// DenyCallerGate, the optional `require_approval` boolean is appended to
// the tool's input schema so the agent can request human approval for
// any call at its own discretion (see ExecuteWithResult).
func (r *Registry) Register(d Def) {
	if !d.DenyCallerGate {
		d.Schema = services.InjectRequireApprovalSchema(d.Schema)
	}
	r.defs = append(r.defs, d)
	r.handlers[d.Name] = d.Handler
}

// requireApprovalField aliases services.RequireApprovalField so in-file
// references stay short without duplicating the constant value.
const requireApprovalField = services.RequireApprovalField

// Policies returns a snapshot of tool-name -> DefaultPolicy for every
// Def in this registry. Used at startup by cmd/kit to build a static
// policy lookup table. The zero-value Policy is PolicyAllow, matching
// unregistered / dynamic tools, so missing keys are safe.
func (r *Registry) Policies() map[string]Policy {
	out := make(map[string]Policy, len(r.defs))
	for _, d := range r.defs {
		out[d.Name] = d.DefaultPolicy
	}
	return out
}

// DropGatedTools removes every Def whose DefaultPolicy is PolicyGate
// from this registry. Used by the chat-revision path (§6 of the plan)
// to build a reduced registry: prompt injection in card content
// shouldn't be able to coerce the chat LLM into calling a gated tool
// directly. The registry-level gate catches such calls anyway, but a
// HALTED result mid-chat is confusing UX — defense-in-depth drops the
// tools before they can even be called.
func (r *Registry) DropGatedTools() {
	kept := r.defs[:0]
	for _, d := range r.defs {
		if d.DefaultPolicy == PolicyGate {
			delete(r.handlers, d.Name)
			continue
		}
		kept = append(kept, d)
	}
	r.defs = kept
}

// alwaysLoaded is the set of tool names that are sent in the request
// prefix on every iteration. Everything else is marked DeferLoading=true
// so the model must discover it via tool_search_tool.
//
// The set is intentionally tight (~5 tools): the three terminal
// messaging tools so the model can always end a turn; search_skills as
// the existing capability-discovery surface; find_user because almost
// every Slack interaction needs to resolve a mention. Add a name here
// only if the model needs it from the very first turn without a
// search round-trip.
var alwaysLoaded = map[string]bool{
	"reply_in_thread":          true,
	"post_to_channel":          true,
	"dm_user":                  true,
	"search_skills":            true,
	"find_user":                true,
	"revise_decision_option":   true, // chat-revision path uses this on turn 1
	"get_decision_tool_result": true,
}

// Definitions returns tool definitions for the Claude API. Tools not in
// alwaysLoaded are marked DeferLoading=true; their schemas stay out of
// the prefix and only enter context when the model invokes
// tool_search_tool to find them.
func (r *Registry) Definitions() []anthropic.Tool {
	var tools []anthropic.Tool
	for _, d := range r.defs {
		tools = append(tools, anthropic.Tool{
			Name:         d.Name,
			Description:  d.Description,
			InputSchema:  d.Schema,
			DeferLoading: !alwaysLoaded[d.Name],
		})
	}
	return tools
}

// DefinitionsFor returns Definitions() filtered for the given caller:
// AdminOnly tools are omitted unless caller.IsAdmin, and VisibleToRoles
// tools are omitted unless caller holds at least one listed role.
// Handlers set via Execute remain callable by name — the filter governs
// catalog/discovery surfaces only. A nil caller is treated as a
// non-admin with no roles (maximally restrictive).
func (r *Registry) DefinitionsFor(caller *services.Caller) []anthropic.Tool {
	var out []anthropic.Tool
	for _, d := range r.defs {
		if !IsDefVisible(d, caller) {
			continue
		}
		out = append(out, anthropic.Tool{
			Name:         d.Name,
			Description:  d.Description,
			InputSchema:  d.Schema,
			DeferLoading: !alwaysLoaded[d.Name],
		})
	}
	return out
}

// IsDefVisible centralises the visibility rule so registry, MCP, and
// tests share one predicate. See DefinitionsFor for semantics.
//
// IsAdmin is computed from membership in the builtin `admin` role at
// Caller construction — Kit's tenant-scoped superuser flag (Django-style
// `is_superuser`, bounded to the caller's tenant since every query
// filters by tenant_id). A tenant admin sees every tool in their own
// tenant regardless of AdminOnly or VisibleToRoles. This bypass keeps
// admins from being silently locked out of tools their tenant hasn't
// materialised a role row for.
func IsDefVisible(d Def, caller *services.Caller) bool {
	if caller != nil && caller.IsAdmin {
		return true
	}
	if d.AdminOnly {
		return false
	}
	if len(d.VisibleToRoles) > 0 {
		if caller == nil {
			return false
		}
		if !anyIntersect(caller.Roles, d.VisibleToRoles) {
			return false
		}
	}
	return true
}

// anyIntersect reports whether slices a and b share at least one element.
// Case-sensitive to match role-name comparisons elsewhere in Kit.
func anyIntersect(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, y := range b {
		if _, ok := set[y]; ok {
			return true
		}
	}
	return false
}

// ExecResult carries a tool-execution outcome along with a Halted flag
// the agent loop uses to short-circuit remaining tool_use blocks in the
// same turn when a gate fired.
type ExecResult struct {
	Output string
	// Halted is true when the call was intercepted by the PolicyGate
	// path and a decision card was created instead of running the
	// handler. The agent must not treat the action as performed.
	Halted bool
}

// Execute runs a tool by name, or intercepts via the PolicyGate path.
// Convenience shim for callers that don't need the Halted signal; see
// ExecuteWithResult for the full outcome.
func (r *Registry) Execute(ec *ExecContext, name string, input json.RawMessage) (string, error) {
	res, err := r.ExecuteWithResult(ec, name, input)
	return res.Output, err
}

// ExecuteWithResult runs a tool by name with policy enforcement. This
// is the single enforcement point for PolicyGate tools: any tool call
// that reaches this function goes through the policy check, so any
// caller that invokes tools through the registry is automatically
// gated. See .claude/skills/gated-tools-guide.md for the full contract.
//
// If the tool's DefaultPolicy is PolicyGate and ctx does not carry an
// approval token (minted by CardService.ResolveDecision), a decision
// card is created via the injected GateCreator and the returned
// ExecResult has Halted=true + a "HALTED: ..." Output. The agent loop
// short-circuits further tool calls on a halted turn.
func (r *Registry) ExecuteWithResult(ec *ExecContext, name string, input json.RawMessage) (ExecResult, error) {
	def, ok := r.defByName(name)
	if !ok {
		return ExecResult{}, fmt.Errorf("unknown tool: %s", name)
	}

	// Extract the caller-set require_approval flag (if any) and strip
	// it from the payload so handlers never see it. Honoured only when
	// the tool hasn't opted out via DenyCallerGate.
	callerRequested := false
	if !def.DenyCallerGate {
		callerRequested, input = services.ReadRequireApproval(input)
	}

	// Task-policy enforcement runs before the gate decision. Allow-list
	// and pinned-args are no-ops when ec.TaskPolicy is nil (interactive
	// paths); force_gate is OR'd into shouldGate below.
	if !ec.TaskPolicy.IsAllowed(name) {
		recordPolicyEvent(ec, models.PolicyEnforcedData{
			Action:   models.PolicyActionAllowListReject,
			ToolName: name,
			Reason:   "tool not in allowed_tools",
		})
		return ExecResult{}, fmt.Errorf("tool %q not permitted by task policy", name)
	}
	if pinned := ec.TaskPolicy.PinnedFor(name); len(pinned) > 0 {
		merged, changed, err := models.MergePinnedArgs(input, pinned)
		if err != nil {
			return ExecResult{}, fmt.Errorf("applying pinned args for %q: %w", name, err)
		}
		if changed {
			for k, v := range pinned {
				recordPolicyEvent(ec, models.PolicyEnforcedData{
					Action:   models.PolicyActionPinnedArgOverride,
					ToolName: name,
					ArgKey:   k,
					NewValue: v,
				})
			}
		}
		input = merged
	}

	forcedGate := ec.TaskPolicy.ForcesGate(name)
	shouldGate := def.DefaultPolicy == PolicyGate || callerRequested || forcedGate
	if shouldGate {
		_, _, approved := approval.FromCtx(ec.Ctx)
		if !approved {
			if forcedGate && def.DefaultPolicy != PolicyGate && !callerRequested {
				recordPolicyEvent(ec, models.PolicyEnforcedData{
					Action:   models.PolicyActionForceGateApplied,
					ToolName: name,
					Reason:   "force_gate on task policy",
				})
			}
			out, err := r.createGateCard(ec, def, input)
			if err != nil {
				return ExecResult{}, err
			}
			return ExecResult{Output: out, Halted: true}, nil
		}
		// Approval present — fall through to the normal handler. The
		// handler is expected to honour the idempotency contract: dedupe
		// by approval.Token.ResolveToken() so a sweep-driven retry of a
		// wedged card doesn't double-execute.
	}

	if msg := validateToolInput(def.Schema, input); msg != "" {
		return ExecResult{Output: msg}, nil
	}

	out, err := def.Handler(ec, input)
	return ExecResult{Output: out}, err
}

// validateToolInput checks the raw JSON input against the tool's declared
// schema before dispatch. Its purpose is to turn LLM argument mistakes —
// typo'd field names, missing required fields — into a tool_result the
// model can read and retry, instead of silently dropping the field after
// json.Unmarshal and letting the handler no-op. Returns the message to
// surface to the caller, or "" when the input is valid.
//
// The check is intentionally narrow: top-level object schemas only,
// reject unknown properties (strict) and missing required fields. Type
// mismatches are left to handler-side unmarshal since the shape of that
// error is usually specific enough for the LLM to self-correct. A schema
// without a `properties` block (or a non-object top-level type) is
// treated as "accept anything" and skipped — matches how the few
// free-form tool schemas we have are used.
func validateToolInput(schema map[string]any, input json.RawMessage) string {
	if schema == nil {
		return ""
	}
	if t, _ := schema["type"].(string); t != "" && t != "object" {
		return ""
	}
	// No properties block = "no args." Tools with real arguments declare
	// them; a no-arg tool declares properties: {}. Either way we validate
	// strictly — there is no "accept anything" mode by design, so a
	// typo'd or hallucinated argument always surfaces as a tool_result
	// the LLM can correct against.
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}

	raw := map[string]json.RawMessage{}
	if len(input) > 0 && !bytes.Equal(bytes.TrimSpace(input), []byte("null")) {
		if err := json.Unmarshal(input, &raw); err != nil {
			return fmt.Sprintf("Invalid arguments: %s. Expected a JSON object matching the tool schema.", err.Error())
		}
	}

	// require_approval is stripped from the payload before this point for
	// normal tools. DenyCallerGate tools keep it in the payload (their
	// handlers can observe it), so tolerate the key here regardless.
	var unknown []string
	for k := range raw {
		if k == services.RequireApprovalField {
			continue
		}
		if _, ok := props[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		known := make([]string, 0, len(props))
		for k := range props {
			known = append(known, k)
		}
		slices.Sort(known)
		return fmt.Sprintf("Unknown argument(s): %s. Expected one of: %s. Check the tool schema and retry.",
			strings.Join(unknown, ", "), strings.Join(known, ", "))
	}

	var missing []string
	switch req := schema["required"].(type) {
	case []string:
		for _, name := range req {
			if _, present := raw[name]; !present {
				missing = append(missing, name)
			}
		}
	case []any:
		for _, v := range req {
			name, _ := v.(string)
			if name == "" {
				continue
			}
			if _, present := raw[name]; !present {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Sprintf("Missing required argument(s): %s. Retry the call with those fields set.", strings.Join(missing, ", "))
	}

	return ""
}

// recordPolicyEvent appends a policy_enforced session event when a run
// has a session to log against. No-ops in tests and interactive paths
// that leave Session or Pool unset.
func recordPolicyEvent(ec *ExecContext, data models.PolicyEnforcedData) {
	if ec == nil || ec.Pool == nil || ec.Session == nil {
		return
	}
	if err := models.AppendSessionEvent(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.Session.ID, models.EventTypePolicyEnforced, data); err != nil {
		slog.Warn("failed to record policy_enforced event", "error", err, "tool", data.ToolName, "action", data.Action)
	}
}

// defByName looks up a tool's Def. Returns (Def{}, false) if missing.
// Registered separately from the handlers map so we can read schema
// + policy without another allocation.
func (r *Registry) defByName(name string) (Def, bool) {
	for i := range r.defs {
		if r.defs[i].Name == name {
			return r.defs[i], true
		}
	}
	return Def{}, false
}

// createGateCard asks the registered GateCreator to mint a decision
// card for the intercepted tool call and returns the user-facing HALTED
// message for the agent. The wording is non-ambiguous on purpose: the
// agent's system prompt has a matching rule about the HALTED token so
// it won't tell the user the action happened.
func (r *Registry) createGateCard(ec *ExecContext, def Def, input json.RawMessage) (string, error) {
	if currentGateCreator == nil {
		return "", fmt.Errorf("gating is not configured for this process; tool %q cannot run", def.Name)
	}
	preview := defaultGateCardPreview(def.Name)
	if def.GateCardPreview != nil {
		custom := def.GateCardPreview(ec, input)
		if custom.Title != "" {
			preview.Title = custom.Title
		}
		if custom.Body != "" {
			preview.Body = custom.Body
		}
		if custom.ApproveLabel != "" {
			preview.ApproveLabel = custom.ApproveLabel
		}
		if custom.SkipLabel != "" {
			preview.SkipLabel = custom.SkipLabel
		}
	}
	cardID, cardURL, err := currentGateCreator.CreateGateCard(ec.Ctx, ec, def.Name, input, preview)
	if err != nil {
		return "", fmt.Errorf("creating approval card for %q: %w", def.Name, err)
	}
	urlClause := ""
	if cardURL != "" {
		urlClause = fmt.Sprintf(" Approve it here: %s.", cardURL)
	}
	return fmt.Sprintf(
		"%s%s requires human approval. Decision card %s was created.%s Do NOT tell the user the action happened; say it's queued for their review and share the approval URL if one is provided.",
		HaltedPrefix, def.Name, cardID, urlClause,
	), nil
}

// defaultGateCardPreview synthesizes a best-effort card framing for a
// tool that didn't supply its own. Title is a human-ish spin on the tool
// name ("post_to_channel" → "Run post to channel?") so the card is at
// least identifiable without custom wiring.
func defaultGateCardPreview(toolName string) GateCardPreview {
	human := toolName
	if human == "" {
		human = "this action"
	} else {
		human = strings.ReplaceAll(human, "_", " ")
	}
	return GateCardPreview{
		Title:        "Run " + human + "?",
		Body:         "Kit drafted the action shown below. Review it and approve to continue, or skip to cancel.",
		ApproveLabel: "Approve",
		SkipLabel:    "Skip",
	}
}

// IsTerminal returns true if calling this tool should end the agent loop.
// All three messaging tools are terminal — any single post is the agent's
// output for that run. A task that needs to fan out to multiple targets
// can emit parallel tool calls in one turn, or be split into separate
// decision options.
func (r *Registry) IsTerminal(name string, _ json.RawMessage, _ string) bool {
	for _, d := range r.defs {
		if d.Name == name {
			return d.Terminal
		}
	}
	return false
}

// propsReq builds a JSON schema with required fields.
func propsReq(fields map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": fields,
		"required":   required,
	}
}

// field is a shorthand for a schema field.
func field(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}
