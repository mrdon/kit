package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/cards/shared"
	"github.com/mrdon/kit/internal/services"
)

// cardProvider surfaces todos as stack items. Scope is explicit — only
// todos the user owns, is assigned, or holds the role_scope for — so the
// stack doesn't flood with every tenant-visible todo.
type cardProvider struct {
	app *TodoApp
}

func (p *cardProvider) SourceApp() string { return "todo" }

func (p *cardProvider) StackItems(ctx context.Context, caller *services.Caller, cursor string, limit int) (shared.StackPage, error) {
	_ = cursor
	if limit <= 0 {
		limit = 50
	}
	todos, err := listStackTodos(ctx, p.app.svc.pool, caller, limit)
	if err != nil {
		return shared.StackPage{}, err
	}
	items := make([]shared.StackItem, 0, len(todos))
	for i := range todos {
		it, err := todoToStackItem(&todos[i])
		if err != nil {
			return shared.StackPage{}, err
		}
		items = append(items, it)
	}
	return shared.StackPage{Items: items}, nil
}

func (p *cardProvider) GetItem(ctx context.Context, caller *services.Caller, kind, id string) (*shared.DetailResponse, error) {
	if kind != "todo" {
		return nil, services.ErrNotFound
	}
	todoID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	t, events, err := p.app.svc.Get(ctx, caller, todoID)
	if err != nil {
		return nil, err
	}
	item, err := todoToStackItem(t)
	if err != nil {
		return nil, err
	}
	encodedEvents, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("encoding events: %w", err)
	}
	return &shared.DetailResponse{
		Item:   item,
		Extras: map[string]json.RawMessage{"events": encodedEvents},
	}, nil
}

func (p *cardProvider) DoAction(ctx context.Context, caller *services.Caller, kind, id, actionID string, _ json.RawMessage) (*shared.ActionResult, error) {
	if kind != "todo" {
		return nil, services.ErrNotFound
	}
	todoID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	switch actionID {
	case "complete":
		t, err := p.app.svc.Complete(ctx, caller, todoID)
		if err != nil {
			// Idempotent: a second complete on an already-done todo still
			// removes it from the client's stack without an error.
			if errors.Is(err, services.ErrNotFound) {
				return nil, err
			}
		}
		_ = t
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("todo", "todo", id)}}, nil
	}
	return nil, fmt.Errorf("unknown todo action %q", actionID)
}

// listStackTodos is the provider's dedicated query. Unlike TodoService.List,
// it explicitly restricts to the caller's personal surface (assignee,
// creator, role_scope member), so the stack doesn't include public todos
// belonging to unrelated parts of the org.
func listStackTodos(ctx context.Context, pool *pgxpool.Pool, c *services.Caller, limit int) ([]Todo, error) {
	var b strings.Builder
	args := []any{c.TenantID}

	b.WriteString(`SELECT ` + todoColumns + ` FROM app_todos
		WHERE tenant_id = $1
		  AND status != 'done'`)

	if !c.IsAdmin {
		// Caller's personal surface only. Admins see every non-done todo
		// (same rule as the agent tool), which avoids empty stacks for
		// admins with few direct assignments.
		args = append(args, c.UserID)
		if len(c.Roles) > 0 {
			args = append(args, c.Roles)
			b.WriteString(` AND (assigned_to = $2 OR created_by = $2 OR role_scope = ANY($3))`)
		} else {
			b.WriteString(` AND (assigned_to = $2 OR created_by = $2)`)
		}
	}

	// Tier ordering is computed client-side in todoToStackItem, but order
	// by the natural todo priority so the StackItems slice lands in a
	// reasonable order before the host merges it with other providers.
	b.WriteString(`
		ORDER BY
			CASE
				WHEN due_date < CURRENT_DATE THEN 0
				WHEN due_date <= CURRENT_DATE + INTERVAL '3 days' THEN 1
				ELSE 2
			END,
			CASE priority
				WHEN 'urgent' THEN 0
				WHEN 'high'   THEN 1
				WHEN 'medium' THEN 2
				WHEN 'low'    THEN 3
			END,
			due_date ASC NULLS LAST,
			created_at DESC
		LIMIT `)
	fmt.Fprintf(&b, "%d", limit)

	rows, err := pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("listing stack todos: %w", err)
	}
	defer rows.Close()
	var out []Todo
	for rows.Next() {
		t, err := scanTodo(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning todo: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func todoToStackItem(t *Todo) (shared.StackItem, error) {
	it := shared.StackItem{
		SourceApp:    "todo",
		Kind:         "todo",
		KindLabel:    "Todo",
		Icon:         "📋",
		ID:           t.ID.String(),
		Title:        t.Title,
		Body:         t.Description,
		KindWeight:   2,
		PriorityTier: todoTier(t),
		CreatedAt:    t.CreatedAt,
		Actions: []shared.StackAction{
			{ID: "complete", Direction: "right", Label: "Complete", Emoji: "✅"},
		},
	}
	if badge, ok := dueBadge(t.DueDate); ok {
		it.Badges = append(it.Badges, badge)
	}
	meta, err := json.Marshal(map[string]any{
		"due_date":    t.DueDate,
		"priority":    t.Priority,
		"status":      t.Status,
		"assigned_to": t.AssignedTo,
		"role_scope":  t.RoleScope,
	})
	if err != nil {
		return it, fmt.Errorf("encoding todo metadata: %w", err)
	}
	it.Metadata = meta
	return it, nil
}

// todoTier maps a todo to one of the shared priority tiers. Due today or
// earlier goes to critical; due within 3 days OR priority=urgent goes to
// high; priority=high or due within 7 days goes to medium; else low.
func todoTier(t *Todo) shared.PriorityTier {
	if days, ok := daysUntilDue(t.DueDate); ok {
		switch {
		case days <= 0:
			return shared.TierCritical
		case days <= 3:
			return shared.TierHigh
		case days <= 7:
			return shared.TierMedium
		}
	}
	switch t.Priority {
	case "urgent":
		return shared.TierHigh
	case "high":
		return shared.TierMedium
	}
	return shared.TierLow
}

// dueBadge builds a human-friendly badge from a due date. Returns ok=false
// when there is no due date.
func dueBadge(due *time.Time) (shared.StackBadge, bool) {
	days, ok := daysUntilDue(due)
	if !ok {
		return shared.StackBadge{}, false
	}
	switch {
	case days < 0:
		return shared.StackBadge{Label: fmt.Sprintf("Overdue %dd", -days), Tone: "urgent"}, true
	case days == 0:
		return shared.StackBadge{Label: "Due today", Tone: "urgent"}, true
	case days == 1:
		return shared.StackBadge{Label: "Due tomorrow", Tone: "warn"}, true
	case days <= 7:
		return shared.StackBadge{Label: fmt.Sprintf("Due in %dd", days), Tone: "warn"}, true
	default:
		return shared.StackBadge{Label: fmt.Sprintf("Due in %dd", days), Tone: "info"}, true
	}
}

// daysUntilDue returns the calendar-day delta (due - today) using each
// date's own year/month/day — no timezone conversion. Postgres DATE
// values arrive as UTC-midnight time.Time values; converting them to the
// server's local zone would shift the day backwards for any server west
// of UTC. We treat the due date as a calendar date (semantically
// timezone-less) and compare against today's calendar date in UTC so both
// sides share a frame.
func daysUntilDue(due *time.Time) (int, bool) {
	if due == nil {
		return 0, false
	}
	today := time.Now().UTC()
	todayDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	dueDay := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, time.UTC)
	return int(dueDay.Sub(todayDay).Hours() / 24), true
}
