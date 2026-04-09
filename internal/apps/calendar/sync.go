package calendar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"

	ics "github.com/arran4/golang-ical"
)

const (
	syncFetchTimeout  = 10 * time.Second
	syncMaxResponseSz = 5 * 1024 * 1024 // 5 MB
)

// httpClient is the package default for production sync. Tests may inject their
// own client by calling parseFeedFromReader directly, or set this var.
var httpClient = newSyncClient(false)

// newSyncClient builds an HTTP client with SSRF protection. allowPrivate=true
// is for tests using httptest.Server (loopback).
func newSyncClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: syncFetchTimeout}
	if !allowPrivate {
		dialer.Control = ssrfControl
	}
	return &http.Client{
		Timeout:   syncFetchTimeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if !allowPrivate {
				return validateURL(req.URL)
			}
			return nil
		},
	}
}

func validateURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return errors.New("private/internal IPs not allowed")
	}
	return nil
}

func ssrfControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return errors.New("SSRF blocked: private/internal IP")
	}
	return nil
}

// fetchAndParse downloads the iCal feed and parses it into normalized Events.
// The returned events have no IDs/tenant set — the caller fills those in on upsert.
func fetchAndParse(ctx context.Context, client *http.Client, rawURL string) ([]Event, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if client == nil {
		if err := validateURL(u); err != nil {
			return nil, err
		}
		client = httpClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Kit-Calendar/1.0)")
	req.Header.Set("Accept", "text/calendar, text/plain;q=0.8, */*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching calendar: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, syncMaxResponseSz))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	return parseFeed(body)
}

// parseFeed parses raw iCal bytes into Events. Exposed for tests.
func parseFeed(body []byte) ([]Event, error) {
	cal, err := ics.ParseCalendar(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parsing iCal: %w", err)
	}

	var out []Event
	for _, ev := range cal.Events() {
		parsed, ok := convertEvent(ev)
		if !ok {
			continue
		}
		out = append(out, parsed)
	}
	return out, nil
}

func convertEvent(ev *ics.VEvent) (Event, bool) {
	uidProp := ev.GetProperty(ics.ComponentPropertyUniqueId)
	if uidProp == nil || uidProp.Value == "" {
		// No UID — can't deduplicate. Skip.
		return Event{}, false
	}

	// Detect all-day events: DTSTART with VALUE=DATE has no time component.
	allDay := false
	if dtStart := ev.GetProperty(ics.ComponentPropertyDtStart); dtStart != nil {
		if vals, ok := dtStart.ICalParameters["VALUE"]; ok && slices.Contains(vals, "DATE") {
			allDay = true
		}
	}

	var start, end time.Time
	var err error
	if allDay {
		start, err = ev.GetAllDayStartAt()
		if err != nil {
			return Event{}, false
		}
		end, err = ev.GetAllDayEndAt()
		if err != nil {
			end = start.Add(24 * time.Hour)
		}
		// All-day events are date-only; the library parses them in time.Local.
		// Re-anchor to UTC midnight on the same calendar date so the stored
		// timestamp is timezone-independent.
		start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
		end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	} else {
		start, err = ev.GetStartAt()
		if err != nil {
			return Event{}, false
		}
		end, err = ev.GetEndAt()
		if err != nil {
			// No DTEND — use start as a zero-length placeholder.
			end = start
		}
	}

	return Event{
		UID:         uidProp.Value,
		Summary:     propValue(ev, ics.ComponentPropertySummary),
		Description: propValue(ev, ics.ComponentPropertyDescription),
		Location:    propValue(ev, ics.ComponentPropertyLocation),
		StartTime:   start.UTC(),
		EndTime:     end.UTC(),
		AllDay:      allDay,
	}, true
}

func propValue(ev *ics.VEvent, name ics.ComponentProperty) string {
	p := ev.GetProperty(name)
	if p == nil {
		return ""
	}
	return p.Value
}

// SyncCalendarFunc is overrideable for tests.
var fetchAndParseFn = fetchAndParse

// syncOne fetches and upserts events for a single calendar. The returned error
// is also persisted on the calendar row by the caller.
func (s *CalendarService) syncOne(ctx context.Context, c *Calendar) error {
	events, err := fetchAndParseFn(ctx, nil, c.URL)
	if err != nil {
		return err
	}
	return upsertEvents(ctx, s.pool, c.TenantID, c.ID, events)
}
