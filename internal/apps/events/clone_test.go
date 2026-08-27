package events

import (
	"errors"
	"testing"
	"time"
)

func TestCloneCopiesContentAsAnIndependentDraft(t *testing.T) {
	f := newFixture(t)
	cap50 := 50
	price := int64(1500)
	src := f.create(t, CreateParams{
		Title: "Supper Club", StartsAt: "2026-09-04 18:00", EndsAt: "2026-09-04 21:00",
		Summary: "Five courses", Description: "Long blurb", PrepNotes: "Set the long table",
		Location: "Back room", Visibility: VisibilityPublic, SpaceImpact: SpaceImpactPartial,
		Capacity: &cap50, PriceCents: &price, RepeatDates: []string{"2026-10-02 18:00"},
	})
	if _, err := f.svc.Publish(f.ctx, f.tenant.ID, src.ID); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	dup, err := f.svc.Clone(f.ctx, f.tenant.ID, src.ID, CloneParams{})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	// The content someone would otherwise retype.
	if dup.Summary != src.Summary || dup.Description != src.Description || dup.PrepNotes != src.PrepNotes {
		t.Error("the copy lost the blurb or the staff notes")
	}
	if dup.Location != src.Location || dup.SpaceImpact != src.SpaceImpact {
		t.Error("the copy lost its location or space impact")
	}
	if dup.Capacity == nil || *dup.Capacity != cap50 || dup.PriceCents == nil || *dup.PriceCents != price {
		t.Error("the copy lost its capacity or price")
	}
	if len(dup.RDates) != 1 {
		t.Errorf("the copy lost the extra dates: %v", dup.RDates)
	}

	// A copy of a PUBLISHED event must not itself be published: it would reach
	// the team calendar -- and, being public, the website -- before anyone had
	// corrected the date.
	if dup.Status != StatusDraft {
		t.Errorf("clone status = %q, want draft", dup.Status)
	}
	if dup.ID == src.ID {
		t.Fatal("clone reused the original's id")
	}
	if dup.Slug == src.Slug {
		t.Fatalf("clone reused the public URL %q; two rows cannot share one", dup.Slug)
	}
	// Zeroed sync state, or the two rows fight over one calendar entry.
	if dup.GCalEventID != "" || dup.GCalCalendarID != "" || dup.GCalContentHash != "" {
		t.Errorf("clone inherited calendar sync state: %+v", dup)
	}
}

func TestCloneDefaultsTitleToACopy(t *testing.T) {
	f := newFixture(t)
	src := f.create(t, CreateParams{Title: "Trivia Night"})

	dup, err := f.svc.Clone(f.ctx, f.tenant.ID, src.ID, CloneParams{})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if dup.Title != "Trivia Night (copy)" {
		t.Errorf("title = %q, want %q", dup.Title, "Trivia Night (copy)")
	}

	named, err := f.svc.Clone(f.ctx, f.tenant.ID, src.ID, CloneParams{Title: "Trivia Night 2"})
	if err != nil {
		t.Fatalf("Clone with title: %v", err)
	}
	if named.Title != "Trivia Night 2" {
		t.Errorf("title = %q, want the supplied one", named.Title)
	}
}

// Editing either row must never touch the other. A clone is a copy, not a link.
func TestCloneIsIndependentOfTheOriginal(t *testing.T) {
	f := newFixture(t)
	src := f.create(t, CreateParams{
		Title: "Market", StartsAt: "2026-09-04 10:00",
		Summary: "original", RepeatDates: []string{"2026-10-02 10:00"},
	})
	dup, err := f.svc.Clone(f.ctx, f.tenant.ID, src.ID, CloneParams{})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	changed := "edited"
	if _, err := f.svc.Update(f.ctx, f.tenant.ID, src.ID, UpdateParams{Summary: &changed}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	after, err := f.svc.Get(f.ctx, f.tenant.ID, dup.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Summary != "original" {
		t.Errorf("editing the original changed the copy: summary = %q", after.Summary)
	}
	if len(after.RDates) != 1 {
		t.Errorf("the copy's dates changed with the original's: %v", after.RDates)
	}
}

// A clone aimed at a new date is "that event, on this day". Carrying the
// previous series' leftover dates onto it is never what was meant.
func TestCloneWithNewStartDropsExtraDatesAndKeepsLength(t *testing.T) {
	f := newFixture(t)
	src := f.create(t, CreateParams{
		Title: "Beer School", StartsAt: "2026-09-04 18:00", EndsAt: "2026-09-04 20:30",
		RepeatDates: []string{"2026-10-02 18:00", "2026-11-06 18:00"},
	})

	dup, err := f.svc.Clone(f.ctx, f.tenant.ID, src.ID, CloneParams{StartsAt: "2027-01-08 18:00"})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if len(dup.RDates) != 0 {
		t.Errorf("a re-dated clone kept the old series' dates: %v", dup.RDates)
	}
	if got := dup.StartsAt.In(dup.Loc()).Format("2006-01-02 15:04"); got != "2027-01-08 18:00" {
		t.Errorf("start = %s", got)
	}
	// EndsAt is an absolute instant; assigning only the start would have left
	// the end in September, inverting the event.
	if d := dup.End().Sub(dup.StartsAt); d != 150*time.Minute {
		t.Errorf("length = %v, want 2h30m", d)
	}
}

func TestCloneRejectsAnUnknownEvent(t *testing.T) {
	f := newFixture(t)
	src := f.create(t, CreateParams{Title: "Real"})

	// Another tenant's id must not be reachable.
	other := newFixture(t)
	if _, err := other.svc.Clone(other.ctx, other.tenant.ID, src.ID, CloneParams{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cloning across tenants must fail with ErrNotFound, got %v", err)
	}
}
