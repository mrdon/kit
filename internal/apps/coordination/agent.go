package coordination

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

// coordinationTools is the shared metadata for agent + MCP surfaces.
var coordinationTools = []services.ToolMeta{
	{
		Name: "start_coordination",
		Description: `Start a multi-party meeting-time coordination. The bot DMs each
participant with the candidate slots, parses their replies, and surfaces
a decision card to you when a slot works for everyone.

You must pre-compute candidate_slots — typically 3 free 30-min windows
within the requested date range, drawn from the organizer's calendar
(if iCal configured) or from the windows the organizer typed in. Each
slot is {start, end} in RFC3339.`,
		Schema: services.PropsReq(map[string]any{
			"title":            services.Field("string", "What the meeting is about (e.g. 'Q2 review')"),
			"duration_minutes": services.Field("integer", "Meeting length in minutes"),
			"start_date":       services.Field("string", "Earliest acceptable date (YYYY-MM-DD)"),
			"end_date":         services.Field("string", "Latest acceptable date (YYYY-MM-DD)"),
			"candidate_slots":  services.Field("array", "Array of {start, end} ISO-8601 timestamps. 3 slots is typical."),
			"participants":     services.Field("array", "Array of Slack user IDs (e.g. ['U09...', 'U07...']). Use find_user first."),
			"notes":            services.Field("string", "Optional preferences like 'mornings preferred'"),
			"auto_approve":     services.Field("boolean", "If true, the bot sends outbound messages without per-message approval cards. Default false."),
			"deadline_days":    services.Field("integer", "Days until coordination auto-abandons. Default 7."),
			"organizer_tz":     services.Field("string", "IANA timezone of the organizer (e.g. 'America/New_York'). Defaults to UTC."),
		}, "title", "duration_minutes", "start_date", "end_date", "candidate_slots", "participants"),
	},
	{
		Name:        "list_coordinations",
		Description: "List your recent coordinations and their statuses.",
		Schema: services.Props(map[string]any{
			"limit": services.Field("integer", "How many to return (default 25)"),
		}),
	},
	{
		Name:        "get_coordination",
		Description: "Get full status of a coordination: who responded, current candidates, next nudges.",
		Schema: services.PropsReq(map[string]any{
			"coordination_id": services.Field("string", "The coordination UUID"),
		}, "coordination_id"),
	},
	{
		Name:        "cancel_coordination",
		Description: "Cancel an active coordination. Sends a brief 'cancelled' note to anyone already contacted.",
		Schema: services.PropsReq(map[string]any{
			"coordination_id": services.Field("string", "The coordination UUID"),
		}, "coordination_id"),
	},
}

func registerCoordinationAgentTools(r *tools.Registry, isAdmin bool, svc *Service) {
	for _, meta := range coordinationTools {
		// All coordination tools are PolicyAllow. The user explicitly
		// asked for the coordination, so creating one doesn't need
		// gating. The outbound DMs the engine sends to participants
		// are the identity-sensitive operation — those go through
		// per-message approval cards (handled in the engine via
		// coord.config.auto_approve).
		r.Register(tools.Def{
			Name:          meta.Name,
			Description:   meta.Description,
			Schema:        meta.Schema,
			DefaultPolicy: tools.PolicyAllow,
			Handler:       agentHandlerFor(meta.Name, svc),
		})
	}
	// Internal resolve tool — invoked by decision card option resolutions
	// only. Not in coordinationTools (no MCP exposure, no system prompt
	// mention). Lives at the bottom of the registry for the resolve path
	// to find by name.
	if svc != nil && svc.app != nil {
		registerResolveDecisionTool(r, svc.app)
	}
	_ = isAdmin
}

