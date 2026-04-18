package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

// Run executes the agent loop for a user message.
func (a *Agent) Run(ctx context.Context, slack *kitslack.Client, tenant *models.Tenant, user *models.User, session *models.Session, channel, threadTS, userText string, taskCtx *TaskContext) error {
	start := time.Now()

	registry := tools.NewRegistry(user.IsAdmin)

	ec := &tools.ExecContext{
		Ctx:      ctx,
		Pool:     a.pool,
		Slack:    slack,
		Fetcher:  a.fetcher,
		Tenant:   tenant,
		User:     user,
		Session:  session,
		Channel:  channel,
		ThreadTS: threadTS,
		Svc:      a.svc,
	}

	// Post status message immediately so user sees feedback (skip for tasks)
	var status *statusTracker
	if taskCtx == nil {
		status = newStatusTracker(slack, channel, threadTS)
		status.update(ctx, "Thinking...")
		defer status.cleanup(ctx)
	}

	_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "message_received", map[string]any{
		"user_id": user.ID,
		"text":    userText,
		"channel": channel,
	})

	messages := a.rebuildHistory(ctx, tenant, session)
	currentTime := fmt.Sprintf("[Current time: %s UTC]", time.Now().UTC().Format("2006-01-02T15:04"))
	messages = append(messages, anthropic.Message{
		Role:    "user",
		Content: []anthropic.Content{{Type: "text", Text: currentTime + "\n" + userText}},
	})

	systemPrompt := []anthropic.SystemBlock{
		{
			Type:         "text",
			Text:         BuildSystemPrompt(ctx, a.pool, tenant, user, taskCtx),
			CacheControl: anthropic.Ephemeral(),
		},
	}
	toolDefs := registry.Definitions()

	sentMessage := false
	var totalIn, totalOut, totalCacheRead, totalCacheWrite int

	for i := range maxIterations {
		iterStart := time.Now()
		if status != nil {
			status.update(ctx, "Thinking...")
		}

		_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "llm_request", map[string]any{
			"model":     modelHaiku,
			"iteration": i,
		})

		resp, err := a.llm.CreateMessage(ctx, &anthropic.Request{
			Model:        modelHaiku,
			MaxTokens:    maxTokens,
			System:       systemPrompt,
			Messages:     messages,
			Tools:        toolDefs,
			CacheControl: anthropic.Ephemeral(),
		})
		if err != nil {
			slog.Error("llm call failed", "error", err, "iteration", i)
			_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "error", map[string]any{
				"error":     err.Error(),
				"iteration": i,
			})
			return fmt.Errorf("llm call: %w", err)
		}

		totalIn += resp.Usage.InputTokens
		totalOut += resp.Usage.OutputTokens
		totalCacheRead += resp.Usage.CacheReadInputTokens
		totalCacheWrite += resp.Usage.CacheCreationInputTokens

		_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "llm_response", map[string]any{
			"model":                       resp.Model,
			"stop_reason":                 resp.StopReason,
			"input_tokens":                resp.Usage.InputTokens,
			"output_tokens":               resp.Usage.OutputTokens,
			"cache_creation_input_tokens": resp.Usage.CacheCreationInputTokens,
			"cache_read_input_tokens":     resp.Usage.CacheReadInputTokens,
			"duration_ms":                 time.Since(iterStart).Milliseconds(),
			"iteration":                   i,
		})

		if resp.StopReason == "end_turn" && len(resp.ToolUses()) == 0 {
			text := resp.TextContent()
			if text != "" {
				_ = slack.PostMessage(ctx, channel, threadTS, text)
				sentMessage = true
			}
			break
		}

		_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "assistant_turn", map[string]any{
			"content": resp.Content,
		})
		messages = append(messages, anthropic.Message{
			Role:    "assistant",
			Content: resp.Content,
		})

		if resp.StopReason == "tool_use" {
			var toolResults []anthropic.Content

			for _, toolUse := range resp.ToolUses() {
				inputJSON, _ := json.Marshal(toolUse.Input)
				slog.Info("executing tool", "tool", toolUse.Name, "input", string(inputJSON), "session_id", session.ID)

				if status != nil {
					status.addTool(ctx, toolUse.Name)
				}

				result, err := registry.Execute(ec, toolUse.Name, inputJSON)
				if err != nil {
					slog.Error("tool execution failed", "tool", toolUse.Name, "error", err, "session_id", session.ID)
					result = "Error: " + err.Error()
				} else {
					slog.Info("tool result", "tool", toolUse.Name, "result", result, "session_id", session.ID)
				}

				toolResults = append(toolResults, anthropic.Content{
					Type:      "tool_result",
					ToolUseID: toolUse.ID,
					Content:   result,
				})

				if registry.IsTerminal(toolUse.Name, inputJSON, channel) {
					sentMessage = true
				}
			}

			_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "tool_results", map[string]any{
				"content": toolResults,
			})
			messages = append(messages, anthropic.Message{
				Role:    "user",
				Content: toolResults,
			})

			if sentMessage {
				break
			}
		}
	}

	if !sentMessage {
		_ = slack.PostMessage(ctx, channel, threadTS, "I'm sorry, I wasn't able to process your request. Please try again.")
	}

	_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "session_complete", map[string]any{
		"duration_ms":      time.Since(start).Milliseconds(),
		"total_input":      totalIn,
		"total_output":     totalOut,
		"total_cache_read": totalCacheRead,
	})

	return nil
}

