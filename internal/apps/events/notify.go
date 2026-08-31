package events

import (
	"context"
	"crypto/sha1" //nolint:gosec // stable content digest, not security
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// Shift notices.
//
// The calendar already carries the bartender's briefing, but reading it is an
// act someone has to remember to perform. A notice is the same information
// arriving unprompted on the morning it matters.
//
// It goes to a CHANNEL, not to each person's DMs. The information is the same
// either way; what a channel adds is somewhere to answer a question. "Do we
// have the second mic?" asked in a 1:1 gets answered for one person, and the
// next person to wonder asks again. The people working are @-mentioned so the
// notification is still targeted, and the per-event detail goes in a thread so
// the channel's top level stays one line a day.
//
// Two rules shape what goes out, and the first is the opposite of the
// website's:
//
//   - Cancelled and draft events are excluded and everything else is included,
//     private bookings among them. IsPubliclyVisible is the gate for what
//     leaves the building; this is the internal surface, and a private booking
//     is precisely the thing a bartender must not be surprised by.
//   - The roster names everyone on the schedule, mapped or not. Someone
//     without a Slack pairing is named in plain text rather than pinged, so an
//     unmapped new starter is visible rather than silently missing.

// NoticeSummary counts what one run did. Unmapped is the number of people
// working with no Slack pairing -- they are named in the post but not pinged,
// which is the one thing an admin can act on.
type NoticeSummary struct {
	Posted   bool
	Skipped  bool
	Mentions int
	Unmapped int
}

func (s NoticeSummary) String() string {
	switch {
	case s.Skipped:
		return "already posted and unchanged, nothing to say"
	case !s.Posted:
		return "nothing on today, nothing posted"
	}
	out := fmt.Sprintf("posted, %s mentioned", plural(s.Mentions, "person", "people"))
	if s.Unmapped > 0 {
		out += fmt.Sprintf(", %d working without a Slack pairing", s.Unmapped)
	}
	return out
}

// changed reports whether the run did anything worth recording.
func (s NoticeSummary) changed() bool { return s.Posted || s.Unmapped > 0 }

// RunShiftNotices posts today's notice for one tenant and records the outcome.
// triggeredBy is "schedule" or "manual".
func (a *App) RunShiftNotices(ctx context.Context, tenantID uuid.UUID, triggeredBy string) (NoticeSummary, error) {
	started := timeNow()
	sum, err := a.postShiftNotice(ctx, tenantID)
	if err != nil {
		a.auditNoticeFailed(ctx, tenantID, triggeredBy, err, time.Since(started))
		return sum, err
	}
	if triggeredBy == "manual" || sum.changed() {
		a.auditNoticeCompleted(ctx, tenantID, triggeredBy, sum, time.Since(started))
	}
	return sum, nil
}

// PreviewShiftNotices builds today's notice without posting it, so an admin
// can read the exact text before a channel full of people does.
func (a *App) PreviewShiftNotices(ctx context.Context, tenantID uuid.UUID) (*DayNotice, error) {
	notice, _, err := a.planShiftNotice(ctx, tenantID)
	return notice, err
}

// DayNotice is one day's post: a headline naming who is on and what is on,
// and the per-event detail that hangs off it in a thread.
type DayNotice struct {
	Headline string `json:"headline"`
	Detail   string `json:"detail"`
	Mentions int    `json:"mentions"`
	Unmapped int    `json:"unmapped"`
}

// postShiftNotice builds and delivers the day's notice.
func (a *App) postShiftNotice(ctx context.Context, tenantID uuid.UUID) (NoticeSummary, error) {
	notice, sum, err := a.planShiftNotice(ctx, tenantID)
	if err != nil || notice == nil {
		return sum, err
	}
	settings, err := a.svc.Settings(ctx, tenantID)
	if err != nil {
		return sum, err
	}
	if settings.NoticeChannelID == "" {
		return sum, nil // notices are off for this workspace
	}
	day := startOfToday(settings.Loc())

	fresh, err := recordNotice(ctx, a, tenantID, day, hashBody(notice.Headline+notice.Detail))
	if err != nil {
		return sum, err
	}
	if !fresh {
		sum.Skipped = true
		return sum, nil
	}

	client, err := a.slackClient(ctx, tenantID)
	if err != nil {
		return sum, err
	}
	ts, err := client.PostMessageReturningTS(ctx, settings.NoticeChannelID, "", notice.Headline)
	if err != nil {
		return sum, fmt.Errorf("posting shift notice to %s: %w", settings.NoticeChannelName, err)
	}
	if err := stampNoticeMessage(ctx, a, tenantID, day, ts); err != nil {
		return sum, err
	}
	// The detail is a thread reply so the channel keeps one line a day. A
	// failure here leaves the headline standing, which still names who is on
	// and what is on -- degraded, not lost.
	if notice.Detail != "" {
		if err := client.PostMessage(ctx, settings.NoticeChannelID, ts, notice.Detail); err != nil {
			return sum, fmt.Errorf("posting shift notice detail: %w", err)
		}
	}
	sum.Posted = true
	return sum, nil
}

// planShiftNotice works out who is working today, what is on, and what the
// channel should be told. Pure apart from its reads, so the preview and the
// real post cannot disagree.
func (a *App) planShiftNotice(ctx context.Context, tenantID uuid.UUID) (*DayNotice, NoticeSummary, error) {
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
	// Nothing on means nothing to post. A daily "nothing today" is exactly the
	// noise that trains a channel to ignore the bot.
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

	// Group shifts by person: someone working a split day is one name on the
	// roster with both blocks, not two entries.
	byMember := map[string][]square.EnrichedShift{}
	for _, s := range shifts {
		if s.TeamMemberID == "" {
			continue // open shift, nobody to name
		}
		byMember[s.TeamMemberID] = append(byMember[s.TeamMemberID], s)
	}

	roster, mentions, unmapped, err := a.rosterMentions(ctx, tenantID, byMember, mapped, loc)
	if err != nil {
		return nil, sum, err
	}
	sum.Mentions, sum.Unmapped = mentions, unmapped

	return &DayNotice{
		Headline: buildHeadline(day, roster, todays, loc),
		Detail:   buildDetail(todays, settings, loc),
		Mentions: mentions,
		Unmapped: unmapped,
	}, sum, nil
}

// rosterMentions renders the roster with mapped staff as Slack mentions and
// everyone else as plain names, and reports how many of each.
//
// Naming the unmapped rather than dropping them is deliberate: the roster is
// what the team reads to know who is on, and a missing name reads as "nobody
// is covering that" rather than "we have not finished the setup".
func (a *App) rosterMentions(
	ctx context.Context, tenantID uuid.UUID,
	byMember map[string][]square.EnrichedShift, mapped map[string]uuid.UUID,
	loc *time.Location,
) (roster string, mentions, unmapped int, err error) {
	type entry struct {
		label string
		start time.Time
	}
	var entries []entry
	for id, shifts := range byMember {
		if len(shifts) == 0 {
			continue
		}
		name := shifts[0].Member
		if userID, ok := mapped[id]; ok {
			user, uerr := models.GetUserByID(ctx, a.pool, tenantID, userID)
			if uerr != nil {
				return "", 0, 0, uerr
			}
			if user != nil && user.SlackUserID != "" {
				name = "<@" + user.SlackUserID + ">"
				mentions++
			} else {
				unmapped++
			}
		} else {
			unmapped++
		}
		e := entry{label: name}
		if hours := shiftHours(shifts, loc); hours != "" {
			e.label += " " + hours
		}
		if t, perr := time.Parse(time.RFC3339, shifts[0].StartAt); perr == nil {
			e.start = t
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].start.Before(entries[j].start) })

	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, e.label)
	}
	return strings.Join(parts, ", "), mentions, unmapped, nil
}