func agentHandlerFor(name string, svc *Service) tools.HandlerFunc {
	switch name {
	case "start_coordination":
		return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
			var inp struct {
				Title           string      `json:"title"`
				DurationMinutes int         `json:"duration_minutes"`
				StartDate       string      `json:"start_date"`
				EndDate         string      `json:"end_date"`
				CandidateSlots  []slotInput `json:"candidate_slots"`
				Participants    []string    `json:"participants"`
				Notes           string      `json:"notes"`
				AutoApprove     bool        `json:"auto_approve"`
				DeadlineDays    int         `json:"deadline_days"`
				OrganizerTZ     string      `json:"organizer_tz"`
			}
			if err := json.Unmarshal(raw, &inp); err != nil {
				return "", fmt.Errorf("parsing args: %w", err)
			}
			start, err := parseISODate(inp.StartDate)
			if err != nil {
				return "", fmt.Errorf("start_date: %w", err)
			}
			end, err := parseISODate(inp.EndDate)
			if err != nil {
				return "", fmt.Errorf("end_date: %w", err)
			}
			slots, err := convertSlots(inp.CandidateSlots)
			if err != nil {
				return "", err
			}
			coord, err := svc.Start(ec.Ctx, ec.Caller(), StartInput{
				Title:           inp.Title,
				DurationMinutes: inp.DurationMinutes,
				StartDate:       start,
				EndDate:         end,
				CandidateSlots:  slots,
				Participants:    inp.Participants,
				Notes:           inp.Notes,
				AutoApprove:     inp.AutoApprove,
				DeadlineDays:    inp.DeadlineDays,
				OrganizerTZ:     inp.OrganizerTZ,
			})
			if err != nil {
				return "", err
			}
			// Drive the engine immediately so the organizer sees the
			// first approval card (or first outbound, if auto-approved)
			// without waiting up to 60s for the next cron tick.
			if svc.app != nil && svc.app.engine != nil {
				_ = svc.app.engine.Tick(ec.Ctx)
			}
			gateMsg := "you'll get an approval card in your swipe stack with the drafted DMs before they go out"
			if inp.AutoApprove {
				gateMsg = "DMs are going out now (auto-approve was enabled at start)"
			}
			return fmt.Sprintf("Coordination started (id=%s, %d participants) — %s. Use get_coordination to check status.", coord.ID, len(inp.Participants), gateMsg), nil
		}

	case "list_coordinations":
		return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
			var inp struct {
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(raw, &inp)
			coords, err := svc.ListForCaller(ec.Ctx, ec.Caller(), inp.Limit)
			if err != nil {
				return "", err
			}
			if len(coords) == 0 {
				return "No coordinations.", nil
			}
			var b strings.Builder
			for _, c := range coords {
				fmt.Fprintf(&b, "- %s [%s] %q (created %s)\n",
					c.ID, c.Status, c.Config.Title, c.CreatedAt.Format(time.RFC3339))
			}
			return b.String(), nil
		}

	case "get_coordination":
		return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
			var inp struct {
				CoordinationID string `json:"coordination_id"`
			}
			if err := json.Unmarshal(raw, &inp); err != nil {
				return "", err
			}
			id, err := uuid.Parse(inp.CoordinationID)
			if err != nil {
				return "", fmt.Errorf("invalid coordination_id: %w", err)
			}
			st, err := svc.GetStatus(ec.Ctx, ec.Caller(), id)
			if err != nil {
				return "", err
			}
			return formatStatus(st), nil
		}

	case "cancel_coordination":
		return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
			var inp struct {
				CoordinationID string `json:"coordination_id"`
			}
			if err := json.Unmarshal(raw, &inp); err != nil {
				return "", err
			}
			id, err := uuid.Parse(inp.CoordinationID)
			if err != nil {
				return "", fmt.Errorf("invalid coordination_id: %w", err)
			}
			if err := svc.Cancel(ec.Ctx, ec.Caller(), id); err != nil {
				return "", err
			}
			return "Coordination cancelled. Contacted participants will be notified.", nil
		}
	}
	return nil
}

func formatStatus(st *Status) string {
	c := st.Coordination
	var b strings.Builder
	fmt.Fprintf(&b, "%q [%s]\n", c.Config.Title, c.Status)
	fmt.Fprintf(&b, "  Created: %s\n", c.CreatedAt.Format(time.RFC3339))
	if c.DeadlineAt != nil {
		fmt.Fprintf(&b, "  Deadline: %s\n", c.DeadlineAt.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "  Candidate slots: %d\n", len(c.Config.CandidateSlots))
	for _, s := range c.Config.CandidateSlots {
		fmt.Fprintf(&b, "    - %s → %s\n", s.Start.Format("Mon Jan 2 15:04 MST"), s.End.Format("15:04 MST"))
	}
	fmt.Fprintf(&b, "  Participants:\n")
	for _, p := range st.Participants {
		fmt.Fprintf(&b, "    - %s [%s] (rounds=%d, nudges=%d)\n",
			p.Identifier, p.Status, len(p.Rounds), p.NudgeCount)
	}
	return b.String()
}

type slotInput struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func convertSlots(in []slotInput) ([]Slot, error) {
	out := make([]Slot, 0, len(in))
	for i, s := range in {
		start, err := parseFlexibleTimestamp(s.Start)
		if err != nil {
			return nil, fmt.Errorf("slot %d start: %w", i, err)
		}
		end, err := parseFlexibleTimestamp(s.End)
		if err != nil {
			return nil, fmt.Errorf("slot %d end: %w", i, err)
		}
		out = append(out, Slot{Start: start, End: end})
	}
	return out, nil
}

// parseFlexibleTimestamp accepts RFC3339 with Z/offset, plain
// "YYYY-MM-DDTHH:MM:SS" (assumed UTC), and "YYYY-MM-DD HH:MM:SS"
// variants. The agent doesn't always produce a timezone in its slot
// output; the engine treats missing-tz as UTC.
func parseFlexibleTimestamp(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("empty timestamp")
	}
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp format %q", s)
}

func parseISODate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("empty date")
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}
