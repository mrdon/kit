package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

const (
	maxIterations = 10
	modelHaiku    = "claude-haiku-4-5-20251001"
	maxTokens     = 4096
)

// Agent runs the observe/reason/act loop for a single message.
type Agent struct {
	pool     *pgxpool.Pool
	llm      *anthropic.Client
	registry *ToolRegistry
}

// NewAgent creates a new agent instance.
func NewAgent(pool *pgxpool.Pool, llm *anthropic.Client) *Agent {
	return &Agent{
		pool: pool,
		llm:  llm,
	}
}

// Run executes the agent loop for a user message.
func (a *Agent) Run(ctx context.Context, slack *kitslack.Client, tenant *models.Tenant, user *models.User, session *models.Session, channel, threadTS, userText string) error {
	start := time.Now()

	// Build tool registry based on user permissions
	registry := NewToolRegistry()
	registry.RegisterUserTools()
	if user.IsAdmin {
		registry.RegisterAdminTools()
	}

	ec := &ExecContext{
		Ctx:      ctx,
		Pool:     a.pool,
		Slack:    slack,
		Tenant:   tenant,
		User:     user,
		Session:  session,
		Channel:  channel,
		ThreadTS: threadTS,
	}

	// Log incoming message
	_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "message_received", map[string]any{
		"user_id": user.ID,
		"text":    userText,
		"channel": channel,
	})

	// Rebuild conversation history from session events
	messages := a.rebuildHistory(ctx, tenant, session)

	// Append current user message
	messages = append(messages, anthropic.Message{
		Role:    "user",
		Content: []anthropic.Content{{Type: "text", Text: userText}},
	})

	// Build system prompt
	systemPrompt := BuildSystemPrompt(ctx, a.pool, tenant, user)

	// Agent loop
	sentMessage := false
	for i := range maxIterations {
		iterStart := time.Now()

		// Log LLM request
		_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "llm_request", map[string]any{
			"model":     modelHaiku,
			"iteration": i,
		})

		resp, err := a.llm.CreateMessage(ctx, &anthropic.Request{
			Model:     modelHaiku,
			MaxTokens: maxTokens,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     registry.Definitions(),
		})
		if err != nil {
			slog.Error("llm call failed", "error", err, "iteration", i)
			_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "error", map[string]any{
				"error":     err.Error(),
				"iteration": i,
			})
			return fmt.Errorf("llm call: %w", err)
		}

		// Log LLM response
		_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "llm_response", map[string]any{
			"model":         resp.Model,
			"stop_reason":   resp.StopReason,
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"duration_ms":   time.Since(iterStart).Milliseconds(),
			"iteration":     i,
		})

		// If model returned end_turn with no tool calls, it tried to respond directly
		if resp.StopReason == "end_turn" && len(resp.ToolUses()) == 0 {
			// Model responded with text instead of tool call — send it as fallback
			text := resp.TextContent()
			if text != "" {
				_ = slack.PostMessage(ctx, channel, threadTS, text)
				sentMessage = true
			}
			break
		}

		// Append assistant response to messages
		messages = append(messages, anthropic.Message{
			Role:    "assistant",
			Content: resp.Content,
		})

		// Process tool calls
		if resp.StopReason == "tool_use" {
			var toolResults []anthropic.Content

			for _, toolUse := range resp.ToolUses() {
				inputJSON, _ := json.Marshal(toolUse.Input)

				// Log tool call
				_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "tool_call", map[string]any{
					"tool":  toolUse.Name,
					"input": string(inputJSON),
				})

				slog.Info("executing tool", "tool", toolUse.Name, "session_id", session.ID)

				result, err := registry.Execute(ec, toolUse.Name, inputJSON)
				if err != nil {
					slog.Error("tool execution failed", "tool", toolUse.Name, "error", err)
					result = fmt.Sprintf("Error: %s", err.Error())
				}

				// Log tool result
				_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "tool_result", map[string]any{
					"tool":   toolUse.Name,
					"result": result,
				})

				toolResults = append(toolResults, anthropic.Content{
					Type:      "tool_result",
					ToolUseID: toolUse.ID,
					Content:   result,
				})

				if IsTerminal(toolUse.Name) {
					sentMessage = true
				}
			}

			// Append tool results as user message
			messages = append(messages, anthropic.Message{
				Role:    "user",
				Content: toolResults,
			})

			// If a terminal tool was called, stop the loop
			if sentMessage {
				break
			}
		}
	}

	// Fallback if agent never sent a message
	if !sentMessage {
		_ = slack.PostMessage(ctx, channel, threadTS, "I'm sorry, I wasn't able to process your request. Please try again.")
	}

	// Log session completion
	_ = models.AppendSessionEvent(ctx, a.pool, tenant.ID, session.ID, "session_complete", map[string]any{
		"duration_ms": time.Since(start).Milliseconds(),
	})

	return nil
}

// rebuildHistory reconstructs the conversation from session events.
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

		case "message_sent":
			var data struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(evt.Data, &data) == nil && data.Text != "" {
				messages = append(messages, anthropic.Message{
					Role:    "assistant",
					Content: []anthropic.Content{{Type: "text", Text: data.Text}},
				})
			}
		}
	}

	return messages
}
