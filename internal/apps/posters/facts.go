package posters

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/apps/events"
)

// Facts are the event fields a poster's copy may draw on: the public ones,
// never prep_notes. The snapshot stored with a set poster is compared
// against the event on every update to flag a stale poster.
type Facts struct {
	Title           string   `json:"title"`
	Summary         string   `json:"summary,omitempty"`
	Description     string   `json:"description,omitempty"`
	When            string   `json:"when"`
	Dates           []string `json:"dates,omitempty"`
	Repeats         string   `json:"repeats,omitempty"`
	AllDay          bool     `json:"all_day,omitempty"`
	Location        string   `json:"location,omitempty"`
	Venue           string   `json:"venue,omitempty"`
	Price           string   `json:"price,omitempty"`
	Capacity        int      `json:"capacity,omitempty"`
	RegistrationURL string   `json:"registration_url,omitempty"`
	Labels          []string `json:"labels,omitempty"`
}

// factsFor builds the snapshot from an event.
func factsFor(e *events.Event) Facts {
	loc := e.Loc()
	start := e.StartsAt.In(loc)
	f := Facts{
		Title:           e.Title,
		Summary:         e.Summary,
		Description:     e.Description,
		AllDay:          e.AllDay,
		Location:        e.Location,
		Venue:           string(e.Venue),
		RegistrationURL: e.RegistrationURL,
		Labels:          e.Labels,
	}
	if e.AllDay {
		f.When = start.Format("Monday 2 January 2006")
	} else {
		f.When = start.Format("Monday 2 January 2006, 3:04pm")
		if e.EndsAt != nil {
			f.When += " to " + e.EndsAt.In(loc).Format("3:04pm")
		}
	}
	f.Repeats = repeatWords(e)
	for _, d := range e.AllDates() {
		if len(f.Dates) >= 12 {
			break
		}
		f.Dates = append(f.Dates, d.In(loc).Format("Mon 2 Jan 2006"))
	}
	if e.PriceCents != nil {
		if *e.PriceCents == 0 {
			f.Price = "free"
		} else {
			f.Price = formatMoney(*e.PriceCents, e.Currency)
		}
	}
	if e.Capacity != nil {
		f.Capacity = *e.Capacity
	}
	return f
}

// repeatWords is the schedule in words, from the events app's own
// description so the two never disagree.
func repeatWords(e *events.Event) string {
	if !e.Repeats() {
		return ""
	}
	return e.CadenceWords()
}

func formatMoney(cents int64, currency string) string {
	sym := ""
	switch strings.ToUpper(currency) {
	case "", "USD", "CAD", "AUD":
		sym = "$"
	case "GBP":
		sym = "£"
	case "EUR":
		sym = "€"
	}
	if cents%100 == 0 {
		return fmt.Sprintf("%s%d", sym, cents/100)
	}
	return fmt.Sprintf("%s%d.%02d", sym, cents/100, cents%100)
}

// factsHash fingerprints a snapshot; a changed hash means a stale poster.
func factsHash(f Facts) string {
	b, _ := json.Marshal(f)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// factsText renders the snapshot for a prompt: one line per present field.
func factsText(f Facts) string {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	line("title", f.Title)
	line("when", f.When)
	line("repeats", f.Repeats)
	if len(f.Dates) > 1 {
		line("dates", strings.Join(f.Dates, "; "))
	}
	line("where", f.Location)
	if f.Venue == string(events.VenueOffsite) {
		line("venue", "offsite (not at our own place)")
	}
	line("price", f.Price)
	if f.Capacity > 0 {
		line("capacity", strconv.Itoa(f.Capacity))
	}
	line("tickets or rsvp", f.RegistrationURL)
	if len(f.Labels) > 0 {
		line("labels", strings.Join(f.Labels, ", "))
	}
	line("summary", f.Summary)
	if f.Description != "" {
		fmt.Fprintf(&b, "description:\n%s\n", f.Description)
	}
	return strings.TrimSpace(b.String())
}

// today is the date the copy prompt sees, so "this Friday" resolves.
func today(loc *time.Location) string { return time.Now().In(loc).Format("Monday 2 January 2006") }
