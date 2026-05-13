package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
)

// WidgetAnalyticsTools defines shared tool metadata for inspecting
// conversations that came in via the website chat widget. All three
// tools are non-admin: any tenant member can read widget conversations
// because they're anonymous-visitor data, not user-private.
//
// Hard-coded scope: every query filters on
//
//	`sessions.slack_channel_id = 'web:widget'` AND `tenant_id = caller.TenantID`.
//
// No user_id/channel input parameter is accepted so the LLM cannot
// broaden the filter to peek into Slack DMs or other surfaces.
var WidgetAnalyticsTools = []ToolMeta{
	{Name: "list_widget_conversations", Description: "List recent website chat-widget conversations for this tenant. Returns each conversation's first user message, message count, and a heuristic 'looks_unanswered' flag.", Schema: props(map[string]any{
		"since":      field("string", "Earliest started_at (RFC3339 or 'YYYY-MM-DD'). Defaults to 7 days ago."),
		"until":      field("string", "Latest started_at (RFC3339 or 'YYYY-MM-DD'). Defaults to now."),
		"origin":     field("string", "Filter by Origin header recorded at conversation start."),
		"visitor_id": field("string", "Filter by browser visitor_id to group conversations from one browser."),
		"limit":      map[string]any{"type": "integer", "description": "Max conversations to return (default 50, max 200)."},
	})},
	{Name: "search_widget_conversations", Description: "Substring-search across user questions and assistant replies in widget conversations. Returns matching sessions with a short snippet around the match.", Schema: propsReq(map[string]any{
		"query": field("string", "Substring to match, case-insensitive."),
		"since": field("string", "Earliest started_at (RFC3339 or 'YYYY-MM-DD'). Defaults to 30 days ago."),
		"until": field("string", "Latest started_at (RFC3339 or 'YYYY-MM-DD'). Defaults to now."),
		"limit": map[string]any{"type": "integer", "description": "Max matches to return (default 20, max 100)."},
	}, "query")},
	{Name: "read_widget_conversation", Description: "Read the full transcript of one widget conversation. Returns the ordered visitor/assistant messages and the tools the agent invoked.", Schema: propsReq(map[string]any{
		"session_id": field("string", "The widget session UUID, from list_widget_conversations or search_widget_conversations."),
	}, "session_id")},
}

// WidgetAnalyticsService runs read-only queries over widget session
// data. Construction is bare; the pool is the only dependency.
type WidgetAnalyticsService struct {
	pool *pgxpool.Pool
}

// NewWidgetAnalyticsService binds the service to a pool.
func NewWidgetAnalyticsService(pool *pgxpool.Pool) *WidgetAnalyticsService {
	return &WidgetAnalyticsService{pool: pool}
}

// WidgetConversation is one row of list_widget_conversations output.
type WidgetConversation struct {
	SessionID        uuid.UUID
	ConversationID   string
	VisitorID        string
	Origin           string
	StartedAt        time.Time
	MessageCount     int
	DurationMs       int64
	FirstUserMessage string
	LooksUnanswered  bool
}

