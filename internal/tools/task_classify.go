package tools

import (
	"context"
	"log/slog"
	"strings"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
)

// classifyTaskModelSystem is the fixed system prompt for the one-shot
// Haiku pass that decides which tier a scheduled task runs under. The
// prompt gives examples from both poles so Haiku has concrete anchors
// instead of classifying from the criteria alone.
const classifyTaskModelSystem = `You are classifying a scheduled-task prompt for a work assistant. The user's prompt will run unattended on a cron. Decide which model tier should execute it and respond with exactly one lowercase word: ` + "`haiku`" + ` or ` + "`sonnet`" + `. No punctuation, no explanation.

Return ` + "`sonnet`" + ` when the task involves nuanced judgment, personalized writing, creative drafting, multi-step reasoning over unstructured content, or matching a human's voice. Examples:
- "Every morning review my open todos and draft a reply for anything I could start."
- "Summarize customer feedback from Slack and propose three changes worth making."
- "Read my inbox and write personalised follow-ups for anything that looks like a warm lead."

Return ` + "`haiku`" + ` for reminders, lookups, summaries with no judgment call, simple orchestration, notifications, or straightforward classification. Examples:
- "At 3pm every weekday, DM me to stand up and stretch."
- "Every Monday list my open todos in #standup."
- "Every morning post the date and top three calendar events for the day."

When in doubt, choose ` + "`haiku`" + ` — it's cheaper and fine for most schedules.`

// ClassifyTaskModel runs one Haiku call against the task description and
// returns the chosen tier name (models.TaskModelHaiku or
// models.TaskModelSonnet). A nil client, API error, or unparseable reply
// all fall back to Haiku — classification failure must never block task
// creation.
func ClassifyTaskModel(ctx context.Context, llm *anthropic.Client, description string) string {
	if llm == nil || strings.TrimSpace(description) == "" {
		return models.TaskModelHaiku
	}
	resp, err := llm.CreateMessage(ctx, &anthropic.Request{
		Model:     models.ModelIDFor(models.TaskModelHaiku),
		MaxTokens: 8,
		System: []anthropic.SystemBlock{{
			Type: "text",
			Text: classifyTaskModelSystem,
		}},
		Messages: []anthropic.Message{{
			Role: "user",
			Content: []anthropic.Content{{
				Type: "text",
				Text: description,
			}},
		}},
	})
	if err != nil {
		slog.Warn("classify task model failed, defaulting to haiku", "error", err)
		return models.TaskModelHaiku
	}
	raw := strings.ToLower(strings.TrimSpace(resp.TextContent()))
	raw = strings.Trim(raw, ".,;:!?\"'` \t\n")
	if raw == models.TaskModelSonnet {
		slog.Info("task classified as sonnet", "description_preview", previewDescription(description))
		return models.TaskModelSonnet
	}
	return models.TaskModelHaiku
}

func previewDescription(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 120 {
		return s
	}
	return s[:119] + "…"
}
