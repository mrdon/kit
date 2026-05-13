package widget

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

func registerWidgetAnalyticsTools(r *tools.Registry, _ bool) {
	for _, meta := range services.WidgetAnalyticsTools {
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			Handler:     widgetAnalyticsHandler(meta.Name),
		})
	}
}

func widgetAnalyticsHandler(name string) tools.HandlerFunc {
	switch name {
	case "list_widget_conversations":
		return handleListWidgetConversations
	case "search_widget_conversations":
		return handleSearchWidgetConversations
	case "read_widget_conversation":
		return handleReadWidgetConversation
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown widget analytics tool: %s", name)
		}
	}
}

func handleListWidgetConversations(ec *tools.ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Since     string `json:"since"`
		Until     string `json:"until"`
		Origin    string `json:"origin"`
		VisitorID string `json:"visitor_id"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	since, err := parseFlexibleTime(inp.Since)
	if err != nil {
		return "Invalid 'since' value: " + err.Error(), nil
	}
	until, err := parseFlexibleTime(inp.Until)
	if err != nil {
		return "Invalid 'until' value: " + err.Error(), nil
	}
	convs, err := ec.Svc.WidgetAnalytics.List(ec.Ctx, ec.Caller(), since, until, inp.Origin, inp.VisitorID, inp.Limit)
	if err != nil {
		return "", err
	}
	if len(convs) == 0 {
		return "No widget conversations match.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Widget conversations (%d):\n", len(convs))
	for _, c := range convs {
		marker := ""
		if c.LooksUnanswered {
			marker = " [looks_unanswered]"
		}
		fmt.Fprintf(&b, "- [%s]%s started=%s msgs=%d origin=%s visitor=%s\n  first: %q\n",
			c.SessionID, marker, c.StartedAt.UTC().Format("2006-01-02 15:04"),
			c.MessageCount, c.Origin, c.VisitorID, c.FirstUserMessage)
	}
	return b.String(), nil
}

func handleSearchWidgetConversations(ec *tools.ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Query string `json:"query"`
		Since string `json:"since"`
		Until string `json:"until"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	since, err := parseFlexibleTime(inp.Since)
	if err != nil {
		return "Invalid 'since' value: " + err.Error(), nil
	}
	until, err := parseFlexibleTime(inp.Until)
	if err != nil {
		return "Invalid 'until' value: " + err.Error(), nil
	}
	hits, err := ec.Svc.WidgetAnalytics.Search(ec.Ctx, ec.Caller(), inp.Query, since, until, inp.Limit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "No matches.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Widget conversation matches (%d):\n", len(hits))
	for _, h := range hits {
		fmt.Fprintf(&b, "- [%s] %s @ %s: %s\n",
			h.SessionID, h.MatchedRole, h.StartedAt.UTC().Format("2006-01-02 15:04"), h.Snippet)
	}
	return b.String(), nil
}

func handleReadWidgetConversation(ec *tools.ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	id, err := uuid.Parse(inp.SessionID)
	if err != nil {
		return "Invalid session ID.", nil
	}
	t, err := ec.Svc.WidgetAnalytics.Read(ec.Ctx, ec.Caller(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return "Conversation not found.", nil
		}
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Conversation [%s] started=%s origin=%s visitor=%s\n",
		t.SessionID, t.StartedAt.UTC().Format("2006-01-02 15:04"), t.Origin, t.VisitorID)
	for _, m := range t.Messages {
		fmt.Fprintf(&b, "  %s [%s]: %s\n", strings.ToUpper(m.Role), m.At.UTC().Format("15:04"), m.Text)
	}
	if len(t.ToolCalls) > 0 {
		b.WriteString("  tools: ")
		var names []string
		for _, tc := range t.ToolCalls {
			names = append(names, tc.Name)
		}
		b.WriteString(strings.Join(names, ", "))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// parseFlexibleTime accepts RFC3339, a date-only "2006-01-02" string,
// or empty (returning nil for "no bound"). Returns the parsed time as
// a *time.Time so the service can distinguish "unset" from "epoch."
func parseFlexibleTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // empty input ⇒ no bound
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("expected RFC3339 or YYYY-MM-DD, got %q", s)
}
