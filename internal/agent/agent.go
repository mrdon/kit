package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools"
	"github.com/mrdon/kit/internal/web"
)

const (
	maxIterations = 10
	modelHaiku    = "claude-haiku-4-5-20251001"
	maxTokens     = 4096
)

// Agent runs the observe/reason/act loop for a single message.
type Agent struct {
	pool    *pgxpool.Pool
	llm     *anthropic.Client
	fetcher *web.Fetcher
	svc     *services.Services
}

// NewAgent creates a new agent instance.
func NewAgent(pool *pgxpool.Pool, llm *anthropic.Client, fetcher *web.Fetcher) *Agent {
	return &Agent{
		pool:    pool,
		llm:     llm,
		fetcher: fetcher,
		svc:     services.New(pool, nil),
	}
}

// RunInput is everything the agent loop needs for a single turn.
// Required fields (Slack through UserText) identify the conversation;
// optional fields configure one run's observer hooks, system-prompt
// additions, and task-context metadata. Pointer-to-struct isn't used
// because every callsite has all the required fields on hand.
type RunInput struct {
	// Slack client, tenant/user/session, and the three conversation
	// coordinates are required for every run.
	Slack    *kitslack.Client
	Tenant   *models.Tenant
	User     *models.User
	Session  *models.Session
	Channel  string
	ThreadTS string
	UserText string

	// Task, when non-nil, marks this run as a scheduled-task execution
	// and adds author metadata to the system prompt. Slack-live and chat
	// runs leave it nil.
	Task *TaskContext

	// Responder overrides where reply_in_thread sends its output. When
	// nil the handler defaults to a Slack responder.
	Responder tools.Responder

	// OnToolCall, OnIteration are no-op if nil. The chat path wires them
	// to emit SSE status/tool events to the browser.
	OnToolCall  func(name string)
	OnIteration func()

	// SystemSuffix is appended to the system prompt for this run only.
	// Chat uses it to inject the card the user is acting on, so the
	// prompt-shaped context doesn't accumulate in replayed history when
	// the user sends follow-up messages on the same card.
	SystemSuffix string

	// HistoryWindow, when > 0, caps replayed message_received +
	// assistant_turn pairs from session history at the last N, and
	// drops tool_results blocks entirely from replay. Used by the
	// card-chat path (HistoryWindow=6) to keep per-card conversations
	// bounded; Slack and task runs leave it 0 (full replay, today's
	// behavior).
	HistoryWindow int

	// DropGatedTools, when true, removes PolicyGate tools from the
	// registry before this run. Used by the card-chat revision path as
	// defense-in-depth against prompt injection in card content that
	// might try to coerce the LLM into calling a gated tool directly.
	// The registry-level gate would still catch such calls, but a
	// HALTED result mid-chat is confusing UX.
	DropGatedTools bool
}

// Run executes the agent loop for a user message.
func (a *Agent) Run(ctx context.Context, in RunInput) error {
	start := time.Now()
	tenant, session := in.Tenant, in.Session

	ec := a.buildExecContext(ctx, in)
	caller := ec.Caller()
	registry := tools.NewRegistry(ctx, caller, in.Session.BotInitiated)
	if in.DropGatedTools {
		registry.DropGatedTools()
	}

	var status *statusTracker
	if in.Task == nil && !strings.HasPrefix(in.Channel, "web:") {
		status = newStatusTracker(in.Slack, in.Channel, in.ThreadTS)
		status.update(ctx, "Thinking...")
		defer status.cleanup(ctx)
	}

	_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, models.EventTypeMessageReceived, map[string]any{
		"user_id": in.User.ID,
		"text":    in.UserText,
		"channel": in.Channel,
	})

	messages := a.buildInitialMessages(ctx, in)
	systemPrompt := a.buildSystemPrompt(ctx, in)
	toolDefs := buildToolDefs(registry, caller)

	sentMessage := false
	var usage usageTotals

	for i := range maxIterations {
		iterStart := time.Now()
		if status != nil {
			status.update(ctx, "Thinking...")
		}
		if ec.OnIteration != nil {
			ec.OnIteration()
		}

		resp, err := a.callLLM(ctx, tenant.ID, session.ID, systemPrompt, messages, toolDefs, i, iterStart)
		if err != nil {
			return err
		}
		usage.add(&resp.Usage)

		if resp.StopReason == "end_turn" && len(resp.ToolUses()) == 0 {
			if a.handleEndTurn(ctx, ec, in, resp) {
				sentMessage = true
			}
			break
		}

		_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, models.EventTypeAssistantTurn, map[string]any{
			"content": resp.Content,
		})
		messages = append(messages, anthropic.Message{Role: "assistant", Content: resp.Content})

		if resp.StopReason == "tool_use" {
			toolResults, terminal := a.executeTools(ec, registry, resp, status, in.Channel)
			if terminal {
				sentMessage = true
			}
			_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, models.EventTypeToolResults, map[string]any{
				"content": toolResults,
			})
			messages = append(messages, anthropic.Message{Role: "user", Content: toolResults})
			if sentMessage {
				break
			}
		}
	}

	if !sentMessage && !session.BotInitiated {
		a.sendFallback(ctx, ec, in)
	}

	_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, models.EventTypeSessionComplete, map[string]any{
		"duration_ms":      time.Since(start).Milliseconds(),
		"total_input":      usage.in,
		"total_output":     usage.out,
		"total_cache_read": usage.cacheRead,
	})

	return nil
}

