package calendar

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

func registerCalendarAgentTools(r *tools.Registry, isAdmin bool, svc *CalendarService) {
	for _, meta := range calendarTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     calendarAgentHandler(meta.Name, svc),
		})
	}
}

func calendarAgentHandler(name string, svc *CalendarService) tools.HandlerFunc {
	switch name {
	case "configure_calendar":
		return handleConfigureCalendar(svc)
	case "delete_calendar":
		return handleDeleteCalendar(svc)
	case "list_calendars":
		return handleListCalendars(svc)
	case "get_calendar_events":
		return handleGetCalendarEvents(svc)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown calendar tool: %s", name)
		}
	}
}

func handleConfigureCalendar(svc *CalendarService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Name       string   `json:"name"`
			URL        string   `json:"url"`
			Timezone   string   `json:"timezone"`
			RoleScopes []string `json:"role_scopes"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		cal, err := svc.Configure(ec.Ctx, ec.Caller(), ConfigureOpts{
			Name:       inp.Name,
			URL:        inp.URL,
			Timezone:   inp.Timezone,
			RoleScopes: inp.RoleScopes,
		})
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return "Only admins can configure calendars.", nil
			}
			return "Error: " + err.Error(), nil
		}
		scope := "all users"
		if len(inp.RoleScopes) > 0 {
			scope = "roles: " + strings.Join(inp.RoleScopes, ", ")
		}
		return fmt.Sprintf("Calendar '%s' configured (id %s). Access: %s. Initial sync: %s.",
			cal.Name, cal.ID, scope, cal.LastSyncStatus), nil
	}
}

func handleDeleteCalendar(svc *CalendarService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			CalendarID string `json:"calendar_id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		id, err := uuid.Parse(inp.CalendarID)
		if err != nil {
			return "Invalid calendar_id.", nil
		}
		if err := svc.Delete(ec.Ctx, ec.Caller(), id); err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return "Only admins can delete calendars.", nil
			}
			if errors.Is(err, services.ErrNotFound) {
				return "Calendar not found.", nil
			}
			return "Error: " + err.Error(), nil
		}
		return "Calendar deleted.", nil
	}
}

func handleListCalendars(svc *CalendarService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		cals, err := svc.List(ec.Ctx, ec.Caller())
		if err != nil {
			return "", fmt.Errorf("listing calendars: %w", err)
		}
		return formatCalendarList(cals), nil
	}
}

func handleGetCalendarEvents(svc *CalendarService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			CalendarID string `json:"calendar_id"`
			After      string `json:"after"`
			Before     string `json:"before"`
			Query      string `json:"query"`
			Limit      int    `json:"limit"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		events, err := svc.GetEvents(ec.Ctx, ec.Caller(), GetEventsOpts{
			CalendarID: inp.CalendarID,
			After:      inp.After,
			Before:     inp.Before,
			Query:      inp.Query,
			Limit:      inp.Limit,
		})
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have access to that calendar.", nil
			}
			return "Error: " + err.Error(), nil
		}
		return formatEvents(events, ec.Caller().Location()), nil
	}
}

func formatCalendarList(cals []CalendarWithStats) string {
	if len(cals) == 0 {
		return "No calendars configured."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d calendar(s):\n", len(cals))
	for _, c := range cals {
		sync := "never synced"
		if c.LastSyncAt != nil {
			sync = fmt.Sprintf("synced %s (%s)", c.LastSyncAt.Format("2006-01-02 15:04 MST"), c.LastSyncStatus)
		}
		fmt.Fprintf(&b, "  • %s (id %s) — %d events, %s\n", c.Name, c.ID, c.EventCount, sync)
		if c.LastSyncError != nil && *c.LastSyncError != "" {
			fmt.Fprintf(&b, "      ⚠ last error: %s\n", *c.LastSyncError)
		}
	}
	return b.String()
}

func formatEvents(events []Event, loc *time.Location) string {
	if len(events) == 0 {
		return "No events found in that range."
	}
	if loc == nil {
		loc = time.UTC
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d event(s):\n\n", len(events))
	for _, e := range events {
		when := e.StartTime.In(loc).Format("Mon 2006-01-02 15:04 MST")
		if e.AllDay {
			// All-day events are stored at UTC midnight on the calendar date.
			// Don't shift them — just format the date.
			when = e.StartTime.UTC().Format("Mon 2006-01-02") + " (all day)"
		}
		fmt.Fprintf(&b, "• %s — %s\n", when, e.Summary)
		if e.Location != "" {
			fmt.Fprintf(&b, "    @ %s\n", e.Location)
		}
		if e.Description != "" {
			desc := e.Description
			if len(desc) > 200 {
				desc = desc[:200] + "…"
			}
			fmt.Fprintf(&b, "    %s\n", desc)
		}
	}
	return b.String()
}
