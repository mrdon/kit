package builder

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestItemService_TranslatorErrorsPropagate(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()

	// Unknown operator at the top level should surface as a translator error.
	_, err := f.svc.Find(ctx, f.scope("x"), map[string]any{"age": map[string]any{"$weird": 1}}, nil, 0, 0)
	if err == nil {
		t.Fatal("expected error for unknown operator, got nil")
	}

	// Empty update → translator error.
	_, err = f.svc.UpdateOne(ctx, f.scope("x"), map[string]any{"name": "x"}, map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty update, got nil")
	}
}

func TestItemService_ScopeValidation(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()

	cases := map[string]Scope{
		"missing tenant":     {BuilderAppID: f.appID, Collection: "c", CallerUserID: f.userID},
		"missing app":        {TenantID: f.tenant.ID, Collection: "c", CallerUserID: f.userID},
		"missing collection": {TenantID: f.tenant.ID, BuilderAppID: f.appID, CallerUserID: f.userID},
		"missing caller":     {TenantID: f.tenant.ID, BuilderAppID: f.appID, Collection: "c"},
	}
	for name, sc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := f.svc.InsertOne(ctx, sc, map[string]any{"x": 1}); err == nil {
				t.Error("InsertOne accepted invalid scope")
			}
			if _, err := f.svc.Find(ctx, sc, nil, nil, 0, 0); err == nil {
				t.Error("Find accepted invalid scope")
			}
			if _, err := f.svc.CountDocuments(ctx, sc, nil); err == nil {
				t.Error("CountDocuments accepted invalid scope")
			}
		})
	}
}

// TestItemService_ConcurrentPush validates the atomicity story the translator
// promises: N goroutines each doing UpdateOne with $push on the same array
// must all land without write-loss.
func TestItemService_ConcurrentPush(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	s := f.scope("concurrent")

	ins, err := f.svc.InsertOne(ctx, s, map[string]any{"notes": []any{}})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	idFilter := map[string]any{"_id": ins["_id"]}

	const workers = 10
	var wg sync.WaitGroup
	errs := make([]error, workers)
	wg.Add(workers)
	for i := range workers {
		go func(idx int) {
			defer wg.Done()
			_, err := f.svc.UpdateOne(ctx, s, idFilter,
				map[string]any{"$push": map[string]any{"notes": idx}})
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}

	got, err := f.svc.FindOne(ctx, s, idFilter)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	notes, ok := got["notes"].([]any)
	if !ok {
		// Re-marshal via json for a helpful failure message.
		b, _ := json.Marshal(got)
		t.Fatalf("notes not []any: %s", b)
	}
	if len(notes) != workers {
		t.Fatalf("want %d notes landed, got %d: %v", workers, len(notes), notes)
	}
	// Verify each index appears exactly once.
	seen := map[int]bool{}
	for _, n := range notes {
		// JSON numbers decode as float64.
		v, ok := n.(float64)
		if !ok {
			t.Fatalf("note not numeric: %T %v", n, n)
		}
		seen[int(v)] = true
	}
	for i := range workers {
		if !seen[i] {
			t.Errorf("push %d did not land", i)
		}
	}
}
