package events

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Service methods for promotion channels and the computed work list.

// promoWindowMonths is how far ahead the promotion list reaches.
//
// Wider than the website feed's two months on purpose: a chamber wanting six
// weeks' notice for an event ten weeks out has to see that item before the
// deadline passes, and a feed-sized window would surface it with days to
// spare. The list is filtered by due date afterwards, so a longer horizon
// costs visibility of nothing.
const promoWindowMonths = 6

// ListChannels returns every configured destination, admin-only because the
// submit URLs and modes are configuration rather than content.
func (s *Service) ListChannels(ctx context.Context, tenantID uuid.UUID) ([]Channel, error) {
	return listChannels(ctx, s.pool, tenantID)
}

// SaveChannel creates or updates a destination.
func (s *Service) SaveChannel(ctx context.Context, tenantID uuid.UUID, c Channel) (*Channel, error) {
	c.TenantID = tenantID
	normaliseChannel(&c)
	if c.Name == "" {
		return nil, invalid("a channel needs a name")
	}
	if c.Mode == ChannelSubscribed && c.FeedTier != "" && !ValidTier(c.FeedTier) {
		return nil, invalid("unknown feed tier %q", c.FeedTier)
	}
	if c.ID == uuid.Nil {
		return insertChannel(ctx, s.pool, &c)
	}
	return updateChannel(ctx, s.pool, &c)
}

func (s *Service) DeleteChannel(ctx context.Context, tenantID, id uuid.UUID) error {
	return deleteChannel(ctx, s.pool, tenantID, id)
}

// PromoList is the computed work list for a tenant.
//
// Nothing is materialised: this reads the upcoming events and the channel
// templates, derives every (event, channel, step) that applies, and joins the
// sparse state on top. That is what makes retiming a drip or flipping a
// channel to `subscribed` take effect on the next load with no backfill.
func (s *Service) PromoList(ctx context.Context, tenantID uuid.UUID) ([]PromoItem, error) {
	channels, err := listChannels(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, nil
	}

	now := timeNow()
	until := now.AddDate(0, promoWindowMonths, 0)
	events, err := listEvents(ctx, s.pool, tenantID, ListFilter{
		Status:           StatusPublished,
		Visibility:       VisibilityPublic,
		From:             &now,
		To:               &until,
		ExcludeCancelled: true,
		Limit:            500,
	})
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	state, err := loadPromoState(ctx, s.pool, tenantID, ids)
	if err != nil {
		return nil, err
	}
	return buildPromoList(events, channels, state, now), nil
}

// PromoSummaryFor is what the reminder card needs: a count, not a grid.
func (s *Service) PromoSummaryFor(ctx context.Context, tenantID uuid.UUID) (PromoSummary, error) {
	items, err := s.PromoList(ctx, tenantID)
	if err != nil {
		return PromoSummary{}, err
	}
	return summarisePromo(items), nil
}

// MarkPromo records what happened to one item.
//
// Passing PromoTodo DELETES the row rather than writing a state, because a
// to-do is the absence of one. That keeps "does this work still apply?" a
// question only the computed list answers -- a stored todo could outlive the
// template that produced it and become a task nobody can explain.
func (s *Service) MarkPromo(ctx context.Context, tenantID uuid.UUID, eventID, channelID uuid.UUID, stepKey string, status PromoState, url, note string, by *uuid.UUID) error {
	stepKey = strings.TrimSpace(stepKey)
	if stepKey == "" {
		return invalid("step is required")
	}
	if status == PromoTodo {
		return clearPromo(ctx, s.pool, tenantID, eventID, channelID, stepKey)
	}
	switch status {
	case PromoDone, PromoIgnored, PromoAutoDone, PromoAutoFailed:
		// Storable states.
	case PromoTodo, PromoExpired:
		// Neither is ever stored. A to-do is the ABSENCE of a row -- handled
		// above by deleting -- and expiry is computed from the date, so
		// writing either would create a second source of truth about whether
		// the work still applies.
		return invalid("%q is computed, never stored", status)
	default:
		return invalid("unknown promo status %q", status)
	}
	// Both must belong to this tenant. Checked rather than assumed: the IDs
	// arrive from a browser, and a cross-tenant write here would attach one
	// workspace's promotion record to another's event.
	if _, err := getEvent(ctx, s.pool, tenantID, eventID); err != nil {
		return err
	}
	if _, err := getChannel(ctx, s.pool, tenantID, channelID); err != nil {
		return err
	}
	return upsertPromo(ctx, s.pool, tenantID, eventID, channelID, stepKey, status, url, note, by)
}