// usageTotals accumulates token counts across iterations of one run.
type usageTotals struct {
	in, out, cacheRead, cacheWrite int
}

func (u *usageTotals) add(x *anthropic.Usage) {
	u.in += x.InputTokens
	u.out += x.OutputTokens
	u.cacheRead += x.CacheReadInputTokens
	u.cacheWrite += x.CacheCreationInputTokens
}

func (a *Agent) buildExecContext(ctx context.Context, in RunInput) *tools.ExecContext {
	ec := &tools.ExecContext{
		Ctx:         ctx,
		Pool:        a.pool,
		Slack:       in.Slack,
		Fetcher:     a.fetcher,
		Tenant:      in.Tenant,
		User:        in.User,
		Session:     in.Session,
		Channel:     in.Channel,
		ThreadTS:    in.ThreadTS,
		Svc:         a.svc,
		Responder:   in.Responder,
		OnToolCall:  in.OnToolCall,
		OnIteration: in.OnIteration,
	}
	if in.Task != nil && in.Task.ID != (uuid.UUID{}) {
		taskID := in.Task.ID
		ec.TaskID = &taskID
	}
	return ec
}

func (a *Agent) buildInitialMessages(ctx context.Context, in RunInput) []anthropic.Message {
	messages := a.rebuildHistory(ctx, in.Tenant, in.Session, historyOptions{
		windowPairs:     in.HistoryWindow,
		dropToolResults: in.HistoryWindow > 0,
	})
	currentTime := fmt.Sprintf("[Current time: %s UTC]", time.Now().UTC().Format("2006-01-02T15:04"))
	return append(messages, anthropic.Message{
		Role:    "user",
		Content: []anthropic.Content{{Type: "text", Text: currentTime + "\n" + in.UserText}},
	})
}

func (a *Agent) buildSystemPrompt(ctx context.Context, in RunInput) []anthropic.SystemBlock {
	systemText := BuildSystemPrompt(ctx, a.pool, in.Tenant, in.User, in.Task)
	if in.SystemSuffix != "" {
		systemText += "\n\n" + in.SystemSuffix
	}
	return []anthropic.SystemBlock{{
		Type:         "text",
		Text:         systemText,
		CacheControl: anthropic.Ephemeral(),
	}}
}

// buildToolDefs assembles the per-request tool list: registry tools
// (with DeferLoading set per the always-loaded set) followed by the
// server-side tool_search_tool. We let the system block's existing
// cache_control mark the cache boundary; a tool-block cache_control
// here didn't seem to trigger writes in initial testing.
func buildToolDefs(registry *tools.Registry, caller *services.Caller) []anthropic.Tool {
	defs := registry.DefinitionsFor(caller)
	defs = append(defs, anthropic.ToolSearchRegex())
	return defs
}

func countLoadedDeferred(defs []anthropic.Tool) (loaded, deferred int) {
	for _, t := range defs {
		if t.DeferLoading {
			deferred++
		} else {
			loaded++
		}
	}
	return
}