// buildHeadline is the channel's top-level line: the date, who is on, and the
// day's events in one scannable list. Everything else waits in the thread, so
// the channel reads as one line a day.
func buildHeadline(day time.Time, roster string, events []dayEvent, loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%s*", day.Format("Monday 2 January"))
	if roster != "" {
		fmt.Fprintf(&b, " — %s", roster)
	} else {
		b.WriteString(" — nobody on the published schedule")
	}
	b.WriteString("\n")

	titles := make([]string, 0, len(events))
	for i := range events {
		titles = append(titles, fmt.Sprintf("%s %s",
			events[i].Event.Title, formatOccurrenceClock(events[i], loc)))
	}
	b.WriteString(strings.Join(titles, " · "))
	if len(events) > 0 {
		b.WriteString("\nDetails in thread.")
	}
	return b.String()
}

// buildDetail is the thread reply: the operational block per event, reusing
// briefingLines so the notice and the calendar entry cannot drift into telling
// two different stories.
func buildDetail(events []dayEvent, settings Settings, loc *time.Location) string {
	var b strings.Builder
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

// slackClient builds a posting client for the tenant.
func (a *App) slackClient(ctx context.Context, tenantID uuid.UUID) (*kitslack.Client, error) {
	if a.enc == nil {
		return nil, errors.New("slack is not configured on this deployment")
	}
	tenant, err := models.GetTenantByID(ctx, a.pool, tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return nil, fmt.Errorf("tenant %s not found", tenantID)
	}
	botToken, err := a.enc.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting bot token: %w", err)
	}
	return kitslack.NewClient(botToken), nil
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
