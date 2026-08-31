package events

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// The table topper: the card that stands on each taproom table telling
// customers what is on this week.
//
// It is derived from the rows the website and the calendar are already derived
// from, which is the entire point. The events are entered once; the weekly
// print run should not be a second act of authorship in a design tool, with
// its own copy of the truth to keep in step.
//
// The visibility rule is the feed's, and it matters more here than anywhere:
// this sheet is handed to customers on the table. Only published, public
// events reach it, decided by the same single predicate (IsPubliclyVisible)
// every public surface asks.

// topperMaxBandBullets is what fits under a title once a band has been sized
// for a seven-day week. Support acts and the headliner's own detail share it.
const topperMaxBandBullets = 4

// topperMaxSupports bounds how many other events one day lists before it stops
// naming them individually and says "+N more" instead.
const topperMaxSupports = 3

// topperMaxBullets is what fits under a title at a readable size. Anything
// longer is a description, and the website is where descriptions live.
const topperMaxBullets = 3

// topperBulletChars truncates a bullet that was written for the web. Long
// enough for a real sentence, short enough that it cannot wrap past two lines.
const topperBulletChars = 78

// TopperRow is one band: a day, what is on, and a few words about it.
type TopperRow struct {
	Day     string // "MON"
	Time    string // "6:30 PM"; empty for an all-day event
	Title   string
	Bullets []string
	// Poster is the event's own artwork, already decoded to something the PDF
	// writer can embed. Nil when the event has none -- the band then simply
	// runs full width rather than showing a placeholder.
	Poster   *posterImage
	at       time.Time
	posterID *uuid.UUID
}

// Topper is everything one printed panel shows.
type Topper struct {
	Heading   string
	DateRange string
	Rows      []TopperRow
	Site      string
	Logo      []byte
	WeekStart time.Time
}

// posterImage is an event poster in a format fpdf can embed. Kind is fpdf's
// own image-type string ("JPG", "PNG", "GIF"), not the source MIME.
type posterImage struct {
	Data []byte
	Kind string
}

// buildTopper assembles the panel for the week containing day.
//
// tenant supplies the branding the events rows cannot: the name in the
// heading and the workspace icon in the footer.
func (a *App) buildTopper(ctx context.Context, tenant *models.Tenant, day time.Time) (Topper, error) {
	settings, err := getSettings(ctx, a.pool, tenant.ID)
	if err != nil {
		return Topper{}, err
	}
	loc := settings.Loc()
	start := weekStart(day.In(loc))
	end := start.AddDate(0, 0, 7)

	events, err := listEvents(ctx, a.pool, tenant.ID, ListFilter{
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		From:       &start,
		To:         &end,
		Limit:      200,
	})
	if err != nil {
		return Topper{}, err
	}

	rows := topperRows(events, start, end, loc)
	out := Topper{
		Heading:   "This week at " + tenant.Name,
		DateRange: topperRange(start, loc),
		Site:      siteHost(settings.PublicURLTemplate),
		Logo:      tenant.Icon192,
		WeekStart: start,
	}
	out.Rows = a.attachPosters(ctx, tenant.ID, rows)
	return out, nil
}

// topperOccurrence is one event landing on one day, before the day's bands are
// decided. Kept separate from TopperRow because several of these collapse into
// a single row.
type topperOccurrence struct {
	at         time.Time
	title      string
	bullets    []string
	timeLabel  string
	prominence Prominence
	posterID   *uuid.UUID
}