// List returns matching widget conversations, newest first.
func (s *WidgetAnalyticsService) List(ctx context.Context, c *Caller, since, until *time.Time, origin, visitorID string, limit int) ([]WidgetConversation, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if since == nil {
		t := time.Now().Add(-7 * 24 * time.Hour)
		since = &t
	}
	if until == nil {
		t := time.Now()
		until = &t
	}
	// Pull sessions in range, then join their first widget_started event
	// for visitor_id/origin and their first message_received for the
	// kickoff text. Last assistant_turn drives the looks_unanswered
	// heuristic.
	rows, err := s.pool.Query(ctx, `
		WITH widget_sessions AS (
			SELECT id, slack_thread_ts, created_at, updated_at
			FROM sessions
			WHERE tenant_id = $1
			  AND slack_channel_id = $2
			  AND created_at BETWEEN $3 AND $4
		),
		started AS (
			SELECT DISTINCT ON (se.session_id)
			       se.session_id,
			       se.data ->> 'visitor_id'      AS visitor_id,
			       se.data ->> 'origin'          AS origin
			FROM session_events se
			JOIN widget_sessions ws ON ws.id = se.session_id
			WHERE se.tenant_id = $1 AND se.event_type = 'widget_started'
			ORDER BY se.session_id, se.created_at ASC
		),
		first_msg AS (
			SELECT DISTINCT ON (se.session_id)
			       se.session_id,
			       se.data ->> 'text' AS text
			FROM session_events se
			JOIN widget_sessions ws ON ws.id = se.session_id
			WHERE se.tenant_id = $1 AND se.event_type = 'message_received'
			ORDER BY se.session_id, se.created_at ASC
		),
		last_assistant AS (
			SELECT DISTINCT ON (se.session_id)
			       se.session_id,
			       se.data AS data
			FROM session_events se
			JOIN widget_sessions ws ON ws.id = se.session_id
			WHERE se.tenant_id = $1 AND se.event_type = 'assistant_turn'
			ORDER BY se.session_id, se.created_at DESC
		),
		msg_counts AS (
			SELECT se.session_id, COUNT(*) AS n
			FROM session_events se
			JOIN widget_sessions ws ON ws.id = se.session_id
			WHERE se.tenant_id = $1 AND se.event_type = 'message_received'
			GROUP BY se.session_id
		)
		SELECT ws.id, ws.slack_thread_ts, ws.created_at,
		       COALESCE(started.visitor_id, ''), COALESCE(started.origin, ''),
		       COALESCE(msg_counts.n, 0)::int,
		       EXTRACT(EPOCH FROM (ws.updated_at - ws.created_at))::bigint * 1000,
		       COALESCE(first_msg.text, ''),
		       last_assistant.data
		FROM widget_sessions ws
		LEFT JOIN started        ON started.session_id        = ws.id
		LEFT JOIN first_msg      ON first_msg.session_id      = ws.id
		LEFT JOIN last_assistant ON last_assistant.session_id = ws.id
		LEFT JOIN msg_counts     ON msg_counts.session_id     = ws.id
		WHERE ($5::text = '' OR started.origin = $5)
		  AND ($6::text = '' OR started.visitor_id = $6)
		ORDER BY ws.created_at DESC
		LIMIT $7
	`, c.TenantID, models.WidgetChannelID, *since, *until, origin, visitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing widget conversations: %w", err)
	}
	defer rows.Close()

	var out []WidgetConversation
	for rows.Next() {
		var wc WidgetConversation
		var lastAssistantData []byte
		if err := rows.Scan(&wc.SessionID, &wc.ConversationID, &wc.StartedAt,
			&wc.VisitorID, &wc.Origin, &wc.MessageCount, &wc.DurationMs,
			&wc.FirstUserMessage, &lastAssistantData); err != nil {
			return nil, fmt.Errorf("scanning widget conversation: %w", err)
		}
		wc.FirstUserMessage = truncate(wc.FirstUserMessage, 200)
		wc.LooksUnanswered = looksUnanswered(lastAssistantData)
		out = append(out, wc)
	}
	return out, rows.Err()
}

// WidgetSearchHit is one row of search_widget_conversations output.
type WidgetSearchHit struct {
	SessionID      uuid.UUID
	ConversationID string
	StartedAt      time.Time
	MatchedRole    string // "user" or "assistant"
	Snippet        string
}