func (a *Agent) rebuildHistory(ctx context.Context, tenant *models.Tenant, session *models.Session) []anthropic.Message {
	events, err := models.GetSessionEvents(ctx, a.pool, tenant.ID, session.ID)
	if err != nil {
		slog.Error("loading session history", "error", err)
		return nil
	}

	var messages []anthropic.Message
	for _, evt := range events {
		switch evt.EventType {
		case "message_received":
			var data struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(evt.Data, &data) == nil && data.Text != "" {
				messages = append(messages, anthropic.Message{
					Role:    "user",
					Content: []anthropic.Content{{Type: "text", Text: data.Text}},
				})
			}

		case "assistant_turn":
			var data struct {
				Content []anthropic.Content `json:"content"`
			}
			if json.Unmarshal(evt.Data, &data) == nil && len(data.Content) > 0 {
				messages = append(messages, anthropic.Message{
					Role:    "assistant",
					Content: data.Content,
				})
			}

		case "tool_results":
			var data struct {
				Content []anthropic.Content `json:"content"`
			}
			if json.Unmarshal(evt.Data, &data) == nil && len(data.Content) > 0 {
				messages = append(messages, anthropic.Message{
					Role:    "user",
					Content: data.Content,
				})
			}
		}
	}

	return sanitizeHistory(messages)
}

// sanitizeHistory removes orphaned tool_use/tool_result pairs that would
// cause the API to reject the request. An assistant message with tool_use
// blocks must be immediately followed by a user message with matching
// tool_result blocks.
func sanitizeHistory(messages []anthropic.Message) []anthropic.Message {
	var clean []anthropic.Message
	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		// Check if this assistant message has tool_use blocks
		if msg.Role == "assistant" {
			hasToolUse := false
			for _, c := range msg.Content {
				if c.Type == "tool_use" {
					hasToolUse = true
					break
				}
			}
			if hasToolUse {
				// Next message must be user with tool_result blocks
				if i+1 < len(messages) && hasToolResults(messages[i+1]) {
					clean = append(clean, msg, messages[i+1])
					i++ // skip the tool_results message, already added
					continue
				}
				// Orphaned tool_use — skip it
				slog.Warn("dropping orphaned tool_use from history", "index", i)
				continue
			}
		}

		clean = append(clean, msg)
	}
	return clean
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