// topperRows builds one band PER DAY, not per occurrence.
//
// A day with two events used to print two bands with the same day label
// stacked on top of each other, which reads as a mistake rather than as a busy
// Saturday. Grouping also bounds the card at seven bands whatever the week
// does, so the bands stay tall enough to read from across a table instead of
// being squeezed by an unusually full week.
//
// Expansion still happens first, and it is what makes a weekly series work:
// trivia's starts_at is the week it began, so the band has to say the date it
// happens THIS week -- and a fortnightly event correctly produces no band at
// all on its off week.
func topperRows(events []Event, start, end time.Time, loc *time.Location) []TopperRow {
	byDay := map[int][]topperOccurrence{}
	for i := range events {
		e := &events[i]
		// Belt and braces, as in the feed: this is the predicate that decides
		// what a customer sees, so it is asked here too rather than trusting
		// the WHERE clause to stay correct through future edits.
		if !e.IsPubliclyVisible() {
			continue
		}
		for _, occ := range e.Occurrences(start, end) {
			at := occ.Start.In(loc)
			o := topperOccurrence{
				at:         at,
				title:      strings.TrimSpace(e.Title),
				bullets:    topperBullets(e),
				prominence: e.Prominence,
				posterID:   e.HeroAttachmentID,
			}
			if !e.AllDay {
				o.timeLabel = topperTime(at)
			}
			day := int(at.Sub(start) / (24 * time.Hour))
			byDay[day] = append(byDay[day], o)
		}
	}

	days := slices.Sorted(maps.Keys(byDay))
	rows := make([]TopperRow, 0, len(days))
	for _, day := range days {
		rows = append(rows, topperDayRow(byDay[day]))
	}
	return rows
}

// topperDayRow turns everything on one day into a single band: a headliner,
// and the rest folded into its bullets.
func topperDayRow(occs []topperOccurrence) TopperRow {
	slices.SortStableFunc(occs, compareBilling)
	head := occs[0]

	supports := make([]string, 0, len(occs)-1)
	for _, o := range occs[1:] {
		supports = append(supports, supportBullet(o))
	}
	return TopperRow{
		Day:      strings.ToUpper(head.at.Format("Mon")),
		Time:     head.timeLabel,
		Title:    head.title,
		Bullets:  bandBullets(head.bullets, supports),
		posterID: head.posterID,
		at:       head.at,
	}
}

// bandBullets shares one band's bullet budget between the headliner's own
// detail and the other events on the day.
//
// The support acts win. Knowing there is a second thing on tonight changes
// whether someone comes in; a third bullet about the first thing does not. So
// the headliner's list is trimmed to make room rather than the other way
// round -- but never below one line, because a band with a title and no words
// under it looks unfinished.
//
// Reading order still puts the headliner's own bullets first. Priority decides
// what survives, not what goes where.
func bandBullets(own, supports []string) []string {
	if len(supports) == 0 {
		return own
	}
	if len(supports) > topperMaxSupports {
		// Name as many as fit and count the rest. Silently dropping them would
		// print a quiet Saturday that is actually the busiest day of the week.
		extra := len(supports) - (topperMaxSupports - 1)
		supports = append(supports[:topperMaxSupports-1], fmt.Sprintf("+%d more", extra))
	}
	// "Also:" on the first one only. Without it a support act reads as one more
	// detail about the headliner -- "BIKE NIGHT · 6PM" under RE-LAUNCH PARTY
	// looks like part of the party. One word, and the band stops lying.
	supports[0] = "Also: " + supports[0]

	room := max(topperMaxBandBullets-len(supports), 1)
	if len(own) > room {
		own = own[:room]
	}
	return append(own, supports...)
}

// compareBilling decides who headlines the day.
//
// Prominence first, which is the whole point of the axis: a standing pizza
// offer must never take the headline off a bike night, and the anniversary
// party outranks both. Then the earlier door time, because on a day with two
// equals the one that starts first is the one someone reading the card at
// lunchtime can still make.
func compareBilling(a, b topperOccurrence) int {
	if r := billingRank(a.prominence) - billingRank(b.prominence); r != 0 {
		return r
	}
	return a.at.Compare(b.at)
}

// billingRank orders the prominence values, lowest first. Unknown values sort
// with normal rather than to an extreme: a value this code has not heard of
// should behave like an ordinary event, not silently seize the headline or
// vanish beneath one.
func billingRank(p Prominence) int {
	switch p {
	case ProminenceFeatured:
		return 0
	case ProminenceBackground:
		return 2
	case ProminenceNormal:
		return 1
	default:
		// Named separately from ProminenceNormal so the exhaustive check still
		// catches a value added later without a ranking, while unknown values
		// keep sorting as ordinary events.
		return 1
	}
}

