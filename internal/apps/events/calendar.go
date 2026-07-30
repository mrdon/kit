package events

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
)

// calendarWriter is the slice of the Google Calendar client this app uses.
//
// Declared here, at the consumer, rather than exported from googlecalendar:
// *googlecalendar.Client satisfies it with no production changes, and it is
// what makes the sync testable at all. The shift sync takes the concrete
// client, which is precisely why it has no sync tests -- every path there
// needs a live Google account.
type calendarWriter interface {
	UpsertEvent(ctx context.Context, calendarID string, event *googlecalendar.Event) (*googlecalendar.Event, error)
	DeleteEvent(ctx context.Context, calendarID, eventID string) error
	ListEventsByPrivateProperties(ctx context.Context, calendarID string, props map[string]string) ([]googlecalendar.Event, error)
}

// writerFor resolves the tenant's calendar client and settings.
//
// resolveWriter is the seam that makes the sync and reconcile paths testable:
// production leaves it nil and gets the real client, tests inject a fake. Both
// entry points go through here rather than reaching for the client directly,
// which is the difference between this app having sync tests and the shift
// sync not having any.
func (a *App) writerFor(ctx context.Context, tenantID uuid.UUID) (calendarWriter, Settings, error) {
	if a.resolveWriter != nil {
		return a.resolveWriter(ctx, tenantID)
	}
	return a.loadWriter(ctx, tenantID)
}

// loadWriter builds the real client.
//
// It deliberately uses LoadClientOnly: the credential is shared with the shift
// sync, but the target calendar is not. This app's calendar comes from its own
// settings row, which is why repointing one feature never silently moves the
// other.
func (a *App) loadWriter(ctx context.Context, tenantID uuid.UUID) (calendarWriter, Settings, error) {
	if a.pool == nil {
		return nil, Settings{}, ErrNotConfigured
	}
	settings, err := getSettings(ctx, a.pool, tenantID)
	if err != nil {
		return nil, Settings{}, err
	}
	if !settings.CalendarConfigured() {
		return nil, settings, ErrNoCalendar
	}
	client, err := googlecalendar.Instance().LoadClientOnly(ctx, tenantID)
	if err != nil {
		return nil, settings, err
	}
	return client, settings, nil
}

// desiredCalendars returns the calendars this event should currently appear on.
//
// This is the ONLY place calendar routing is decided. It returns a slice
// though it always yields at most one today: a future public/ops split would
// change this function and the storage shape, not the sync and reconcile loops
// that call it.
//
// Note what does NOT appear here: visibility. Every settled event goes on the
// single shared calendar, private bookings included -- staff need them, and the
// food partner caters them. Visibility gates the public feed, nothing else.
func desiredCalendars(e *Event, s Settings) []string {
	if e == nil || e.Status != StatusPublished || !s.CalendarConfigured() {
		return nil
	}
	return []string{s.CalendarID}
}
