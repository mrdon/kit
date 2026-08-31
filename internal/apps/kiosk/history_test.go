package kiosk

import (
	"fmt"
	"testing"
)

// seed makes a board pointed at start, then walks it through each URL in
// order, returning the board.
func seed(t *testing.T, f *fixture, start string, walk ...string) *Board {
	t.Helper()
	b, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Taproom TV", URL: start})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, u := range walk {
		b, err = f.svc.Update(f.ctx, f.tenant.ID, b.ID, BoardInput{Key: b.Key, Name: b.Name, URL: u})
		if err != nil {
			t.Fatalf("Update to %s: %v", u, err)
		}
	}
	return b
}

func urls(changes []URLChange) []string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.URL
	}
	return out
}

func TestHistoryRecordsWhatAnUpdateDisplaced(t *testing.T) {
	f := newFixture(t)
	b := seed(t, f, "https://a.example.com", "https://b.example.com")

	got, err := f.svc.History(f.ctx, f.tenant.ID, b.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 1 || got[0].URL != "https://a.example.com" {
		t.Fatalf("want the displaced URL, got %v", urls(got))
	}
}

func TestHistoryIsNewestFirstAndCappedAtDepth(t *testing.T) {
	f := newFixture(t)
	// One more hop than the cap, so the oldest must fall off.
	var walk []string
	for i := 1; i <= HistoryDepth+1; i++ {
		walk = append(walk, fmt.Sprintf("https://u%d.example.com", i))
	}
	b := seed(t, f, "https://u0.example.com", walk...)

	got, err := f.svc.History(f.ctx, f.tenant.ID, b.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != HistoryDepth {
		t.Fatalf("want %d entries, got %d: %v", HistoryDepth, len(got), urls(got))
	}
	// Newest first: the most recently displaced is the one before the current.
	want := fmt.Sprintf("https://u%d.example.com", HistoryDepth)
	if got[0].URL != want {
		t.Errorf("want newest %q first, got %v", want, urls(got))
	}
	for _, c := range got {
		if c.URL == "https://u0.example.com" {
			t.Errorf("oldest URL should have been trimmed, got %v", urls(got))
		}
	}
}

func TestFirstURLIsNotAChange(t *testing.T) {
	f := newFixture(t)
	// Created with no URL, then given one: nothing was displaced.
	b := seed(t, f, "", "https://first.example.com")

	got, err := f.svc.History(f.ctx, f.tenant.ID, b.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a board's first URL should not record history, got %v", urls(got))
	}
}

func TestEditingOtherFieldsLeavesHistoryAlone(t *testing.T) {
	f := newFixture(t)
	b := seed(t, f, "https://a.example.com")

	_, err := f.svc.Update(f.ctx, f.tenant.ID, b.ID,
		BoardInput{Key: b.Key, Name: "Renamed", URL: b.URL, Notes: "by the door"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := f.svc.History(f.ctx, f.tenant.ID, b.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("renaming a board is not a URL change, got %v", urls(got))
	}
}

func TestHistoryByBoardIsScopedAndCapped(t *testing.T) {
	f := newFixture(t)
	one := seed(t, f, "https://one-a.example.com", "https://one-b.example.com")
	two, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Second", URL: "https://two-a.example.com"})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if _, err := f.svc.Update(f.ctx, f.tenant.ID, two.ID,
		BoardInput{Key: two.Key, Name: two.Name, URL: "https://two-b.example.com"}); err != nil {
		t.Fatalf("Update second: %v", err)
	}

	byBoard, err := ListURLHistoryByBoard(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatalf("ListURLHistoryByBoard: %v", err)
	}
	if got := urls(byBoard[one.ID]); len(got) != 1 || got[0] != "https://one-a.example.com" {
		t.Errorf("board one history wrong: %v", got)
	}
	if got := urls(byBoard[two.ID]); len(got) != 1 || got[0] != "https://two-a.example.com" {
		t.Errorf("board two history wrong: %v", got)
	}
}

func TestHistoryStopsAtTheTenantBoundary(t *testing.T) {
	f := newFixture(t)
	b := seed(t, f, "https://a.example.com", "https://b.example.com")

	other := newFixture(t)
	byBoard, err := ListURLHistoryByBoard(other.ctx, other.pool, other.tenant.ID)
	if err != nil {
		t.Fatalf("ListURLHistoryByBoard: %v", err)
	}
	if len(byBoard[b.ID]) != 0 {
		t.Error("another tenant could read this board's URL history")
	}
	got, err := other.svc.History(other.ctx, other.tenant.ID, b.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 0 {
		t.Error("History leaked across tenants")
	}
}
