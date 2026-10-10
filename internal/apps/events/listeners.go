package events

import (
	"context"
	"sync"
)

// ChangeListener is told after an event row is updated, with the row as it
// was and as it is now. Another app registers one to react to edits: the
// posters app marks a poster out of date when the facts its copy used
// change. Listeners run synchronously on the updating request and must be
// quick; anything slow belongs behind a scheduled task.
type ChangeListener func(ctx context.Context, before, after *Event)

var (
	listenersMu sync.RWMutex
	listeners   []ChangeListener
)

// RegisterChangeListener adds a listener. Safe to call from an app's Init.
func RegisterChangeListener(l ChangeListener) {
	listenersMu.Lock()
	defer listenersMu.Unlock()
	listeners = append(listeners, l)
}

func notifyChange(ctx context.Context, before, after *Event) {
	listenersMu.RLock()
	ls := append([]ChangeListener(nil), listeners...)
	listenersMu.RUnlock()
	for _, l := range ls {
		l(ctx, before, after)
	}
}
