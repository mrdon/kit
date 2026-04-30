package calendar

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

func init() {
	apps.Register(&CalendarApp{})
}

// CalendarApp lets users configure public iCal URLs and query the events on them.
type CalendarApp struct {
	svc *CalendarService
}

// Init sets up the service after DB is available.
func (a *CalendarApp) Init(pool *pgxpool.Pool) {
	a.svc = &CalendarService{pool: pool}
}

func (a *CalendarApp) Name() string { return "calendar" }

func (a *CalendarApp) SystemPrompt() string {
	return mustRender("system_prompt.tmpl", nil)
}

func (a *CalendarApp) ToolMetas() []services.ToolMeta {
	return calendarTools
}

func (a *CalendarApp) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r := registerer.(*tools.Registry)
	registerCalendarAgentTools(r, isAdmin, a.svc)
}

func (a *CalendarApp) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildCalendarMCPTools(a.svc)
}

func (a *CalendarApp) RegisterRoutes(_ *http.ServeMux) {}

func (a *CalendarApp) CronJobs() []apps.CronJob {
	return []apps.CronJob{
		{
			Name:     "sync_calendars",
			Interval: 15 * time.Minute,
			Run: func(ctx context.Context, pool *pgxpool.Pool, _ *crypto.Encryptor) error {
				svc := &CalendarService{pool: pool}
				return svc.SyncAllCalendars(ctx)
			},
		},
	}
}

var calendarTools = []services.ToolMeta{
	{
		Name:        "configure_calendar",
		Description: "Add a public iCal (.ics) URL as a calendar source. Validates the URL by fetching it once before saving. Specify role_scopes to restrict access.",
		AdminOnly:   true,
		Schema: services.PropsReq(map[string]any{
			"name":        services.Field("string", "Short name for this calendar (e.g. 'shifts', 'festivals')"),
			"url":         services.Field("string", "Public iCal feed URL ending in .ics"),
			"timezone":    services.Field("string", "Optional IANA timezone (e.g. 'America/New_York'). Defaults to UTC."),
			"role_scopes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Role names that can access this calendar. Empty = all users in tenant."},
		}, "name", "url"),
	},
	{
		Name:        "delete_calendar",
		Description: "Remove a configured calendar source and all its events.",
		AdminOnly:   true,
		Schema: services.PropsReq(map[string]any{
			"calendar_id": services.Field("string", "Calendar UUID from list_calendars"),
		}, "calendar_id"),
	},
	{
		Name:        "list_calendars",
		Description: "List configured calendars you have access to, including last-sync status and event counts.",
		Schema:      services.Props(map[string]any{}),
	},
	{
		Name:        "get_calendar_events",
		Description: "Look up events on configured calendars. Defaults to today through the next 7 days. Supports keyword search and date filtering.",
		Schema: services.Props(map[string]any{
			"calendar_id": services.Field("string", "Optional calendar UUID. Omit to search all calendars you can see."),
			"after":       services.Field("string", "Only events at or after this date (YYYY-MM-DD). Defaults to today."),
			"before":      services.Field("string", "Only events at or before this date (YYYY-MM-DD). Defaults to 7 days from today."),
			"query":       services.Field("string", "Optional keyword to search summary, description, and location."),
			"limit":       map[string]any{"type": "integer", "description": "Max events to return (1-100). Defaults to 25."},
		}),
	},
}
