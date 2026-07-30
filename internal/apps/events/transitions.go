package events

import (
	"context"

	"github.com/google/uuid"
)

// Lifecycle transitions.
//
// The rule that shapes this file: an event row is never hard-deleted. Cancel
// is the delete. A cancelled row keeps its gcal_* columns, which are the only
// record of which Google event still needs removing -- drop the row and the
// 15-minute sync goes blind, leaving an orphan on a calendar staff are reading.
// Reconcile would eventually catch it, but only because it re-queries Google;
// there is no reason to depend on that.

// Result carries an event plus any non-blocking advice for the caller to
// surface. Warnings are deliberately not errors: refusing to publish because a
// price has no link would be worse than mentioning it.
type Result struct {
	Event    *Event
	Warnings []string
}

// Publish marks an event confirmed.
//
// Publishing does NOT make an event public -- visibility is a separate axis. A
// private booking is published the moment it is on the books; it belongs on
// the team calendar and must still never reach the website.
func (s *Service) Publish(ctx context.Context, tenantID, id uuid.UUID) (*Result, error) {
	e, err := getEvent(ctx, s.pool, tenantID, id)
	if err != nil {
		return nil, err
	}
	if e.Status == StatusPublished {
		return &Result{Event: e}, nil
	}
	e.Status = StatusPublished
	if err := validateEvent(e); err != nil {
		return nil, err
	}
	saved, err := updateEvent(ctx, s.pool, e)
	if err != nil {
		return nil, err
	}
	settings, err := getSettings(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	return &Result{Event: saved, Warnings: publishWarnings(saved, settings)}, nil
}

// Unpublish returns a published event to draft, pulling it off the calendar
// and the feed while it is reworked.
func (s *Service) Unpublish(ctx context.Context, tenantID, id uuid.UUID) (*Event, error) {
	return s.setStatus(ctx, tenantID, id, StatusDraft)
}

// Cancel marks an event called off. The row survives as a tombstone so the
// next sync knows to remove the Google copy; see the file comment.
func (s *Service) Cancel(ctx context.Context, tenantID, id uuid.UUID) (*Event, error) {
	return s.setStatus(ctx, tenantID, id, StatusCancelled)
}

// Reopen restores a cancelled event to draft. The slug is still held by this
// row, so the original URL is reused rather than gaining a "-2" suffix.
func (s *Service) Reopen(ctx context.Context, tenantID, id uuid.UUID) (*Event, error) {
	e, err := getEvent(ctx, s.pool, tenantID, id)
	if err != nil {
		return nil, err
	}
	if e.Status != StatusCancelled {
		return nil, invalid("only a cancelled event can be reopened")
	}
	e.Status = StatusDraft
	return updateEvent(ctx, s.pool, e)
}

func (s *Service) setStatus(ctx context.Context, tenantID, id uuid.UUID, status Status) (*Event, error) {
	e, err := getEvent(ctx, s.pool, tenantID, id)
	if err != nil {
		return nil, err
	}
	if e.Status == status {
		return e, nil
	}
	e.Status = status
	if err := validateEvent(e); err != nil {
		return nil, err
	}
	return updateEvent(ctx, s.pool, e)
}
