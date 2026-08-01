package events

import (
	"context"
	"log/slog"
)

// Immediate calendar push.
//
// Publishing an event IS the intent to put it on the calendar, so waiting up
// to fifteen minutes for the cron is the wrong default: the person who just
// clicked Publish looks at the calendar, sees nothing, and concludes it is
// broken. That gap is also the only reason anyone would want a per-event
// "sync this one" button, so closing it removes the need for the button.
//
// The hook hangs off the Service rather than the HTTP handlers on purpose.
// Console, agent tools and MCP all call the same service methods; wiring the
// push into one handler would leave the other two surfaces silently on the
// fifteen-minute path.
//
// Failure is never fatal. The row is already saved and correct, the cron
// re-runs every fifteen minutes, and reconcile sweeps every twelve hours --
// so a Google outage delays the calendar copy rather than losing the event or
// blocking the edit.

// syncHook pushes one event's calendar copy. Nil until the app wires it (and
// in tests), in which case every change simply waits for the cron.
type syncHook func(ctx context.Context, e *Event) error

// pushCalendar runs the hook if there is one, swallowing the error into a
// user-facing warning. Callers that have nowhere to put a warning ignore the
// return; the failure is logged either way.
func (s *Service) pushCalendar(ctx context.Context, e *Event) string {
	if s.push == nil || e == nil {
		return ""
	}
	if err := s.push(ctx, e); err != nil {
		slog.Warn("events: immediate calendar push failed",
			"tenant_id", e.TenantID, "event_id", e.ID, "error", err)
		return "Saved, but the calendar could not be updated just now — the next sync will pick it up within 15 minutes."
	}
	return ""
}

// installSyncHook gives the service a way to reach the calendar. Called once
// from Init, after both the pool and the service exist.
func (a *App) installSyncHook() {
	if a.svc == nil {
		return
	}
	a.svc.push = func(ctx context.Context, e *Event) error {
		writer, settings, err := a.writerFor(ctx, e.TenantID)
		if err != nil {
			return err
		}
		_, err = a.syncOne(ctx, writer, settings, e)
		return err
	}
}
