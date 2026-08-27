package events

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Cloning: the answer to "same night again, next month".
//
// Most events at a venue are variations on one that already happened. Before
// this, repeating one meant retyping the blurb, the prep notes, the capacity
// and the price, and re-uploading the poster -- which is how two events that
// should be identical end up subtly different, usually in the staff brief
// nobody re-reads.
//
// A clone is deliberately NOT a link. The two rows share no state after the
// copy: editing the original never touches the copy, and the copy gets its own
// slug, its own calendar entry and its own audit trail. A linked series is what
// the repeat rule and the date list are for; this is for "like that, but".

// CloneParams tunes a copy. Every field is optional -- the zero value produces
// a faithful duplicate as a draft.
type CloneParams struct {
	// Title defaults to the original's with a "(copy)" suffix, which also keeps
	// the derived slug distinct.
	Title string
	// StartsAt moves the copy. Supplying it also DROPS the original's extra
	// dates: a clone aimed at a new date is "that event, on this day", and
	// carrying a previous series' leftover dates onto it is never what was
	// meant. Leave it empty to duplicate the schedule exactly.
	StartsAt  string
	CreatedBy *uuid.UUID
}

// Clone copies an event into a new draft.
//
// Status is forced to draft regardless of the original's, and the calendar sync
// columns are left zeroed. Both matter: a copy that inherited 'published' would
// hit the team calendar -- and, if the original was public, the website -- the
// instant it was created, before anyone had corrected the date. Zeroed gcal_*
// state means the sync treats it as a new entry rather than fighting the
// original over one calendar id.
func (s *Service) Clone(ctx context.Context, tenantID, id uuid.UUID, p CloneParams) (*Event, error) {
	src, err := getEvent(ctx, s.pool, tenantID, id)
	if err != nil {
		return nil, err
	}

	dup := *src
	dup.ID = uuid.Nil
	dup.Status = StatusDraft
	dup.GCalEventID = ""
	dup.GCalCalendarID = ""
	dup.GCalContentHash = ""
	dup.CreatedAt = time.Time{}
	dup.UpdatedAt = time.Time{}
	dup.CreatedBy = p.CreatedBy

	// The slice is shared with src by the struct copy; a fresh one keeps a
	// later edit to either row from mutating the other's dates in place.
	dup.RDates = append([]time.Time(nil), src.RDates...)

	if title := strings.TrimSpace(p.Title); title != "" {
		dup.Title = title
	} else {
		dup.Title = src.Title + " (copy)"
	}

	if raw := strings.TrimSpace(p.StartsAt); raw != "" {
		loc, err := ResolveTimezone(dup.Timezone)
		if err != nil {
			return nil, err
		}
		start, err := ParseTime(raw, loc)
		if err != nil {
			return nil, err
		}
		// Shift the end by the same delta so the copy keeps its length. EndsAt
		// is an absolute instant, so assigning only the start would silently
		// stretch or invert the event.
		if dup.EndsAt != nil {
			moved := dup.EndsAt.Add(start.Sub(dup.StartsAt))
			dup.EndsAt = &moved
		}
		dup.StartsAt = start
		dup.RDates = nil
	}

	if err := validateEvent(&dup); err != nil {
		return nil, err
	}
	// A fresh slug, never the original's: two rows cannot share a public URL,
	// and the original's may already be in a newsletter.
	if dup.Slug, err = UniqueSlug(ctx, s.pool, tenantID, dup.Title, nil); err != nil {
		return nil, err
	}

	created, err := insertEvent(ctx, s.pool, &dup)
	if err != nil {
		return nil, err
	}
	// Recorded as a creation rather than its own verb: what the website cares
	// about is that a new event exists, and it is still a draft either way.
	recordChange(ctx, s.pool, actionEventCreated, nil, created)
	return created, nil
}