func (a *Agent) callLLM(ctx context.Context, tenantID, sessionID uuid.UUID, system []anthropic.SystemBlock, messages []anthropic.Message, toolDefs []anthropic.Tool, iteration int, iterStart time.Time) (*anthropic.Response, error) {
	loaded, deferred := countLoadedDeferred(toolDefs)
	_ = models.AppendSessionEvent(ctx, a.pool, tenantID, sessionID, models.EventTypeLLMRequest, map[string]any{
		"model":               modelHaiku,
		"iteration":           iteration,
		"tool_count_loaded":   loaded,
		"tool_count_deferred": deferred,
	})
	resp, err := a.llm.CreateMessage(ctx, &anthropic.Request{
		Model:        modelHaiku,
		MaxTokens:    maxTokens,
		System:       system,
		Messages:     messages,
		Tools:        toolDefs,
		CacheControl: anthropic.Ephemeral(),
	})
	if err != nil {
		slog.Error("llm call failed", "error", err, "iteration", iteration)
		_ = models.AppendSessionEvent(ctx, a.pool, tenantID, sessionID, models.EventTypeError, map[string]any{
			"error":     err.Error(),
			"iteration": iteration,
		})
		return nil, fmt.Errorf("llm call: %w", err)
	}
	_ = models.AppendSessionEvent(ctx, a.pool, tenantID, sessionID, models.EventTypeLLMResponse, map[string]any{
		"model":                       resp.Model,
		"stop_reason":                 resp.StopReason,
		"input_tokens":                resp.Usage.InputTokens,
		"output_tokens":               resp.Usage.OutputTokens,
		"cache_creation_input_tokens": resp.Usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     resp.Usage.CacheReadInputTokens,
		"duration_ms":                 time.Since(iterStart).Milliseconds(),
		"iteration":                   iteration,
	})
	return resp, nil
}

// handleEndTurn emits any trailing assistant text. Bot-initiated runs
// (scheduled tasks, decision resolves) post via explicit tools, so any
// stray final text is a terse "Done." and is dropped. Returns true if a
// message was sent to the user.
func (a *Agent) handleEndTurn(ctx context.Context, ec *tools.ExecContext, in RunInput, resp *anthropic.Response) bool {
	text := resp.TextContent()
	if text == "" || in.Session.BotInitiated {
		return false
	}
	_ = models.AppendSessionEvent(ctx, a.pool, in.Tenant.ID, in.Session.ID, models.EventTypeAssistantTurn, map[string]any{
		"content": resp.Content,
	})
	if ec.Responder != nil {
		return ec.Responder.Send(ctx, text) == nil
	}
	_ = in.Slack.PostMessage(ctx, in.Channel, in.ThreadTS, text)
	return true
}

// executeTools runs each tool_use block in resp.Content, collects tool_result
// blocks, and reports whether any tool was terminal (i.e. already posted the
// final message, so the loop should stop).
//
// If any tool returns Halted=true (the PolicyGate path minted a decision
// card instead of running the handler), we short-circuit the remaining
// tool_use blocks with synthetic "skipped" tool_results. The Anthropic
// API contract requires every tool_use in an assistant turn to be paired
// with a tool_result in the next user turn, so we must emit *something*
// for each remaining ID, but we don't want to run more tools under the
// agent's now-false belief that the halted tool succeeded.
func (a *Agent) executeTools(ec *tools.ExecContext, registry *tools.Registry, resp *anthropic.Response, status *statusTracker, channel string) ([]anthropic.Content, bool) {
	var toolResults []anthropic.Content
	terminal := false
	halted := false
	sessionID := ec.Session.ID

	for _, toolUse := range resp.ToolUses() {
		inputJSON, _ := json.Marshal(toolUse.Input)

		if halted {
			// Prior tool in this turn halted the run. Emit a synthetic
			// tool_result so the API accepts the message pairing, but
			// don't invoke the tool.
			toolResults = append(toolResults, anthropic.Content{
				Type:      "tool_result",
				ToolUseID: toolUse.ID,
				Content:   "skipped — prior tool halted this turn",
			})
			continue
		}

		slog.Info("executing tool", "tool", toolUse.Name, "input", string(inputJSON), "session_id", sessionID)

		if status != nil {
			status.addTool(ec.Ctx, toolUse.Name)
		}
		if ec.OnToolCall != nil {
			ec.OnToolCall(toolUse.Name)
		}

		res, err := registry.ExecuteWithResult(ec, toolUse.Name, inputJSON)
		result := res.Output
		if err != nil {
			slog.Error("tool execution failed", "tool", toolUse.Name, "error", err, "session_id", sessionID)
			result = "Error: " + err.Error()
		} else {
			slog.Info("tool result", "tool", toolUse.Name, "result", result, "halted", res.Halted, "session_id", sessionID)
		}

		toolResults = append(toolResults, anthropic.Content{
			Type:      "tool_result",
			ToolUseID: toolUse.ID,
			Content:   result,
		})

		if res.Halted {
			// The gate fired. Future tools in this same turn get
			// skipped, and we mark the turn terminal so the outer loop
			// stops — the agent shouldn't get another chance to act
			// after being told HALTED.
			halted = true
			terminal = true
			continue
		}

		if registry.IsTerminal(toolUse.Name, inputJSON, channel) {
			terminal = true
		}
	}
	return toolResults, terminal
}

