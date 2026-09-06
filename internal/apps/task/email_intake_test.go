package task

import (
	"testing"
	"time"

	"github.com/mrdon/kit/internal/apps/email"
	"github.com/mrdon/kit/internal/models"
)

func ptime(t time.Time) *time.Time { return &t }

func TestEmailIntakeDueUsesRunNotWatermark(t *testing.T) {
	now := time.Date(2026, 9, 6, 21, 0, 0, 0, time.UTC)
	daily := "0 7 * * *"

	// The regression: a quiet mailbox parks the watermark days in the past
	// (it tracks email dates, and no new email means no movement). Due-ness
	// must not read it, or the row is overdue on every sweep tick and posts a
	// briefing card every 15 minutes.
	stale := models.EmailIntake{
		Schedule:      daily,
		LastScannedAt: ptime(time.Date(2026, 9, 4, 20, 28, 0, 0, time.UTC)),
		LastRunAt:     ptime(now.Add(-2 * time.Hour)),
	}
	if emailIntakeDue(stale, "UTC", now) {
		t.Fatal("row that ran 2h ago on a daily schedule should not be due")
	}

	// Never run: always due, whatever the watermark says.
	fresh := models.EmailIntake{Schedule: daily, LastScannedAt: ptime(now.Add(-time.Hour))}
	if !emailIntakeDue(fresh, "UTC", now) {
		t.Fatal("never-run row should be due")
	}

	// Ran yesterday morning: today's 07:00 tick has passed.
	overdue := models.EmailIntake{
		Schedule:  daily,
		LastRunAt: ptime(time.Date(2026, 9, 5, 7, 0, 0, 0, time.UTC)),
	}
	if !emailIntakeDue(overdue, "UTC", now) {
		t.Fatal("row last run yesterday should be due")
	}

	bad := models.EmailIntake{Schedule: "not a cron", LastRunAt: ptime(now.Add(-48 * time.Hour))}
	if emailIntakeDue(bad, "UTC", now) {
		t.Fatal("unparseable schedule should be treated as not due")
	}
}

func TestNewerThanDropsAlreadySeenMail(t *testing.T) {
	since := time.Date(2026, 9, 4, 20, 28, 0, 0, time.UTC)
	// IMAP SEARCH SINCE is date-granular, so the server hands back everything
	// from the watermark's *day*, including mail already triaged.
	got := newerThan([]email.Summary{
		{UID: 1, Date: since.Add(-8 * time.Hour)}, // same day, already seen
		{UID: 2, Date: since},                     // the watermark itself
		{UID: 3, Date: since.Add(time.Minute)},    // genuinely new
		{UID: 4},                                  // undated — keep, don't silently drop
	}, since)

	if len(got) != 2 || got[0].UID != 3 || got[1].UID != 4 {
		t.Fatalf("expected uids [3 4], got %+v", got)
	}
}
