// Integration tests for ItemService against the local test Postgres.
//
// Each test creates its own tenant + builder_app fixture so parallel runs
// don't step on each other. Cleanup cascades through ON DELETE CASCADE on
// tenants, so dropping the tenant wipes app_items and history.
package builder

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// itemFixture captures everything the service needs to make a Scope.
type itemFixture struct {
	pool   *pgxpool.Pool
	tenant *models.Tenant
	appID  uuid.UUID
	userID uuid.UUID
	svc    *ItemService
}

func (f *itemFixture) scope(collection string) Scope {
	return Scope{
		TenantID:     f.tenant.ID,
		BuilderAppID: f.appID,
		Collection:   collection,
		CallerUserID: f.userID,
	}
}

// newItemFixture spins up a tenant + user + builder_app, returning the
// wiring needed to exercise ItemService. Uses t.Cleanup so tests don't
// leak rows even on failure.
func newItemFixture(t *testing.T) *itemFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_builder_" + uuid.NewString()
	slug := models.SanitizeSlug("builder-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "builder-test", "enc-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_builder_"+uuid.NewString()[:8], "Builder User")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	// Insert a builder_apps row directly; no model helper exists yet.
	var appID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO builder_apps (tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, tenant.ID, "test-app-"+uuid.NewString()[:8], "integration test app", user.ID).Scan(&appID)
	if err != nil {
		t.Fatalf("creating builder_app: %v", err)
	}

	return &itemFixture{
		pool:   pool,
		tenant: tenant,
		appID:  appID,
		userID: user.ID,
		svc:    NewItemService(pool),
	}
}

func TestItemService_InsertOne_AutoSystemFields(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()

	before := time.Now().UTC()
	doc, err := f.svc.InsertOne(ctx, f.scope("customers"), map[string]any{
		"name":  "Jane",
		"email": "jane@example.com",
	})
	if err != nil {
		t.Fatalf("InsertOne: %v", err)
	}

	idStr, ok := doc["_id"].(string)
	if !ok || idStr == "" {
		t.Fatalf("_id missing or wrong type: %T %v", doc["_id"], doc["_id"])
	}
	if _, err := uuid.Parse(idStr); err != nil {
		t.Errorf("_id not a UUID: %v", err)
	}

	createdAt, ok := doc["_created_at"].(string)
	if !ok {
		t.Fatalf("_created_at not string: %T", doc["_created_at"])
	}
	updatedAt, ok := doc["_updated_at"].(string)
	if !ok {
		t.Fatalf("_updated_at not string: %T", doc["_updated_at"])
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		t.Errorf("_created_at not RFC3339Nano: %v", err)
	}
	if parsed.Before(before.Add(-time.Second)) || parsed.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("_created_at outside expected range: %v", parsed)
	}
	if createdAt != updatedAt {
		t.Errorf("_created_at (%s) != _updated_at (%s) on fresh insert", createdAt, updatedAt)
	}
}

func TestItemService_InsertOne_AdminProvidedID(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()

	want := uuid.New()
	doc, err := f.svc.InsertOne(ctx, f.scope("products"), map[string]any{
		"_id":  want.String(),
		"sku":  "ABC-123",
		"name": "Widget",
	})
	if err != nil {
		t.Fatalf("InsertOne: %v", err)
	}
	if got := doc["_id"]; got != want.String() {
		t.Fatalf("_id = %v, want %s", got, want)
	}

	// Verify it landed in the id column too.
	var rowID uuid.UUID
	err = f.pool.QueryRow(ctx, "SELECT id FROM app_items WHERE tenant_id = $1 AND id = $2",
		f.tenant.ID, want).Scan(&rowID)
	if err != nil {
		t.Fatalf("row not found with provided _id: %v", err)
	}
}

func TestItemService_FindOne(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	s := f.scope("people")

	_, err := f.svc.InsertOne(ctx, s, map[string]any{"name": "Alice", "age": 30})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	_, err = f.svc.InsertOne(ctx, s, map[string]any{"name": "Bob", "age": 25})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	doc, err := f.svc.FindOne(ctx, s, map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatalf("FindOne: %v", err)
	}
	if doc == nil {
		t.Fatal("FindOne returned nil for matching filter")
	}
	if doc["name"] != "Alice" {
		t.Errorf("got name=%v", doc["name"])
	}

	// No match → nil, no error.
	doc, err = f.svc.FindOne(ctx, s, map[string]any{"name": "Nobody"})
	if err != nil {
		t.Fatalf("FindOne (no match): %v", err)
	}
	if doc != nil {
		t.Errorf("expected nil doc for no match, got %v", doc)
	}
}