func (a *Agent) sendFallback(ctx context.Context, ec *tools.ExecContext, in RunInput) {
	fallback := "I'm sorry, I wasn't able to process your request. Please try again."
	if ec.Responder != nil {
		_ = ec.Responder.Send(ctx, fallback)
		return
	}
	_ = in.Slack.PostMessage(ctx, in.Channel, in.ThreadTS, fallback)
}

// historyOptions tunes replay behavior per caller. The defaults (zero
// values) preserve today's full-replay behavior for Slack and task
// callers; the card-chat path sets both to bound memory growth.
type historyOptions struct {
	// windowPairs > 0 caps replayed user/assistant message pairs at the
	// last N (decision_resolved events count as user messages). 0 =
	// unbounded (today's behavior).
	windowPairs int
	// dropToolResults suppresses tool_results replay entirely. Set
	// together with windowPairs for chat paths — tool-result blocks can
	// carry multi-KB echoed args that dominate context on revise loops.
	dropToolResults bool
}

func (a *Agent) rebuildHistory(ctx context.Context, tenant *models.Tenant, session *models.Session, opts historyOptions) []anthropic.Message {
	events, err := models.GetSessionEvents(ctx, a.pool, tenant.ID, session.ID)
	if err != nil {
		slog.Error("loading session history", "error", err)
		return nil
	}

	var messages []anthropic.Message
	for _, evt := range events {
		switch evt.EventType {
		case models.EventTypeMessageReceived:
			var data struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(evt.Data, &data) == nil && data.Text != "" {
				messages = append(messages, anthropic.Message{
					Role:    "user",
					Content: []anthropic.Content{{Type: "text", Text: data.Text}},
				})
			}

		case models.EventTypeAssistantTurn:
			var data struct {
				Content []anthropic.Content `json:"content"`
			}
			if json.Unmarshal(evt.Data, &data) == nil && len(data.Content) > 0 {
				messages = append(messages, anthropic.Message{
					Role:    "assistant",
					Content: data.Content,
				})
			}

		case models.EventTypeToolResults:
			// Chat-path callers drop these from replay; they can carry
			// multi-KB of echoed tool arguments that blow up context on
			// iterated revise loops.
			if opts.dropToolResults {
				continue
			}
			var data struct {
				Content []anthropic.Content `json:"content"`
			}
			if json.Unmarshal(evt.Data, &data) == nil && len(data.Content) > 0 {
				messages = append(messages, anthropic.Message{
					Role:    "user",
					Content: data.Content,
				})
			}

		case models.EventTypeDecisionResolved:
			var data struct {
				CardTitle     string          `json:"card_title"`
				OptionLabel   string          `json:"option_label"`
				ResolvedBy    string          `json:"resolved_by"`
				ToolName      string          `json:"tool_name,omitempty"`
				ToolArguments json.RawMessage `json:"tool_arguments,omitempty"`
				ToolResult    string          `json:"tool_result,omitempty"`
				CardID        string          `json:"card_id,omitempty"`
			}
			if json.Unmarshal(evt.Data, &data) == nil && data.OptionLabel != "" {
				var text string
				switch {
				case data.ToolName != "" && data.ToolResult != "":
					text = fmt.Sprintf(
						"Decision %q was resolved with option %q by %s. "+
							"Tool `%s` executed successfully and returned:\n%s\n\n"+
							"(Call get_decision_tool_result with card_id=%s for the full output if needed.)",
						data.CardTitle, data.OptionLabel, data.ResolvedBy,
						data.ToolName, data.ToolResult, data.CardID,
					)
				case data.ToolName != "":
					text = fmt.Sprintf(
						"Decision %q was resolved with option %q by %s. Tool `%s` executed (no output returned).",
						data.CardTitle, data.OptionLabel, data.ResolvedBy, data.ToolName,
					)
				default:
					text = fmt.Sprintf("Decision %q was resolved with option %q by %s.",
						data.CardTitle, data.OptionLabel, data.ResolvedBy)
				}
				messages = append(messages, anthropic.Message{
					Role:    "user",
					Content: []anthropic.Content{{Type: "text", Text: text}},
				})
			}

		case models.EventTypeMessageSent,
			models.EventTypeLLMRequest,
			models.EventTypeLLMResponse,
			models.EventTypeError,
			models.EventTypeSessionComplete:
			// Diagnostic / telemetry events — not part of the conversation.
		}
	}

	if opts.windowPairs > 0 {
		messages = tailUserAssistantWindow(messages, opts.windowPairs)
	}

	return sanitizeHistory(messages)
}

