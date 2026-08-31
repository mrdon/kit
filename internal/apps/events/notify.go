package events

import (
	"context"
	"crypto/sha1" //nolint:gosec // stable content digest, not security
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services/messenger"
)

// Shift notices.
//
// The calendar already carries the bartender's briefing, but reading it is an
// act someone has to remember to perform. A notice is the same information
// arriving unprompted on the morning it matters, to exactly the people who are
// working -- so the person opening at 11:30 finds out about the 7pm private
// booking before they set the room, not when thirty people walk in.
//
// Two rules shape what goes out, and both are the opposite of the website's:
//
//   - Cancelled events are excluded and everything else is included, private
//     bookings among them. IsPubliclyVisible is the gate for what leaves the
//     building; this is the internal surface, and a private booking is
//     precisely the thing a bartender must not be surprised by.
//   - Delivery is per person, not per event. One DM lists the whole day, so
//     someone working a five-event Saturday gets one message rather than five.

// NoticeSummary counts what one run did. Unmapped is the number of people
// working who have no Kit user paired with them -- the one failure an admin
// has to act on, so it is counted rather than merely logged.
type NoticeSummary struct {
	Sent     int
	Skipped  int
	Unmapped int
}

func (s NoticeSummary) String() string {
	return fmt.Sprintf("%d sent, %d already current, %d unmapped",
		s.Sent, s.Skipped, s.Unmapped)
}

// changed reports whether the run did anything worth recording.
func (s NoticeSummary) changed() bool { return s.Sent > 0 || s.Unmapped > 0 }

// RunShiftNotices sends today's notices for one tenant and records the outcome.
// triggeredBy is "schedule" or "manual".
func (a *App) RunShiftNotices(ctx context.Context, tenantID uuid.UUID, triggeredBy string) (NoticeSummary, error) {
	started := timeNow()
	sum, err := a.notifyShiftStaff(ctx, tenantID)
	if err != nil {
		a.auditNoticeFailed(ctx, tenantID, triggeredBy, err, time.Since(started))
		return sum, err
	}
	if triggeredBy == "manual" || sum.changed() {
		a.auditNoticeCompleted(ctx, tenantID, triggeredBy, sum, time.Since(started))
	}
	return sum, nil
}

// PreviewShiftNotices builds today's notices without sending them, so an admin
// can see who would hear what before turning the schedule on.
func (a *App) PreviewShiftNotices(ctx context.Context, tenantID uuid.UUID) ([]NoticePlan, error) {
	plans, _, err := a.planShiftNotices(ctx, tenantID)
	return plans, err
}

