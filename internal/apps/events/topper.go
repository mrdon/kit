package events

import (
	"context"
	"net/url"
	"sort"
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

// topperMaxRows bounds one panel. Past this the bands are too short to read
// from across a table, which defeats the object -- so the overflow is counted
// in the footer rather than silently dropped.
const topperMaxRows = 7

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
	// More is the number of events in the week that did not fit, so the panel
	// can admit to it instead of quietly implying the list is complete.
	More      int
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
	if len(rows) > topperMaxRows {
		out.More = len(rows) - topperMaxRows
		rows = rows[:topperMaxRows]
	}
	out.Rows = a.attachPosters(ctx, tenant.ID, rows)
	return out, nil
}

// topperRows expands every event into the occurrences that land inside the
// week, one band each.
//
// Expansion rather than a row-per-event is what makes a weekly series work:
// trivia's starts_at is the week it began, so the band has to say the date it
// happens THIS week. It also means a fortnightly event correctly produces no
// band on its off week.
func topperRows(events []Event, start, end time.Time, loc *time.Location) []TopperRow {
	var rows []TopperRow
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
			row := TopperRow{
				Day:     strings.ToUpper(at.Format("Mon")),
				Title:   strings.TrimSpace(e.Title),
				Bullets: topperBullets(e),
				at:      at,
			}
			if !e.AllDay {
				row.Time = topperTime(at)
			}
			row.posterID = e.HeroAttachmentID
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	return rows
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
