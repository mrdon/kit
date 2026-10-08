package task

import (
	"context"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/apps/cards/shared"
)

// TestAllTasksView: the All tasks view shows open work the feed leaves out
// for not being urgent, keeps the feed's personal scope, and a snooze still
// takes the card away.
func TestAllTasksView(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bob := f.caller(t, f.bob)

	// A month out and normal priority: real work, nowhere near the feed's
	// urgency bar.
	later := time.Now().AddDate(0, 1, 0)
	tk, err := f.svc.Create(ctx, bob, CreateInput{
		Title:          "not urgent",
		RoleName:       "founders",
		AssigneeUserID: &f.bob.ID,
		DueDate:        &later,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	feed, err := listStackTasks(ctx, f.pool, bob, 50, stackActive)
	if err != nil {
		t.Fatalf("listing feed: %v", err)
	}
	if containsStackTask(feed, tk.ID) {
		t.Fatalf("a task due in a month should not be in the feed")
	}

	p := &cardProvider{app: &TaskApp{svc: f.svc}}
	page, ok, err := p.ViewItems(ctx, bob, shared.ViewTasks, 50)
	if err != nil || !ok {
		t.Fatalf("ViewItems(tasks) = ok %v, err %v", ok, err)
	}
	if !containsStackItem(page.Items, tk.ID.String()) {
		t.Fatalf("the All tasks view should show a task that is not due yet")
	}

	if _, ok, _ := p.ViewItems(ctx, bob, shared.ViewFeed, 50); ok {
		t.Fatalf("the task provider should not answer the default feed as a view")
	}

	alice := f.caller(t, f.alice)
	all, err := listStackTasks(ctx, f.pool, alice, 50, stackAllOpen)
	if err != nil {
		t.Fatalf("listing alice's tasks: %v", err)
	}
	if containsStackTask(all, tk.ID) {
		t.Fatalf("alice is not in founders and should not see bob's task")
	}

	if _, err := f.svc.Snooze(ctx, bob, tk.ID, time.Now().Add(48*time.Hour)); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	all, err = listStackTasks(ctx, f.pool, bob, 50, stackAllOpen)
	if err != nil {
		t.Fatalf("listing after snooze: %v", err)
	}
	if containsStackTask(all, tk.ID) {
		t.Fatalf("a snoozed task should leave the All tasks view")
	}
}

func containsStackItem(items []shared.StackItem, id string) bool {
	for _, it := range items {
		if it.Kind == "task" && it.ID == id {
			return true
		}
	}
	return false
}
