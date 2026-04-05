package ingest

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/anthropic"
)

const modelSonnet = "claude-sonnet-4-5-20241022"

// ConvertToSkill uses Claude Sonnet to structure raw text into a skill.
func ConvertToSkill(ctx context.Context, llm *anthropic.Client, filename, rawText string) (name, description, content string, err error) {
	prompt := fmt.Sprintf(`You are converting an uploaded document into a structured knowledge article for an AI assistant.

Document filename: %s

Document content:
---
%s
---

Create a structured knowledge article from this document. Respond in exactly this format:

NAME: <A short, descriptive name for this knowledge article>
DESCRIPTION: <A one-sentence summary of what this article covers>
CONTENT:
<The full content, cleaned up and formatted as markdown. Preserve all important information. Organize with headers where appropriate.>`, filename, rawText)

	resp, err := llm.CreateMessage(ctx, &anthropic.Request{
		Model:     modelSonnet,
		MaxTokens: 8192,
		Messages: []anthropic.Message{
			{Role: "user", Content: []anthropic.Content{{Type: "text", Text: prompt}}},
		},
	})
	if err != nil {
		return "", "", "", fmt.Errorf("llm conversion: %w", err)
	}

	text := resp.TextContent()
	return parseConversionResponse(text, filename)
}

func parseConversionResponse(text, fallbackName string) (name, description, content string, err error) {
	// Parse the structured response
	lines := splitLines(text)
	state := "init"
	var contentLines []string

	for _, line := range lines {
		switch {
		case state == "init" && hasPrefix(line, "NAME:"):
			name = trimPrefix(line, "NAME:")
			state = "after_name"
		case (state == "after_name" || state == "init") && hasPrefix(line, "DESCRIPTION:"):
			description = trimPrefix(line, "DESCRIPTION:")
			state = "after_desc"
		case (state == "after_desc" || state == "after_name") && hasPrefix(line, "CONTENT:"):
			state = "content"
		case state == "content":
			contentLines = append(contentLines, line)
		}
	}

	if name == "" {
		name = fallbackName
	}
	if description == "" {
		description = "Uploaded document: " + fallbackName
	}
	content = joinLines(contentLines)
	if content == "" {
		content = text // Fallback: use the raw LLM output
	}

	return name, description, content, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(lines[0])
	for _, l := range lines[1:] {
		b.WriteString("\n")
		b.WriteString(l)
	}
	return b.String()
}

func hasPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range len(prefix) {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

func trimPrefix(s, prefix string) string {
	s = s[len(prefix):]
	// Trim leading spaces
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s
}
