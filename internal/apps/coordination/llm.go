package coordination

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/prompts"
)

//go:embed prompts/*.tmpl
var promptFS embed.FS

var promptSet = prompts.MustParse(promptFS, "prompts/*.tmpl")

const modelHaiku = "claude-haiku-4-5-20251001"

func mustRender(name string, data any) string {
	out, err := prompts.Render(promptSet, name, data)
	if err != nil {
		panic(fmt.Errorf("coordination: %w", err))
	}
	return out
}

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
	hasProposal := len(coord.Config.LatestProposal.ProposedTimes) > 0 || coord.Config.LatestProposal.Summary != ""
	user := mustRender("user_draft_message.tmpl", map[string]any{
		"Reason":          reason,
		"OrganizerName":   organizerName,
		"ParticipantName": participantName,
		"Title":           coord.Config.Title,
		"DurationMinutes": coord.Config.DurationMinutes,
		"SlotsBlob":       slotsBlob,
		"HasProposal":     hasProposal,
		"RoundCount":      coord.RoundCount,
		"ProposalSummary": coord.Config.LatestProposal.Summary,
		"ProposalTimes":   fmt.Sprintf("%v", coord.Config.LatestProposal.ProposedTimes),
		"Notes":           coord.Config.Notes,
		"NudgeCount":      p.NudgeCount,
	})

	resp, err := a.llm.CreateMessage(ctx, &anthropic.Request{
		Model:     modelHaiku,
		MaxTokens: 600,
		System:    []anthropic.SystemBlock{{Type: "text", Text: mustRender("system_draft_message.tmpl", nil)}},
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
		return fmt.Sprintf("The organizer is trying to set up %q. Are any of these times OK? %s", coord.Config.Title, slotsForBody(coord.Config.CandidateSlots, coord.Config.OrganizerTZ))
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
// message in light of the full conversation log. New shape for the
// iterative-negotiation flow: free-form availability text + intent.
type ParsedResponse struct {
	Intent             string                 `json:"intent"` // accept | refine | decline | vague | unrelated
	Availability       string                 `json:"availability,omitempty"`
	AcceptedTime       string                 `json:"accepted_time,omitempty"`
	CurrentConstraints map[string]SlotVerdict `json:"current_constraints,omitempty"` // legacy; will be removed
	Notes              string                 `json:"notes,omitempty"`
}

// parseMeetingReply is the LLM call that extracts the participant's
// current availability statement and intent from the conversation log.
func (a *CoordinationApp) parseMeetingReply(ctx context.Context, log []MessageLogEntry, slots []Slot, organizerTZ string) (*ParsedResponse, error) {
	if a.llm == nil {
		return nil, errors.New("LLM not configured")
	}
	logJSON, _ := json.Marshal(log)
	slotsBlob := slotsForPrompt(slots, organizerTZ)

	user := mustRender("user_parse_meeting_reply.tmpl", map[string]any{
		"SlotsBlob": slotsBlob,
		"LogJSON":   string(logJSON),
	})

	resp, err := a.llm.CreateMessage(ctx, &anthropic.Request{
		Model:     modelHaiku,
		MaxTokens: 800,
		System:    []anthropic.SystemBlock{{Type: "text", Text: mustRender("system_parse_meeting_reply.tmpl", nil)}},
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

// ProposeResponse is the output of the LLM solver, given all
// participants' current availability statements.
type ProposeResponse struct {
	Converged              bool     `json:"converged"`
	ChosenTime             string   `json:"chosen_time,omitempty"`
	ProposedTimes          []string `json:"proposed_times,omitempty"`
	Summary                string   `json:"summary,omitempty"`
	NeedsClarificationFrom []string `json:"needs_clarification_from,omitempty"`
	// NeedsOutreach lists participants whose stated availability does
	// not yet satisfy any proposed_time (and so need another DM). The
	// engine uses this to skip re-engaging participants whose answer
	// already covers a candidate time.
	NeedsOutreach []string `json:"needs_outreach,omitempty"`
}

// ParticipantState is the input to proposeRound: each active
// participant's name + availability + acceptance state.
type ParticipantState struct {
	Name         string `json:"name"`
	SlackID      string `json:"slack_id,omitempty"`
	Availability string `json:"availability"`
	AcceptedTime string `json:"accepted_time,omitempty"`
	Status       string `json:"status"`
}

// proposeRound asks the LLM to look at all participant availability
// statements and either find a converged time or propose 1-3 viable
// candidates.
func (a *CoordinationApp) proposeRound(ctx context.Context, coord *Coordination, parts []ParticipantState) (*ProposeResponse, error) {
	if a.llm == nil {
		return nil, errors.New("LLM not configured")
	}
	partsJSON, _ := json.Marshal(parts)
	user := mustRender("user_propose_times.tmpl", map[string]any{
		"Title":       coord.Config.Title,
		"OrganizerTZ": coord.Config.OrganizerTZ,
		"PartsJSON":   string(partsJSON),
	})

	resp, err := a.llm.CreateMessage(ctx, &anthropic.Request{
		Model:     modelHaiku,
		MaxTokens: 800,
		System:    []anthropic.SystemBlock{{Type: "text", Text: mustRender("system_propose_times.tmpl", nil)}},
		Messages: []anthropic.Message{
			{Role: "user", Content: []anthropic.Content{{Type: "text", Text: user}}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("proposeRound LLM: %w", err)
	}
	raw := strings.TrimSpace(resp.TextContent())
	raw = stripCodeFence(raw)

	var parsed ProposeResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshaling proposer output %q: %w", raw, err)
	}
	return &parsed, nil
}

func validIntent(s string) bool {
	switch s {
	case "accept", "refine", "decline", "vague", "unrelated":
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
