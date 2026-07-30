package events

import (
	"context"
	"errors"
	"sync"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
)

// fakeCalendar is an in-memory stand-in for Google Calendar. It exists because
// the sync's interesting behaviour -- what it deletes, what it leaves alone --
// is exactly what you cannot exercise against a live account.
//
// It records an operation log as well as state, so a test can assert that a
// dry run issued no writes at all, not merely that state came out unchanged.
type fakeCalendar struct {
	mu sync.Mutex
	// calendars[calendarID][eventID] = event
	calendars map[string]map[string]googlecalendar.Event
	ops       []string

	// failDelete/failUpsert inject a one-shot error to exercise retry paths.
	failDelete error
	failUpsert error
}

func newFakeCalendar() *fakeCalendar {
	return &fakeCalendar{calendars: map[string]map[string]googlecalendar.Event{}}
}

func (f *fakeCalendar) UpsertEvent(_ context.Context, calendarID string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUpsert; err != nil {
		f.failUpsert = nil
		f.ops = append(f.ops, "upsert-failed:"+event.ID)
		return nil, err
	}
	if f.calendars[calendarID] == nil {
		f.calendars[calendarID] = map[string]googlecalendar.Event{}
	}
	f.calendars[calendarID][event.ID] = *event
	f.ops = append(f.ops, "upsert:"+calendarID+":"+event.ID)
	return event, nil
}

func (f *fakeCalendar) DeleteEvent(_ context.Context, calendarID, eventID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failDelete; err != nil {
		f.failDelete = nil
		f.ops = append(f.ops, "delete-failed:"+eventID)
		return err
	}
	delete(f.calendars[calendarID], eventID)
	f.ops = append(f.ops, "delete:"+calendarID+":"+eventID)
	return nil
}

// ListEventsByPrivateProperties mirrors the real client's AND semantics: an
// event matches only if it carries every requested property. The reconcile
// sweep's safety depends on this being a filter, not a full listing.
func (f *fakeCalendar) ListEventsByPrivateProperties(_ context.Context, calendarID string, props map[string]string) ([]googlecalendar.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(props) == 0 {
		return nil, errors.New("at least one property required")
	}
	f.ops = append(f.ops, "list:"+calendarID)

	var out []googlecalendar.Event
	for _, ev := range f.calendars[calendarID] {
		if matchesProps(ev, props) {
			out = append(out, ev)
		}
	}
	return out, nil
}

func matchesProps(ev googlecalendar.Event, props map[string]string) bool {
	if ev.ExtendedProperties == nil {
		return false
	}
	for k, v := range props {
		if ev.ExtendedProperties.Private[k] != v {
			return false
		}
	}
	return true
}

// put seeds an event directly, bypassing the sync — used to plant entries the
// app does not own.
func (f *fakeCalendar) put(calendarID string, ev googlecalendar.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calendars[calendarID] == nil {
		f.calendars[calendarID] = map[string]googlecalendar.Event{}
	}
	f.calendars[calendarID][ev.ID] = ev
}

func (f *fakeCalendar) get(calendarID, eventID string) (googlecalendar.Event, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev, ok := f.calendars[calendarID][eventID]
	return ev, ok
}

func (f *fakeCalendar) count(calendarID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calendars[calendarID])
}

func (f *fakeCalendar) writeOps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, op := range f.ops {
		if len(op) >= 4 && op[:4] == "list" {
			continue
		}
		out = append(out, op)
	}
	return out
}

func (f *fakeCalendar) resetOps() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = nil
}