func TestItemService_Find_FilterSortLimitSkip(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	s := f.scope("events")

	for i, name := range []string{"c", "a", "b", "d"} {
		_, err := f.svc.InsertOne(ctx, s, map[string]any{"name": name, "idx": i})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Sort by name ASC; limit 2, skip 1 → should be "b", "c".
	docs, err := f.svc.Find(ctx, s, nil, []any{[]any{"name", 1}}, 2, 1)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("want 2 docs, got %d", len(docs))
	}
	if docs[0]["name"] != "b" || docs[1]["name"] != "c" {
		t.Errorf("got names %v, %v", docs[0]["name"], docs[1]["name"])
	}
}

func TestItemService_Find_ScopeIsolation(t *testing.T) {
	// Two tenants with the same collection name must not see each other's rows.
	fA := newItemFixture(t)
	fB := newItemFixture(t)
	ctx := context.Background()

	if _, err := fA.svc.InsertOne(ctx, fA.scope("shared"), map[string]any{"tag": "A"}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if _, err := fB.svc.InsertOne(ctx, fB.scope("shared"), map[string]any{"tag": "B"}); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	docsA, err := fA.svc.Find(ctx, fA.scope("shared"), nil, nil, 0, 0)
	if err != nil {
		t.Fatalf("find A: %v", err)
	}
	if len(docsA) != 1 || docsA[0]["tag"] != "A" {
		t.Errorf("tenant A saw wrong rows: %v", docsA)
	}

	docsB, err := fB.svc.Find(ctx, fB.scope("shared"), nil, nil, 0, 0)
	if err != nil {
		t.Fatalf("find B: %v", err)
	}
	if len(docsB) != 1 || docsB[0]["tag"] != "B" {
		t.Errorf("tenant B saw wrong rows: %v", docsB)
	}

	// Collections are also scope dimensions: different collection, same tenant → 0 rows.
	docsOther, err := fA.svc.Find(ctx, fA.scope("something-else"), nil, nil, 0, 0)
	if err != nil {
		t.Fatalf("find other collection: %v", err)
	}
	if len(docsOther) != 0 {
		t.Errorf("other collection should be empty, got %d", len(docsOther))
	}
}

func TestItemService_UpdateOne_Set(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	s := f.scope("docs")

	runID := uuid.New()
	scope := s
	scope.ScriptRunID = &runID

	ins, err := f.svc.InsertOne(ctx, scope, map[string]any{"name": "Alpha", "count": 1})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	origUpdatedAt := ins["_updated_at"].(string)
	// Make sure _updated_at moves on update — RFC3339Nano has sub-ms precision
	// but we still sleep a tick to guard against clock skew on slow runners.
	time.Sleep(2 * time.Millisecond)

	n, err := f.svc.UpdateOne(ctx, scope,
		map[string]any{"_id": ins["_id"]},
		map[string]any{"$set": map[string]any{"count": 42}})
	if err != nil {
		t.Fatalf("UpdateOne: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 updated, got %d", n)
	}

	got, err := f.svc.FindOne(ctx, scope, map[string]any{"_id": ins["_id"]})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	// JSONB numbers come back as float64.
	if got["count"].(float64) != 42 {
		t.Errorf("count = %v, want 42", got["count"])
	}
	if got["name"].(string) != "Alpha" {
		t.Errorf("name clobbered: %v", got["name"])
	}
	if got["_updated_at"].(string) == origUpdatedAt {
		t.Errorf("_updated_at not refreshed")
	}

	// History row should exist with the run id.
	idStr := ins["_id"].(string)
	rowID, _ := uuid.Parse(idStr)
	var histCount int
	err = f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items_history
		WHERE tenant_id = $1 AND id = $2 AND operation = 'UPDATE'
	`, f.tenant.ID, rowID).Scan(&histCount)
	if err != nil {
		t.Fatalf("history count: %v", err)
	}
	if histCount != 1 {
		t.Errorf("history row count = %d, want 1", histCount)
	}
	var histRunID *uuid.UUID
	var validFrom, validTo time.Time
	err = f.pool.QueryRow(ctx, `
		SELECT script_run_id, valid_from, valid_to FROM app_items_history
		WHERE tenant_id = $1 AND id = $2 AND operation = 'UPDATE'
	`, f.tenant.ID, rowID).Scan(&histRunID, &validFrom, &validTo)
	if err != nil {
		t.Fatalf("history query: %v", err)
	}
	// Insert had ScriptRunID=runID, so pre-update OLD.script_run_id = runID.
	if histRunID == nil || *histRunID != runID {
		t.Errorf("history script_run_id = %v, want %v", histRunID, runID)
	}
	if !validTo.After(validFrom) {
		t.Errorf("valid_to (%v) should be after valid_from (%v)", validTo, validFrom)
	}
}

func TestItemService_UpdateOne_Push(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	s := f.scope("lists")

	ins, err := f.svc.InsertOne(ctx, s, map[string]any{"name": "L", "items": []any{}})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	_, err = f.svc.UpdateOne(ctx, s,
		map[string]any{"_id": ins["_id"]},
		map[string]any{"$push": map[string]any{"items": "one"}})
	if err != nil {
		t.Fatalf("push 1: %v", err)
	}
	_, err = f.svc.UpdateOne(ctx, s,
		map[string]any{"_id": ins["_id"]},
		map[string]any{"$push": map[string]any{"items": "two"}})
	if err != nil {
		t.Fatalf("push 2: %v", err)
	}

	got, err := f.svc.FindOne(ctx, s, map[string]any{"_id": ins["_id"]})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("items not []any: %T %v", got["items"], got["items"])
	}
	if len(items) != 2 || items[0] != "one" || items[1] != "two" {
		t.Errorf("items = %v, want [one two]", items)
	}
}

func TestItemService_DeleteOne_HistoryHasDeleterProvenance(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	s := f.scope("rows")

	// Insert with a different (older) run id.
	originalRun := uuid.New()
	insScope := s
	insScope.ScriptRunID = &originalRun

	ins, err := f.svc.InsertOne(ctx, insScope, map[string]any{"name": "to-delete"})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	rowID, _ := uuid.Parse(ins["_id"].(string))

	// Delete with a new run id + different caller.
	deleterRun := uuid.New()
	deleterUser, err := models.GetOrCreateUser(ctx, f.pool, f.tenant.ID, "U_deleter_"+uuid.NewString()[:8], "Deleter")
	if err != nil {
		t.Fatalf("deleter user: %v", err)
	}
	delScope := Scope{
		TenantID:     f.tenant.ID,
		BuilderAppID: f.appID,
		Collection:   "rows",
		CallerUserID: deleterUser.ID,
		ScriptRunID:  &deleterRun,
	}

	n, err := f.svc.DeleteOne(ctx, delScope, map[string]any{"_id": ins["_id"]})
	if err != nil {
		t.Fatalf("DeleteOne: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 deleted, got %d", n)
	}

	// Row is gone.
	var stillThere int
	err = f.pool.QueryRow(ctx, "SELECT COUNT(*) FROM app_items WHERE tenant_id = $1 AND id = $2",
		f.tenant.ID, rowID).Scan(&stillThere)
	if err != nil {
		t.Fatalf("count row: %v", err)
	}
	if stillThere != 0 {
		t.Errorf("row still present after delete")
	}

	// History carries the DELETER's run id + user.
	var histRun *uuid.UUID
	var histUser *uuid.UUID
	err = f.pool.QueryRow(ctx, `
		SELECT script_run_id, caller_user_id
		FROM app_items_history
		WHERE tenant_id = $1 AND id = $2 AND operation = 'DELETE'
	`, f.tenant.ID, rowID).Scan(&histRun, &histUser)
	if err != nil {
		t.Fatalf("history query: %v", err)
	}
	if histRun == nil || *histRun != deleterRun {
		t.Errorf("history script_run_id = %v, want deleter run %v", histRun, deleterRun)
	}
	if histUser == nil || *histUser != deleterUser.ID {
		t.Errorf("history caller_user_id = %v, want deleter user %v", histUser, deleterUser.ID)
	}
}

func TestItemService_DeleteOne_NoMatchIsZero(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	n, err := f.svc.DeleteOne(ctx, f.scope("empty"), map[string]any{"name": "nope"})
	if err != nil {
		t.Fatalf("DeleteOne: %v", err)
	}
	if n != 0 {
		t.Errorf("want 0, got %d", n)
	}
}

func TestItemService_CountDocuments(t *testing.T) {
	f := newItemFixture(t)
	ctx := context.Background()
	s := f.scope("counters")

	for _, age := range []int{10, 20, 30, 40, 50} {
		_, err := f.svc.InsertOne(ctx, s, map[string]any{"age": age})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	total, err := f.svc.CountDocuments(ctx, s, nil)
	if err != nil {
		t.Fatalf("count all: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}

	over25, err := f.svc.CountDocuments(ctx, s, map[string]any{"age": map[string]any{"$gt": 25}})
	if err != nil {
		t.Fatalf("count filtered: %v", err)
	}
	if over25 != 3 {
		t.Errorf("over25 = %d, want 3", over25)
	}
}