// tailUserAssistantWindow keeps only the last N user/assistant pairs
// of messages. Counting is approximate — we walk backward from the
// end, counting user-role messages as pair starts; once we've passed N
// user messages we cut everything before that point. Tool_results
// already skipped at the switch level; this just bounds the
// conversation-shaped messages.
func tailUserAssistantWindow(messages []anthropic.Message, n int) []anthropic.Message {
	if n <= 0 || len(messages) == 0 {
		return messages
	}
	// Walk backward: count user-role messages; stop when we've seen n+1
	// (so we keep exactly n pairs worth of context before the current
	// message). The caller appends the *current* user message after
	// this function returns, so we want n prior pairs.
	userSeen := 0
	cut := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			userSeen++
			if userSeen > n {
				cut = i + 1
				break
			}
		}
	}
	if cut == 0 {
		return messages
	}
	return messages[cut:]
}

// sanitizeHistory removes orphaned tool_use/tool_result pairs that would
// cause the API to reject the request. An assistant message with tool_use
// blocks must be immediately followed by a user message with matching
// tool_result blocks.
//
// It also strips server-side blocks (server_tool_use,
// tool_search_tool_result) from assistant messages: those were live
// expansions emitted by the API during the original turn, but on replay
// the client doesn't need to (and shouldn't try to) reassert them — the
// API has the deferred tool registrations directly in the request.
func sanitizeHistory(messages []anthropic.Message) []anthropic.Message {
	for i := range messages {
		if messages[i].Role == "assistant" {
			messages[i].Content = stripServerBlocks(messages[i].Content)
		}
	}

	var clean []anthropic.Message
	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		if msg.Role == "assistant" {
			hasToolUse := false
			for _, c := range msg.Content {
				if c.Type == "tool_use" {
					hasToolUse = true
					break
				}
			}
			if hasToolUse {
				if i+1 < len(messages) && hasToolResults(messages[i+1]) {
					clean = append(clean, msg, messages[i+1])
					i++
					continue
				}
				slog.Warn("dropping orphaned tool_use from history", "index", i)
				continue
			}
		}

		clean = append(clean, msg)
	}
	return clean
}

func stripServerBlocks(blocks []anthropic.Content) []anthropic.Content {
	var out []anthropic.Content
	for _, c := range blocks {
		switch c.Type {
		case "server_tool_use", "tool_search_tool_result":
			continue
		}
		out = append(out, c)
	}
	return out
}

func hasToolResults(msg anthropic.Message) bool {
	if msg.Role != "user" {
		return false
	}
	for _, c := range msg.Content {
		if c.Type == "tool_result" {
			return true
		}
	}
	return false
}

// statusTracker posts and updates a live status message in Slack.
type statusTracker struct {
	slack    *kitslack.Client
	channel  string
	threadTS string
	msgTS    string // timestamp of the status message
	tools    []string
}

func newStatusTracker(slack *kitslack.Client, channel, threadTS string) *statusTracker {
	return &statusTracker{slack: slack, channel: channel, threadTS: threadTS}
}

func (s *statusTracker) update(ctx context.Context, status string) {
	text := s.render(status)
	if s.msgTS == "" {
		ts, err := s.slack.PostMessageReturningTS(ctx, s.channel, s.threadTS, text)
		if err != nil {
			slog.Warn("posting status message", "error", err, "channel", s.channel, "thread_ts", s.threadTS)
			return
		}
		s.msgTS = ts
	} else {
		if err := s.slack.UpdateMessage(ctx, s.channel, s.msgTS, text); err != nil {
			slog.Warn("updating status message", "error", err, "channel", s.channel, "msg_ts", s.msgTS)
		}
	}
}

func (s *statusTracker) addTool(ctx context.Context, name string) {
	// Server-side tool search is internal routing, not user-visible work.
	if strings.HasPrefix(name, "tool_search_tool") {
		return
	}
	s.tools = append(s.tools, name)
	s.update(ctx, "")
}

func (s *statusTracker) render(status string) string {
	var b strings.Builder
	for _, t := range s.tools {
		b.WriteString("• `" + t + "`\n")
	}
	if status != "" {
		b.WriteString("_" + status + "_")
	}
	if b.Len() == 0 {
		return "_Thinking..._"
	}
	return b.String()
}

func (s *statusTracker) cleanup(ctx context.Context) {
	if s.msgTS != "" {
		_ = s.slack.DeleteMessage(ctx, s.channel, s.msgTS)
	}
}