// NoticePlan is one person's message for one day.
type NoticePlan struct {
	UserID      uuid.UUID `json:"-"`
	SlackUserID string    `json:"slack_user_id"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
}

// notifyShiftStaff builds and delivers the day's notices.
func (a *App) notifyShiftStaff(ctx context.Context, tenantID uuid.UUID) (NoticeSummary, error) {
	plans, sum, err := a.planShiftNotices(ctx, tenantID)
	if err != nil {
		return sum, err
	}
	if a.msg == nil {
		return sum, nil
	}

	settings, err := a.svc.Settings(ctx, tenantID)
	if err != nil {
		return sum, err
	}
	day := startOfToday(settings.Loc())

	for _, p := range plans {
		fresh, err := recordNotice(ctx, a, tenantID, p.UserID, day, hashBody(p.Body))
		if err != nil {
			return sum, err
		}
		if !fresh {
			sum.Skipped++
			continue
		}
		if _, err := a.msg.Send(ctx, messenger.SendRequest{
			TenantID:  tenantID,
			Channel:   "slack",
			Recipient: messenger.Recipient{SlackUserID: p.SlackUserID},
			UserID:    p.UserID,
			Body:      p.Body,
			Origin:    AppName,
			OriginRef: day.Format("2006-01-02"),
		}); err != nil {
			return sum, fmt.Errorf("sending shift notice to %s: %w", p.SlackUserID, err)
		}
		sum.Sent++
	}
	return sum, nil
}

// planShiftNotices works out who is working today, what is on today, and what
// each person should be told. Pure apart from its reads, so the preview and
// the real send cannot disagree about what would go out.
func (a *App) planShiftNotices(ctx context.Context, tenantID uuid.UUID) ([]NoticePlan, NoticeSummary, error) {
	var sum NoticeSummary

	settings, err := a.svc.Settings(ctx, tenantID)
	if err != nil {
		return nil, sum, err
	}
	loc := settings.Loc()
	day := startOfToday(loc)
	next := day.AddDate(0, 0, 1)

	todays, err := a.eventsOn(ctx, tenantID, day, next)
	if err != nil {
		return nil, sum, err
	}
	// Nothing on means nobody needs telling, whether or not they are mapped.
	// Returning here also keeps a quiet Tuesday from recording an "unmapped"
	// count every day, which would turn a standing config gap into daily audit
	// noise that nobody reads.
	if len(todays) == 0 {
		return nil, sum, nil
	}

	shifts, err := square.Instance().ListPublishedShifts(ctx, tenantID, day.UTC(), next.UTC())
	if err != nil {
		return nil, sum, fmt.Errorf("pulling today's shifts: %w", err)
	}
	mapped, err := mappedUserIDs(ctx, a, tenantID)
	if err != nil {
		return nil, sum, err
	}

	// Group shifts by person first: someone working a split day gets one
	// notice, and their earliest start is what decides how the day reads.
	byMember := map[string][]square.EnrichedShift{}
	for _, s := range shifts {
		if s.TeamMemberID == "" {
			continue // open shift, nobody to tell
		}
		byMember[s.TeamMemberID] = append(byMember[s.TeamMemberID], s)
	}

	memberIDs := make([]string, 0, len(byMember))
	for id := range byMember {
		memberIDs = append(memberIDs, id)
	}
	sort.Strings(memberIDs)

	plans := []NoticePlan{}
	for _, id := range memberIDs {
		userID, ok := mapped[id]
		if !ok {
			sum.Unmapped++
			continue
		}
		user, err := models.GetUserByID(ctx, a.pool, tenantID, userID)
		if err != nil {
			return nil, sum, fmt.Errorf("loading mapped user: %w", err)
		}
		if user == nil || user.SlackUserID == "" {
			sum.Unmapped++
			continue
		}
		plans = append(plans, NoticePlan{
			UserID:      userID,
			SlackUserID: user.SlackUserID,
			Name:        byMember[id][0].Member,
			Body:        buildNoticeBody(byMember[id], todays, settings, loc),
		})
	}
	return plans, sum, nil
}

// eventsOn returns the events with an occurrence inside [from, to), expanded.
//
// The expansion happens here rather than in SQL because a repeating event
// stores only its FIRST occurrence in starts_at -- a weekly quiz that began
// last year is still on tonight, and no date predicate on that column would
// find it. listEvents deliberately exempts recurring rows from its lower bound
// for the same reason, which leaves the narrowing to us.
func (a *App) eventsOn(ctx context.Context, tenantID uuid.UUID, from, to time.Time) ([]dayEvent, error) {
	all, err := a.svc.List(ctx, tenantID, ListFilter{To: &to, Limit: 500})
	if err != nil {
		return nil, err
	}
	out := []dayEvent{}
	for i := range all {
		e := all[i]
		// Draft events are still being worked out and cancelled ones are off;
		// neither is something to set the room for.
		if e.Status != StatusPublished {
			continue
		}
		for _, occ := range e.Occurrences(from, to) {
			out = append(out, dayEvent{Event: e, Start: occ.Start, End: occ.End})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// dayEvent pairs an event with the specific occurrence landing on the day.
// A weekly event's Event.StartsAt is its first ever occurrence, so the
// occurrence time has to travel alongside it or every notice would announce
// the wrong date.
type dayEvent struct {
	Event Event
	Start time.Time
	End   time.Time
}

// buildNoticeBody writes one person's message.
//
// It leads with their own hours, because that is the fact that tells them
// which of the day's events actually fall in their shift, then lists the day
// in order. The per-event detail reuses briefingLines -- the same operational
// block the calendar carries, so the DM and the calendar entry can never drift
// into telling two different stories.
func buildNoticeBody(shifts []square.EnrichedShift, events []dayEvent, settings Settings, loc *time.Location) string {
	var b strings.Builder

	day := startOfToday(loc)
	fmt.Fprintf(&b, "*On today, %s*\n", day.Format("Monday 2 January"))
	if clock := shiftHours(shifts, loc); clock != "" {
		fmt.Fprintf(&b, "You're on %s.\n", clock)
	}
	b.WriteString("\n")

	for i := range events {
		de := events[i]
		fmt.Fprintf(&b, "*%s* — %s\n", de.Event.Title, formatOccurrenceClock(de, loc))
		for _, line := range briefingLines(&de.Event) {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		if s := strings.TrimSpace(de.Event.Summary); s != "" {
			fmt.Fprintf(&b, "  %s\n", firstLine(s))
		}
		if n := strings.TrimSpace(de.Event.PrepNotes); n != "" {
			fmt.Fprintf(&b, "  Staff notes: %s\n", n)
		}
		if url := settings.CanonicalURL(de.Event.Slug); url != "" && de.Event.IsPubliclyVisible() {
			fmt.Fprintf(&b, "  %s\n", url)
		}
		if i < len(events)-1 {
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// shiftHours renders the person's own hours, merging a split day into one
// range per block rather than pretending it is continuous.
func shiftHours(shifts []square.EnrichedShift, loc *time.Location) string {
	parts := []string{}
	for _, s := range shifts {
		start, serr := time.Parse(time.RFC3339, s.StartAt)
		end, eerr := time.Parse(time.RFC3339, s.EndAt)
		if serr != nil || eerr != nil {
			continue
		}
		parts = append(parts, clockRange(start.In(loc), end.In(loc)))
	}
	sort.Strings(parts)
	return strings.Join(parts, " and ")
}

// formatOccurrenceClock renders when an event runs, in the tenant's zone.
func formatOccurrenceClock(de dayEvent, loc *time.Location) string {
	if de.Event.AllDay {
		return "all day"
	}
	return clockRange(de.Start.In(loc), de.End.In(loc))
}

func clockRange(start, end time.Time) string {
	return strings.ToLower(start.Format("3:04pm")) + "–" + strings.ToLower(end.Format("3:04pm"))
}

// startOfToday is midnight in the tenant's zone. "Today" has to be resolved in
// the venue's zone, not UTC: at 6pm Denver it is already tomorrow in UTC, and
// a notice about tomorrow's events is worse than none.
func startOfToday(loc *time.Location) time.Time {
	now := timeNow().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

// hashBody digests a message so an unchanged day is not re-sent.
func hashBody(body string) string {
	sum := sha1.Sum([]byte(body)) //nolint:gosec // not security
	return hex.EncodeToString(sum[:])
}
