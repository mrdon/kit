package events

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrInvalid wraps every rejection so callers can map the whole class to one
// user-facing message instead of matching on prose.
var ErrInvalid = errors.New("invalid event")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// timeLayouts are accepted for human-entered times, most specific first. The
// bare forms are interpreted in the event's own zone, which is why the zone
// must be resolved before parsing.
var timeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseTime reads a timestamp in the given zone. A value carrying its own
// offset (RFC 3339) keeps it; a bare local time is anchored to loc.
func ParseTime(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, invalid("a start time is required")
	}
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, invalid("could not read %q as a date/time; use YYYY-MM-DD HH:MM", s)
}

// ResolveTimezone validates a named IANA zone. A fixed offset is refused
// outright: the whole DST story depends on carrying the zone, and "-07:00"
// would silently be wrong for half the year.
func ResolveTimezone(tz string) (*time.Location, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return nil, invalid("a timezone is required")
	}
	if tz == "Local" {
		return nil, invalid("timezone must be a named IANA zone such as America/Denver, not %q", tz)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, invalid("unknown timezone %q; use a named IANA zone such as America/Denver", tz)
	}
	return loc, nil
}

// validateEvent enforces every rule the database CHECK constraints also
// enforce, plus the ones SQL cannot express, so a bad write surfaces as a
// readable message rather than a constraint violation.
func validateEvent(e *Event) error {
	if strings.TrimSpace(e.Title) == "" {
		return invalid("a title is required")
	}
	if !ValidStatus(e.Status) {
		return invalid("status must be draft, published, or cancelled")
	}
	if !ValidVisibility(e.Visibility) {
		return invalid("visibility must be public or private")
	}
	if !ValidVenue(e.Venue) {
		return invalid("venue must be onsite or offsite")
	}
	if !ValidSpaceImpact(e.SpaceImpact) {
		return invalid("space impact must be none or partial")
	}
	if e.Venue == VenueOffsite && e.SpaceImpact != SpaceImpactNone {
		return invalid("an offsite event cannot reserve taproom space")
	}
	if _, err := ResolveTimezone(e.Timezone); err != nil {
		return err
	}
	if e.StartsAt.IsZero() {
		return invalid("a start time is required")
	}
	if e.EndsAt != nil && e.EndsAt.Before(e.StartsAt) {
		return invalid("the end time is before the start time")
	}
	if e.PriceCents != nil && *e.PriceCents < 0 {
		return invalid("price cannot be negative")
	}
	if e.Capacity != nil && *e.Capacity <= 0 {
		return invalid("capacity must be a positive number")
	}
	if e.ExpectedAttendance != nil && *e.ExpectedAttendance < 0 {
		return invalid("expected attendance cannot be negative")
	}
	if err := validateURL(e.RegistrationURL, "registration link"); err != nil {
		return err
	}
	return validateRecurrence(e)
}

// validateRecurrence is the write-time allowlist gate. Storing a rule the
// expander cannot read is the failure mode worth preventing here: Google would
// render the series correctly while every Kit date query silently saw nothing.
func validateRecurrence(e *Event) error {
	if strings.TrimSpace(e.RRule) == "" {
		return nil
	}
	if e.AllDay {
		return invalid("recurring all-day events are not supported; give the event a start and end time")
	}
	rule, err := ParseRule(e.RRule)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	if rule == nil {
		return nil
	}
	// RFC 5545 treats DTSTART as an occurrence even when it does not match
	// BYDAY, which would leave Kit and Google disagreeing about a stray first
	// instance. Refusing the mismatch keeps the two views identical.
	loc, err := ResolveTimezone(e.Timezone)
	if err != nil {
		return err
	}
	if wd := e.StartsAt.In(loc).Weekday(); !rule.CoversWeekday(wd) {
		return invalid("the event starts on a %s but the repeat rule does not include %s", wd, wd)
	}
	return nil
}

func validateURL(raw, label string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return invalid("%s must be a full http(s) URL", label)
	}
	return nil
}

// publishWarnings are non-blocking notes surfaced when an event goes live.
// They are advice, not validation: a paid event with no link is probably a
// mistake, but refusing the publish would be worse than mentioning it.
func publishWarnings(e *Event, s Settings) []string {
	var out []string
	if e.PriceCents != nil && *e.PriceCents > 0 && strings.TrimSpace(e.RegistrationURL) == "" {
		out = append(out, "this event has a price but no registration link, so there is nowhere to send buyers")
	}
	if e.IsPubliclyVisible() && s.CanonicalURL(e.Slug) == "" {
		out = append(out, "no public URL template is configured, so the website has no page to link to")
	}
	if e.IsPubliclyVisible() && !s.CalendarConfigured() {
		out = append(out, "no calendar is selected, so this will not appear on Google Calendar")
	}
	if e.Capacity != nil && e.ExpectedAttendance != nil && *e.ExpectedAttendance > *e.Capacity {
		out = append(out, "expected attendance is higher than the capacity")
	}
	return out
}
