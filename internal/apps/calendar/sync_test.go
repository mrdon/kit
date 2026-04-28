package calendar

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureSingleEvent = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Kit Test//EN
BEGIN:VEVENT
UID:evt-1@example.com
SUMMARY:Saturday brunch shift
DESCRIPTION:Cover the brunch crowd
LOCATION:Tap Room
DTSTART:20260411T140000Z
DTEND:20260411T180000Z
END:VEVENT
END:VCALENDAR
`

const fixtureAllDay = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Kit Test//EN
BEGIN:VEVENT
UID:evt-allday@example.com
SUMMARY:Spring festival
LOCATION:Riverside Park
DTSTART;VALUE=DATE:20260418
DTEND;VALUE=DATE:20260419
END:VEVENT
END:VCALENDAR
`

const fixtureMultiple = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Kit Test//EN
BEGIN:VEVENT
UID:evt-1@example.com
SUMMARY:Brunch shift
DTSTART:20260411T140000Z
DTEND:20260411T180000Z
END:VEVENT
BEGIN:VEVENT
UID:evt-2@example.com
SUMMARY:Evening shift
DTSTART:20260411T220000Z
DTEND:20260412T020000Z
END:VEVENT
END:VCALENDAR
`

const fixtureNoUID = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Kit Test//EN
BEGIN:VEVENT
SUMMARY:Anonymous event
DTSTART:20260411T140000Z
DTEND:20260411T180000Z
END:VEVENT
END:VCALENDAR
`

func TestParseFeed_SingleTimedEvent(t *testing.T) {
	events, err := parseFeed([]byte(fixtureSingleEvent))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.UID != "evt-1@example.com" {
		t.Errorf("UID: got %q", e.UID)
	}
	if e.Summary != "Saturday brunch shift" {
		t.Errorf("Summary: got %q", e.Summary)
	}
	if e.Location != "Tap Room" {
		t.Errorf("Location: got %q", e.Location)
	}
	if e.AllDay {
		t.Errorf("AllDay: expected false")
	}
	wantStart := time.Date(2026, 4, 11, 14, 0, 0, 0, time.UTC)
	if !e.StartTime.Equal(wantStart) {
		t.Errorf("StartTime: got %s, want %s", e.StartTime, wantStart)
	}
	wantEnd := time.Date(2026, 4, 11, 18, 0, 0, 0, time.UTC)
	if !e.EndTime.Equal(wantEnd) {
		t.Errorf("EndTime: got %s, want %s", e.EndTime, wantEnd)
	}
}

func TestParseFeed_AllDayEvent(t *testing.T) {
	events, err := parseFeed([]byte(fixtureAllDay))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if !e.AllDay {
		t.Errorf("AllDay: expected true")
	}
	if e.Summary != "Spring festival" {
		t.Errorf("Summary: got %q", e.Summary)
	}
	// Start should be midnight on the date.
	if e.StartTime.Hour() != 0 || e.StartTime.Minute() != 0 {
		t.Errorf("StartTime should be midnight, got %s", e.StartTime)
	}
	if e.StartTime.Year() != 2026 || e.StartTime.Month() != time.April || e.StartTime.Day() != 18 {
		t.Errorf("StartTime date wrong: got %s", e.StartTime)
	}
}

func TestParseFeed_MultipleEvents(t *testing.T) {
	events, err := parseFeed([]byte(fixtureMultiple))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	uids := map[string]bool{events[0].UID: true, events[1].UID: true}
	if !uids["evt-1@example.com"] || !uids["evt-2@example.com"] {
		t.Errorf("missing expected UIDs: got %v", uids)
	}
}

// fixtureBadAttendee reproduces the Google Calendar export shape that broke
// Sleuth's onboarding: an ATTENDEE line with parameters but no `:value`,
// plus folded continuation, plus an ORGANIZER. The strict parser would
// otherwise return "unexpected end of property ATTENDEE" and lose every
// event in the feed.
const fixtureBadAttendee = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//Kit Test//EN\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:evt-bad-attendee@example.com\r\n" +
	"SUMMARY:Sync meeting\r\n" +
	"DTSTART:20260411T140000Z\r\n" +
	"DTEND:20260411T150000Z\r\n" +
	"ORGANIZER;CN=Owner:mailto:owner@example.com\r\n" +
	"ATTENDEE;CN=Foo Bar;EMAIL=foo@bar.com;ROLE=REQ-PARTICIPANT;PARTSTAT=NEED\r\n" +
	" S-ACTION;RSVP=TRUE\r\n" +
	"ATTENDEE;CN=Baz:mailto:baz@example.com\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseFeed_SurvivesMalformedAttendee(t *testing.T) {
	events, err := parseFeed([]byte(fixtureBadAttendee))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].UID != "evt-bad-attendee@example.com" {
		t.Errorf("UID: got %q", events[0].UID)
	}
	if events[0].Summary != "Sync meeting" {
		t.Errorf("Summary: got %q", events[0].Summary)
	}
}

func TestParseFeed_SkipsEventsWithoutUID(t *testing.T) {
	events, err := parseFeed([]byte(fixtureNoUID))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events (UID required), got %d", len(events))
	}
}

func TestParseFeed_MalformedReturnsError(t *testing.T) {
	_, err := parseFeed([]byte("this is not an iCal feed"))
	if err == nil {
		t.Fatal("expected error for malformed feed")
	}
}

// TestFetchAndParse_EndToEnd spins up an httptest.Server serving a fixture and
// runs the full HTTP fetch + parse path against it. Uses an allow-private
// client because httptest binds to loopback (which the SSRF guard would block).
func TestFetchAndParse_EndToEnd(t *testing.T) {
	var current atomic.Value
	current.Store(fixtureSingleEvent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = io.WriteString(w, current.Load().(string))
	}))
	defer server.Close()

	client := newSyncClient(true) // allow loopback
	ctx := context.Background()

	// Initial fetch.
	events, err := fetchAndParse(ctx, client, server.URL)
	if err != nil {
		t.Fatalf("first fetchAndParse: %v", err)
	}
	if len(events) != 1 || events[0].UID != "evt-1@example.com" {
		t.Fatalf("first fetch: got %+v", events)
	}

	// Mutate the served fixture; second fetch should reflect new state.
	current.Store(fixtureMultiple)
	events, err = fetchAndParse(ctx, client, server.URL)
	if err != nil {
		t.Fatalf("second fetchAndParse: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("second fetch: expected 2 events, got %d", len(events))
	}
}

func TestFetchAndParse_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newSyncClient(true)
	_, err := fetchAndParse(context.Background(), client, server.URL)
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention HTTP 500, got %v", err)
	}
}

func TestNewSyncClient_BlocksLoopbackByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, fixtureSingleEvent)
	}))
	defer server.Close()

	client := newSyncClient(false) // SSRF guard ON
	_, err := fetchAndParse(context.Background(), client, server.URL)
	if err == nil {
		t.Fatal("expected SSRF block to prevent fetching loopback")
	}
}