// supportBullet is how a second event on the same day reads: its name, then
// its time. Short enough to sit among the headliner's own bullets without
// looking like a different kind of thing.
func supportBullet(o topperOccurrence) string {
	if o.timeLabel == "" {
		return o.title
	}
	return o.title + " · " + o.timeLabel
}

// topperBullets turns an event's prose into the two or three lines that fit
// under a title.
//
// Description first, because someone who wrote a multi-line description was
// already writing bullets; summary is the fallback, split at sentences. Either
// way this is a view of copy written for the web, not a new field to maintain
// -- one more place to keep in step is exactly what this feature exists to
// avoid.
func topperBullets(e *Event) []string {
	source := strings.TrimSpace(e.Description)
	lines := splitLines(source)
	if len(lines) < 2 {
		source = strings.TrimSpace(e.Summary)
		if source == "" {
			source = strings.TrimSpace(e.Description)
		}
		lines = splitSentences(source)
	}

	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*•· \t"))
		if line == "" {
			continue
		}
		out = append(out, truncateWords(line, topperBulletChars))
		if len(out) == topperMaxBullets {
			break
		}
	}
	return out
}

// topperTime renders a door time the way it is said out loud: "7pm", not
// "7:00 PM". The band has room for a handful of characters next to the day,
// and the shorter form is the one people read at a glance.
func topperTime(at time.Time) string {
	if at.Minute() == 0 {
		return at.Format("3pm")
	}
	return at.Format("3:04pm")
}

func splitLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// splitSentences breaks a summary at sentence ends. Deliberately crude: it
// only has to turn "Quiz night every Wednesday. Free to play." into two
// bullets, and a mis-split reads as a slightly odd line break on a poster
// rather than as a bug.
func splitSentences(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.Index(s, ". ")
		if i < 0 {
			out = append(out, strings.TrimSuffix(strings.TrimSpace(s), "."))
			break
		}
		out = append(out, strings.TrimSpace(s[:i]))
		s = s[i+2:]
	}
	return out
}

// truncateWords clips at a word boundary so a cut bullet still reads as words.
func truncateWords(s string, limit int) string {
	if len([]rune(s)) <= limit {
		return s
	}
	r := []rune(s)[:limit]
	if i := strings.LastIndex(string(r), " "); i > limit/2 {
		return strings.TrimRight(string(r)[:i], " ,;:") + "…"
	}
	return strings.TrimRight(string(r), " ,;:") + "…"
}

// attachPosters loads each row's artwork, decoding once per attachment even
// when a series contributes several bands.
//
// Failures are dropped rather than propagated: a topper missing one thumbnail
// still prints and still says what is on, whereas a 500 at 4pm on a Sunday
// leaves the tables bare.
func (a *App) attachPosters(ctx context.Context, tenantID uuid.UUID, rows []TopperRow) []TopperRow {
	store := a.attachments()
	if store == nil {
		return rows
	}
	cache := map[uuid.UUID]*posterImage{}
	for i := range rows {
		id := rows[i].posterID
		if id == nil {
			continue
		}
		img, seen := cache[*id]
		if !seen {
			img = loadPoster(ctx, store, tenantID, *id)
			cache[*id] = img
		}
		rows[i].Poster = img
	}
	return rows
}

// weekStart snaps to the Sunday at or before day, at midnight in its own zone.
// Sunday because that is where a printed week starts on a table: the sheet goes
// out with the weekend and has to cover the one that follows.
func weekStart(day time.Time) time.Time {
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return d.AddDate(0, 0, -int(d.Weekday()))
}

// topperRange renders the week as the panel's subheading.
func topperRange(start time.Time, loc *time.Location) string {
	end := start.AddDate(0, 0, 6).In(loc)
	if start.Month() == end.Month() {
		return start.Format("January 2") + "-" + end.Format("2")
	}
	return start.Format("Jan 2") + " – " + end.Format("Jan 2")
}

// siteHost reduces the public URL template to the bare domain, which is what
// belongs in a footer someone reads from a table.
func siteHost(template string) string {
	tpl := strings.TrimSpace(template)
	if tpl == "" {
		return ""
	}
	u, err := url.Parse(strings.ReplaceAll(tpl, "{slug}", "x"))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(u.Host, "www.")
}
