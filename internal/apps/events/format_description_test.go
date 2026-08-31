package events

import (
	"strings"
	"testing"
	"time"
)

// The description is the public marketing copy, and it is what the weekly
// newsletter is written from. It was stored but never rendered, so callers saw
// only the one-line summary and wrote thin copy from it.
func TestFormatEventRendersFullDescription(t *testing.T) {
	e := &Event{
		Title:       "Pottery and Sips",
		StartsAt:    time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC),
		Timezone:    "America/Denver",
		Status:      StatusPublished,
		Visibility:  VisibilityPublic,
		Summary:     "Unplug, unwind, and make something by hand.",
		Description: "A guided clay workshop, no experience needed.\n\nYou leave with a piece you built by hand.",
		PrepNotes:   "Tables needed for the session.\nHost arrives at 12:30.",
	}

	got := FormatEvent(e, Settings{})

	if !strings.Contains(got, "You leave with a piece you built by hand.") {
		t.Errorf("description body missing from output:\n%s", got)
	}
	if !strings.Contains(got, "  description:\n    A guided clay workshop") {
		t.Errorf("description not rendered as an indented block:\n%s", got)
	}
	// Paragraphing is the author's; re-wrapping or collapsing it would change
	// copy that goes out to customers.
	if !strings.Contains(got, "no experience needed.\n\n    You leave") {
		t.Errorf("blank line between paragraphs not preserved:\n%s", got)
	}
	// Summary and description are distinct fields; rendering one must not
	// displace the other.
	if !strings.Contains(got, "  summary: Unplug, unwind, and make something by hand.\n") {
		t.Errorf("summary missing from output:\n%s", got)
	}
	// PrepNotes stays truncated to its first line: it is the internal brief and
	// has never been part of the public payload.
	if strings.Contains(got, "Host arrives at 12:30") {
		t.Errorf("staff notes should stay first-line only:\n%s", got)
	}
}

func TestFormatEventOmitsEmptyDescription(t *testing.T) {
	e := &Event{
		Title:      "Bike Night",
		StartsAt:   time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC),
		Timezone:   "America/Denver",
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
	}

	if got := FormatEvent(e, Settings{}); strings.Contains(got, "description:") {
		t.Errorf("empty description should emit no label:\n%s", got)
	}
}
