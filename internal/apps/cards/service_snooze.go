package cards

// Per-user snoozing and briefing acknowledgement. Snoozing hides a card
// from one user's feeds until a time (the one-month variant snaps to the
// next Monday 07:00 in the tenant timezone); acking moves a briefing to a
// terminal state, singly or in bulk.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// cardSnoozeHourLocal is the clock hour (in tenant timezone) a snoozed
// card reappears at. 7am puts the card back on the user's morning feed —
// cards are deliberative, so unlike tasks (3am) we want them to land
// during the workday, not pile up overnight.
const cardSnoozeHourLocal = 7

// SnoozeForUser hides a card from this user's swipe feed (and any future
// notification surface — Slack DMs, email digests) until `until`. Per-user
// state, not per-card: another user sharing the role-scope still sees it.
// The card itself stays pending and reachable via list_decisions / direct
// link / MCP; this only filters it out of the personal feeds.
func (s *CardService) SnoozeForUser(ctx context.Context, c *services.Caller, cardID uuid.UUID, until time.Time) error {
	card, err := s.Get(ctx, c, cardID)
	if err != nil {
		return err
	}
	if !canWrite(c, card) {
		return services.ErrForbidden
	}
	return upsertUserCardSnooze(ctx, s.pool, c.TenantID, cardID, c.UserID, until)
}

// SnoozeForUserOneMonth is the convenience the swipe-UI's "Sleep 1 month"
// action calls. Advances 30 calendar days from now and then snaps forward
// to the first Monday at or after that date, clock 07:00 local in the
// tenant timezone. Landing on a Monday keeps the snooze on a predictable
// weekly rhythm so the user isn't surprised mid-week.
func (s *CardService) SnoozeForUserOneMonth(ctx context.Context, c *services.Caller, cardID uuid.UUID) error {
	tz, err := s.tenantTimezone(ctx, c.TenantID)
	if err != nil {
		return err
	}
	until, err := cardSnoozeOneMonthAt(time.Now(), tz)
	if err != nil {
		return err
	}
	return s.SnoozeForUser(ctx, c, cardID, until)
}

func (s *CardService) tenantTimezone(ctx context.Context, tenantID uuid.UUID) (string, error) {
	tenant, err := models.GetTenantByID(ctx, s.pool, tenantID)
	if err != nil {
		return "", fmt.Errorf("looking up tenant: %w", err)
	}
	if tenant == nil || tenant.Timezone == "" {
		return "UTC", nil
	}
	return tenant.Timezone, nil
}

// cardSnoozeOneMonthAt returns the snoozed_until timestamp for the "Sleep
// 1 month" action: advance 30 calendar days from `now`, then snap forward
// to the first Monday at or after that date, clock set to
// cardSnoozeHourLocal (07:00) in tz, converted to UTC. Pure function so
// tests can pin wall time.
func cardSnoozeOneMonthAt(now time.Time, tz string) (time.Time, error) {
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("loading timezone %q: %w", tz, err)
	}
	local := now.In(loc)
	plus30 := time.Date(local.Year(), local.Month(), local.Day()+30,
		cardSnoozeHourLocal, 0, 0, 0, loc)
	// Snap to the first Monday at or after plus30. If plus30 already lands
	// on a Monday, keep it (offset 0).
	offset := (int(time.Monday) - int(plus30.Weekday()) + 7) % 7
	return plus30.AddDate(0, 0, offset).UTC(), nil
}

// AckBriefing transitions a briefing card to a terminal state. Caller must
// have write access.
func (s *CardService) AckBriefing(ctx context.Context, c *services.Caller, cardID uuid.UUID, kind BriefingAckKind) (*Card, error) {
	if !kind.Valid() {
		return nil, fmt.Errorf("invalid ack kind %q", kind)
	}
	card, err := s.Get(ctx, c, cardID)
	if err != nil {
		return nil, err
	}
	if card.Kind != CardKindBriefing {
		return nil, fmt.Errorf("card %s is not a briefing", cardID)
	}
	if !canWrite(c, card) {
		return nil, services.ErrForbidden
	}
	out, err := ackBriefing(ctx, s.pool, c.TenantID, cardID, c.UserID, kind)
	if err != nil {
		if errors.Is(err, ErrAlreadyTerminal) {
			return nil, ErrAlreadyTerminal
		}
		return nil, err
	}
	return out, nil
}

// AckManyBriefings acknowledges several briefings with the same kind and
// returns the ones that took alongside per-card failures.
//
// Same partial-success shape as UpdateMany, and for the same reason: acks
// are per-card and per-caller, so one already-acked or invisible card
// shouldn't sink the rest of the batch.
func (s *CardService) AckManyBriefings(ctx context.Context, c *services.Caller, cardIDs []uuid.UUID, kind BriefingAckKind) ([]*Card, []BulkCardFailure, error) {
	if !kind.Valid() {
		return nil, nil, fmt.Errorf("invalid ack kind %q", kind)
	}
	if len(cardIDs) == 0 {
		return nil, nil, errors.New("no card ids supplied")
	}
	if len(cardIDs) > MaxBulkCards {
		return nil, nil, fmt.Errorf("too many cards: %d (max %d)", len(cardIDs), MaxBulkCards)
	}
	var acked []*Card
	var failures []BulkCardFailure
	seen := make(map[uuid.UUID]bool, len(cardIDs))
	for _, id := range cardIDs {
		// A repeated id would otherwise be acked twice and double-counted.
		if seen[id] {
			continue
		}
		seen[id] = true
		card, err := s.AckBriefing(ctx, c, id, kind)
		if err != nil {
			failures = append(failures, BulkCardFailure{CardID: id, Err: err})
			continue
		}
		acked = append(acked, card)
	}
	return acked, failures, nil
}
