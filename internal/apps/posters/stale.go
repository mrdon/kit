package posters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/events"
)

// Out-of-date posters. When a poster is set on an event, the facts its
// copy used are stored with a hash. The events app tells us about every
// update; a changed hash flips stale. Update poster then asks Sonnet to
// change only the affected strings in the source and saves a new version
// for a person to compare and set.

func (a *App) installEventListener() {
	events.RegisterChangeListener(func(ctx context.Context, before, after *events.Event) {
		if after == nil || !apps.IsEnabled(ctx, after.TenantID, AppName) {
			return
		}
		poster, err := posterForEvent(ctx, a.pool, after.TenantID, after.ID)
		if err != nil || poster == nil || poster.SetVersionID == nil {
			return
		}
		stale := factsHash(factsFor(after)) != poster.FactsHash
		if stale == poster.Stale {
			return
		}
		if err := setStale(ctx, a.pool, after.TenantID, poster.ID, stale); err != nil {
			slog.Warn("posters: flagging stale poster", "error", err, "poster_id", poster.ID)
		}
	})
}

// UpdateFacts refreshes a stale poster's copy from the event. The new
// version sits next to the current one; nothing reaches the event until
// someone sets it.
func (a *App) UpdateFacts(ctx context.Context, tenantID uuid.UUID, posterID uuid.UUID, author string) (*EditResult, []string, error) {
	poster, cur, err := a.loadPoster(ctx, tenantID, posterID)
	if err != nil {
		return nil, nil, err
	}
	if cur == nil {
		return nil, nil, invalid("this poster has no version yet")
	}
	if poster.EventID == nil {
		return nil, nil, invalid("this poster is not attached to an event")
	}
	evs := a.eventsService()
	if evs == nil {
		return nil, nil, invalid("the events app is not available")
	}
	ev, err := evs.Get(ctx, tenantID, *poster.EventID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading event: %w", err)
	}
	var out struct {
		Source  string   `json:"source"`
		Changes []string `json:"changes"`
	}
	user := mustRender("user_update_facts.tmpl", map[string]any{
		"Event":    factsText(factsFor(ev)),
		"OldFacts": strOr(string(poster.Facts), "(none recorded)"),
		"Source":   cur.Source,
	})
	if err := askJSON(ctx, a.llm, mustRender("system_update_facts.tmpl", nil), user, nil, &out); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(out.Source) == "" {
		return nil, nil, invalid("the model returned no source")
	}
	instruction := "Updated facts from the event"
	if len(out.Changes) > 0 {
		instruction += ": " + strings.Join(out.Changes, "; ")
	}
	res, err := a.saveEdit(ctx, tenantID, poster, out.Source, instruction, author)
	if err != nil {
		return nil, nil, err
	}
	return res, out.Changes, nil
}