// Search returns substring matches across widget user messages and
// assistant replies, newest first.
func (s *WidgetAnalyticsService) Search(ctx context.Context, c *Caller, query string, since, until *time.Time, limit int) ([]WidgetSearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if since == nil {
		t := time.Now().Add(-30 * 24 * time.Hour)
		since = &t
	}
	if until == nil {
		t := time.Now()
		until = &t
	}
	// Two-armed ILIKE: messages keep `data->>'text'`; assistant turns
	// flatten the typed content blocks into a single text column.
	rows, err := s.pool.Query(ctx, `
		WITH widget_sessions AS (
			SELECT id, slack_thread_ts, created_at
			FROM sessions
			WHERE tenant_id = $1
			  AND slack_channel_id = $2
			  AND created_at BETWEEN $3 AND $4
		),
		matches AS (
			SELECT ws.id AS session_id,
			       ws.slack_thread_ts,
			       ws.created_at,
			       se.created_at AS evt_at,
			       'user'::text   AS role,
			       (se.data ->> 'text') AS text
			FROM session_events se
			JOIN widget_sessions ws ON ws.id = se.session_id
			WHERE se.tenant_id = $1
			  AND se.event_type = 'message_received'
			  AND (se.data ->> 'text') ILIKE $5
			UNION ALL
			SELECT ws.id AS session_id,
			       ws.slack_thread_ts,
			       ws.created_at,
			       se.created_at AS evt_at,
			       'assistant'::text AS role,
			       (
			         SELECT string_agg(c ->> 'text', ' ')
			         FROM jsonb_array_elements(se.data -> 'content') c
			         WHERE (c ->> 'type') = 'text'
			       ) AS text
			FROM session_events se
			JOIN widget_sessions ws ON ws.id = se.session_id
			WHERE se.tenant_id = $1
			  AND se.event_type = 'assistant_turn'
			  AND EXISTS (
			    SELECT 1
			    FROM jsonb_array_elements(se.data -> 'content') c
			    WHERE (c ->> 'type') = 'text' AND (c ->> 'text') ILIKE $5
			  )
		)
		SELECT session_id, slack_thread_ts, created_at, role, text
		FROM matches
		ORDER BY evt_at DESC
		LIMIT $6
	`, c.TenantID, models.WidgetChannelID, *since, *until, "%"+query+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("searching widget conversations: %w", err)
	}
	defer rows.Close()

	var hits []WidgetSearchHit
	for rows.Next() {
		var h WidgetSearchHit
		var text string
		if err := rows.Scan(&h.SessionID, &h.ConversationID, &h.StartedAt, &h.MatchedRole, &text); err != nil {
			return nil, fmt.Errorf("scanning widget search hit: %w", err)
		}
		h.Snippet = snippet(text, query, 80)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// WidgetTranscript is the output of read_widget_conversation.
type WidgetTranscript struct {
	SessionID      uuid.UUID
	ConversationID string
	VisitorID      string
	Origin         string
	StartedAt      time.Time
	Messages       []WidgetMessage
	ToolCalls      []WidgetToolCall
}

// WidgetMessage is one turn in a widget transcript.
type WidgetMessage struct {
	Role string // "user" or "assistant"
	Text string
	At   time.Time
}

// WidgetToolCall is one tool invocation observed during a widget
// conversation. Surfaced separately from messages so the LLM caller
// can see what the agent reached for without inlining tool noise into
// the transcript.
type WidgetToolCall struct {
	Name string
	At   time.Time
}

// Read returns the full transcript of a widget conversation. The
// session_id is re-validated against the widget channel filter on
// every call so a session_id leaked from another surface (Slack,
// card chat) returns ErrNotFound instead of a transcript.
func (s *WidgetAnalyticsService) Read(ctx context.Context, c *Caller, sessionID uuid.UUID) (*WidgetTranscript, error) {
	sess, err := models.GetSession(ctx, s.pool, c.TenantID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil || sess.SlackChannelID != models.WidgetChannelID {
		return nil, ErrNotFound
	}
	events, err := models.GetSessionEvents(ctx, s.pool, c.TenantID, sessionID)
	if err != nil {
		return nil, err
	}
	t := &WidgetTranscript{
		SessionID:      sess.ID,
		ConversationID: sess.SlackThreadTS,
		StartedAt:      sess.CreatedAt,
	}
	for _, e := range events {
		switch e.EventType {
		case models.EventTypeWidgetStarted:
			var d struct {
				VisitorID string `json:"visitor_id"`
				Origin    string `json:"origin"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				if t.VisitorID == "" {
					t.VisitorID = d.VisitorID
				}
				if t.Origin == "" {
					t.Origin = d.Origin
				}
			}
		case models.EventTypeMessageReceived:
			var d struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(e.Data, &d) == nil && d.Text != "" {
				t.Messages = append(t.Messages, WidgetMessage{Role: "user", Text: d.Text, At: e.CreatedAt})
			}
		case models.EventTypeAssistantTurn:
			text := extractAssistantText(e.Data)
			if text != "" {
				t.Messages = append(t.Messages, WidgetMessage{Role: "assistant", Text: text, At: e.CreatedAt})
			}
		case models.EventTypeToolResults,
			models.EventTypeMessageSent,
			models.EventTypeLLMRequest,
			models.EventTypeLLMResponse,
			models.EventTypeError,
			models.EventTypeSessionComplete,
			models.EventTypeDecisionResolved,
			models.EventTypePolicyEnforced,
			models.EventTypeDryRunCaptures:
			// Diagnostic / non-conversation events — tool calls are
			// surfaced separately via extractToolCallNames below.
		}
	}
	t.ToolCalls = extractToolCallNames(events)
	return t, nil
}

// extractAssistantText pulls the concatenated text from an
// assistant_turn event's content blocks.
func extractAssistantText(data []byte) string {
	var d struct {
		Content []anthropic.Content `json:"content"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return ""
	}
	var parts []string
	for _, c := range d.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, " ")
}

// extractToolCallNames walks the event stream and returns every
// tool_use block's name, ordered by event time. Used so widget
// transcripts can show what the agent reached for without flooding
// the message log.
func extractToolCallNames(events []models.SessionEvent) []WidgetToolCall {
	var out []WidgetToolCall
	for _, e := range events {
		if e.EventType != models.EventTypeAssistantTurn {
			continue
		}
		var d struct {
			Content []anthropic.Content `json:"content"`
		}
		if err := json.Unmarshal(e.Data, &d); err != nil {
			continue
		}
		for _, c := range d.Content {
			if c.Type == "tool_use" && c.Name != "" {
				out = append(out, WidgetToolCall{Name: c.Name, At: e.CreatedAt})
			}
		}
	}
	return out
}

// looksUnanswered scans the assistant's last reply for phrases that
// suggest the bot couldn't find an answer. A coarse heuristic — good
// enough to surface review candidates; a human reads the transcript
// to judge.
func looksUnanswered(lastAssistantData []byte) bool {
	if len(lastAssistantData) == 0 {
		return false
	}
	text := strings.ToLower(extractAssistantText(lastAssistantData))
	if text == "" {
		return false
	}
	phrases := []string{
		"i don't know",
		"i do not know",
		"i couldn't find",
		"i could not find",
		"i'm not able to",
		"i am not able to",
		"i don't see",
		"i do not see",
		"i don't have",
		"i do not have",
		"i'm unable to",
		"no information",
	}
	for _, p := range phrases {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// snippet returns up to `width` characters of `text` centred on the
// first case-insensitive occurrence of `query`. Used for showing
// search matches in context.
func snippet(text, query string, width int) string {
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	q := strings.ToLower(query)
	idx := strings.Index(lower, q)
	if idx < 0 {
		return truncate(text, width)
	}
	half := width / 2
	start := max(idx-half, 0)
	end := start + width
	if end > len(text) {
		end = len(text)
		start = max(end-width, 0)
	}
	out := text[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}

// truncate returns s capped at `max` runes with an ellipsis suffix
// when truncated.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
