package coordination

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
)

//go:embed prompts/draft_message.txt
var draftMessagePrompt string

//go:embed prompts/parse_meeting_reply.txt
var parseReplyPrompt string

const modelHaiku = "claude-haiku-4-5-20251001"

// draftMessage produces an outbound message body for a coordination
// participant. reason ∈ {"initial","nudge","reengage_invalidated"}.
//
// Voice rules: identifies as Kit, names the organizer in third person,
// no thread mentions on initial outreach.
func (a *CoordinationApp) draftMessage(ctx context.Context, coord *Coordination, p *Participant, reason string) (string, error) {
	if a.llm == nil {
		// Allows tests / partial wiring to no-op gracefully.
		return defaultDraft(coord, p, reason), nil
	}

	organizer, err := models.GetUserByID(ctx, a.pool, coord.TenantID, coord.OrganizerID)
	if err != nil {
		return "", fmt.Errorf("loading organizer: %w", err)
	}
	organizerName := "the organizer"
	if organizer != nil && organizer.DisplayName != nil && *organizer.DisplayName != "" {
		organizerName = *organizer.DisplayName
	}

	// Resolve participant display name — falls back to "" if not a Kit
	// user (the prompt then tells the bot to use a generic greeting).
	participantName := ""
	if p.UserID != nil {
		if u, err := models.GetUserByID(ctx, a.pool, coord.TenantID, *p.UserID); err == nil && u != nil && u.DisplayName != nil {
			participantName = *u.DisplayName
		}
	}

	slotsBlob := slotsForPrompt(coord.Config.CandidateSlots, coord.Config.OrganizerTZ)
	user := fmt.Sprintf(`
Reason: %s
Organizer name: %s
Participant name: %s
Meeting title: %s
Duration: %d minutes
Candidate slots:
%s
Notes from organizer: %s
Nudge count for this participant: %d
`, reason, organizerName, participantName, coord.Config.Title, coord.Config.DurationMinutes,
		slotsBlob, coord.Config.Notes, p.NudgeCount)

	resp, err := a.llm.CreateMessage(ctx, &anthropic.Request{
		Model:     modelHaiku,
		MaxTokens: 600,
		System:    []anthropic.SystemBlock{{Type: "text", Text: draftMessagePrompt}},
		Messages: []anthropic.Message{
			{Role: "user", Content: []anthropic.Content{{Type: "text", Text: user}}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("draftMessage LLM: %w", err)
	}
	body := strings.TrimSpace(resp.TextContent())
	if body == "" {
		return defaultDraft(coord, p, reason), nil
	}
	return body, nil
}

// defaultDraft is a deterministic fallback when the LLM isn't wired
// (e.g. tests, partial bootstrap). Not intended to ship in production
// outbound — but harmless if it does.
func defaultDraft(coord *Coordination, p *Participant, reason string) string {
	switch reason {
	case "nudge":
		return fmt.Sprintf("Just checking in on %q — when's good for you?", coord.Config.Title)
	case "reengage_invalidated":
		return fmt.Sprintf("Sorry to come back to you on %q — others' availability shifted. Updated options coming.", coord.Config.Title)
	default:
		return fmt.Sprintf("Hi — I'm Kit, a scheduling assistant. Trying to set up %q. Are any of these times OK? %s", coord.Config.Title, slotsForBody(coord.Config.CandidateSlots, coord.Config.OrganizerTZ))
	}
}

// MessageLogEntry is one outbound or inbound message used to construct
// the parse-reply input. Built by buildMessageLog from session_events.
type MessageLogEntry struct {
	Direction string    `json:"direction"`
	Body      string    `json:"body"`
	At        time.Time `json:"at"`
}

// ParsedResponse is the LLM's understanding of the latest inbound
// message in light of the full conversation log.
type ParsedResponse struct {
	Intent             string                 `json:"intent"` // reply | ambiguous | unrelated | decline | out_of_window
	CurrentConstraints map[string]SlotVerdict `json:"current_constraints,omitempty"`
	Notes              string                 `json:"notes,omitempty"`
}

// parseMeetingReply is the LLM call that classifies the participant's
// latest message and extracts their current constraint set, given the
// full message log + candidate slot list.
func (a *CoordinationApp) parseMeetingReply(ctx context.Context, log []MessageLogEntry, slots []Slot, organizerTZ string) (*ParsedResponse, error) {
	if a.llm == nil {
		return nil, errors.New("LLM not configured")
	}
	logJSON, _ := json.Marshal(log)
	slotsBlob := slotsForPrompt(slots, organizerTZ)

	user := fmt.Sprintf(`
Candidate slots (with their stable keys):
%s

Conversation log (chronological):
%s

Output JSON only, in this shape:
{
  "intent": "reply" | "ambiguous" | "unrelated" | "decline" | "out_of_window",
  "current_constraints": {"<slot_key>": "accept" | "reject" | "unspecified", ...},
  "notes": "free-form text"
}

The current_constraints map should reflect the participant's CURRENT view
across all of their messages — if they corrected themselves, only the
latest stance matters. Use "unspecified" for slots they haven't addressed.
`, slotsBlob, string(logJSON))

	resp, err := a.llm.CreateMessage(ctx, &anthropic.Request{
		Model:     modelHaiku,
		MaxTokens: 800,
		System:    []anthropic.SystemBlock{{Type: "text", Text: parseReplyPrompt}},
		Messages: []anthropic.Message{
			{Role: "user", Content: []anthropic.Content{{Type: "text", Text: user}}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("parseMeetingReply LLM: %w", err)
	}

	raw := strings.TrimSpace(resp.TextContent())
	raw = stripCodeFence(raw)

	var parsed ParsedResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshaling parser output %q: %w", raw, err)
	}
	if !validIntent(parsed.Intent) {
		return nil, fmt.Errorf("invalid intent %q", parsed.Intent)
	}
	if parsed.CurrentConstraints == nil {
		parsed.CurrentConstraints = map[string]SlotVerdict{}
	}
	return &parsed, nil
}

func validIntent(s string) bool {
	switch s {
	case "reply", "ambiguous", "unrelated", "decline", "out_of_window":
		return true
	}
	return false
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// strip leading ``` (possibly with json after) and trailing ```
		end := strings.LastIndex(s, "```")
		if end > 3 {
			s = s[3:end]
			if i := strings.Index(s, "\n"); i >= 0 {
				s = s[i+1:]
			}
		}
	}
	return strings.TrimSpace(s)
}

func slotsForPrompt(slots []Slot, tz string) string {
	loc, err := loadTZ(tz)
	if err != nil {
		loc = time.UTC
	}
	var b strings.Builder
	for _, s := range slots {
		fmt.Fprintf(&b, "  - key=%s | %s → %s\n",
			s.Key(),
			s.Start.In(loc).Format("Mon Jan 2, 3:04 PM"),
			s.End.In(loc).Format("3:04 PM"))
	}
	return b.String()
}

func slotsForBody(slots []Slot, tz string) string {
	loc, err := loadTZ(tz)
	if err != nil {
		loc = time.UTC
	}
	var b strings.Builder
	for i, s := range slots {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s.Start.In(loc).Format("Mon Jan 2 3:04 PM"))
	}
	return b.String()
}

func loadTZ(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(tz)
}
