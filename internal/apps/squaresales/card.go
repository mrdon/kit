package squaresales

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// cardTTL is how long a sales card lives if nobody acks it.
//
// Slightly over a day, so exactly one is in the stack at a time: today's
// arrives as yesterday's expires. Without an explicit TTL, notable and
// important briefings stay until acked, and a daily cadence would rebuild
// the unread pile the whole card is meant to replace.
const cardTTL = 40 * time.Hour

// CardSurface is the slice of the cards service this app needs. Injected
// rather than imported so the dependency graph stays one-way; cmd/kit
// adapts the concrete CardService, as the vault app does.
type CardSurface interface {
	CreateSystemBriefing(ctx context.Context, tenantID uuid.UUID, title, body, severity string, expiresAt *time.Time) error
}

// RunPostCard posts the sales card for the most recent unposted business
// day. It returns whether a card was actually created.
//
// There is no agent in this path by design. The previous LLM-driven recap
// produced nothing at all on 25% of days because the model ended its turn
// without calling a tool; this cannot, because there is no decision to get
// wrong -- the data either exists or it does not.
func (a *App) RunPostCard(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	if a.cards == nil {
		return false, nil // card surface not wired; nothing to do
	}

	// Yesterday: today is still in progress and would report a partial day
	// as a collapse.
	cutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	date, ok, err := nextUnpostedDate(ctx, a.pool, tenantID, cutoff)
	if err != nil || !ok {
		return false, err
	}

	// Mark handled first, whatever happens below. A day we decline to post
	// (closed, no baseline) must not be reconsidered every morning
	// forever, and a duplicate card is worse than a missing one.
	if err := markCardPosted(ctx, a.pool, tenantID, date); err != nil {
		return false, err
	}
	return a.PostCardFor(ctx, tenantID, date)
}

// PostCardFor builds and posts the card for one business date regardless of
// whether that date has already been posted.
//
// This is the manual path (squaresales_post_card_now): previewing a card
// before the 6am run, and re-posting a day after a threshold change. The
// scheduled path goes through RunPostCard, which claims the date first so
// it cannot double-post.
func (a *App) PostCardFor(ctx context.Context, tenantID uuid.UUID, date time.Time) (bool, error) {
	if a.cards == nil {
		return false, nil
	}
	summary, err := a.summaryFor(ctx, tenantID, date)
	if err != nil {
		return false, err
	}
	if summary.Status == StatusClosed || summary.Status == StatusNoData {
		slog.Info("squaresales: skipping card", "tenant_id", tenantID,
			"date", date.Format(time.DateOnly), "status", summary.Status)
		return false, nil
	}

	expires := time.Now().Add(cardTTL)
	severity := Severity(summary)
	if err := a.cards.CreateSystemBriefing(ctx, tenantID,
		CardTitle(summary), FormatDaySummary(summary), severity, &expires); err != nil {
		return false, fmt.Errorf("creating sales card: %w", err)
	}
	a.auditCardPosted(ctx, tenantID, date, severity, len(summary.Findings))

	// Stamp here too, not only on the scheduled path. Without this a
	// manual post shortly before the morning run leaves the date looking
	// unposted, and the scheduled run posts it a second time.
	// markCardPosted is a no-op once the date is already claimed, so the
	// scheduled path stamping first costs nothing.
	if err := markCardPosted(ctx, a.pool, tenantID, date); err != nil {
		return true, err
	}
	return true, nil
}

// PreviewCard renders the card for a date without posting it.
func (a *App) PreviewCard(ctx context.Context, tenantID uuid.UUID, date time.Time) (string, error) {
	summary, err := a.summaryFor(ctx, tenantID, date)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s  [%s]\n\n%s", CardTitle(summary), Severity(summary), FormatDaySummary(summary)), nil
}

// Yesterday is the most recent complete business day in UTC. Today is still
// in progress and would report a partial day as a collapse.
func Yesterday() time.Time {
	return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
}

// summaryFor loads a business day with its same-weekday history and
// analyses it.
func (a *App) summaryFor(ctx context.Context, tenantID uuid.UUID, date time.Time) (DaySummary, error) {
	day, err := getDaily(ctx, a.pool, tenantID, date)
	if err != nil {
		return DaySummary{}, err
	}
	if day == nil {
		return DaySummary{Status: StatusNoData, Day: DayRollup{Date: date}}, nil
	}

	dates := sameWeekdayDates(date, baselineWindow)
	history, err := listDailyDates(ctx, a.pool, tenantID, dates)
	if err != nil {
		return DaySummary{}, err
	}
	withTarget := append(dates, date) //nolint:gocritic // deliberate copy: buckets need the target day too
	hours, err := listHourlyDates(ctx, a.pool, tenantID, withTarget)
	if err != nil {
		return DaySummary{}, err
	}
	items, err := listItemsDates(ctx, a.pool, tenantID, withTarget)
	if err != nil {
		return DaySummary{}, err
	}
	return Analyze(*day, history, hours, items), nil
}

// sameWeekdayDates returns the n dates preceding date on the same weekday,
// oldest first. Comparing a Saturday to the Saturdays before it is the
// whole basis of the analysis: comparing it to yesterday would flag every
// Monday as a collapse.
func sameWeekdayDates(date time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	for i := n; i >= 1; i-- {
		out = append(out, date.AddDate(0, 0, -7*i))
	}
	return out
}
