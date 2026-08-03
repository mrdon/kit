// Tests for bulk briefing updates: the polymorphic card_ids argument and
// the partial-success semantics of UpdateMany.
package cards

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestCardIDListAcceptsStringOrArray is the whole point of the single
// polymorphic parameter: one id and many ids decode through the same field.
func TestCardIDListAcceptsStringOrArray(t *testing.T) {
	id1, id2 := uuid.NewString(), uuid.NewString()

	var single struct {
		CardIDs CardIDList `json:"card_ids"`
	}
	if err := json.Unmarshal([]byte(`{"card_ids":"`+id1+`"}`), &single); err != nil {
		t.Fatalf("single id: %v", err)
	}
	if len(single.CardIDs) != 1 || single.CardIDs[0] != id1 {
		t.Errorf("single id decoded to %v, want [%s]", single.CardIDs, id1)
	}

	var many struct {
		CardIDs CardIDList `json:"card_ids"`
	}
	if err := json.Unmarshal([]byte(`{"card_ids":["`+id1+`","`+id2+`"]}`), &many); err != nil {
		t.Fatalf("array: %v", err)
	}
	if len(many.CardIDs) != 2 || many.CardIDs[0] != id1 || many.CardIDs[1] != id2 {
		t.Errorf("array decoded to %v, want [%s %s]", many.CardIDs, id1, id2)
	}
}

// TestCardIDListRejectsOtherShapes guards against a number or object being
// coerced into something surprising instead of erroring.
func TestCardIDListRejectsOtherShapes(t *testing.T) {
	for _, body := range []string{`{"card_ids":42}`, `{"card_ids":{"id":"x"}}`, `{"card_ids":[1,2]}`} {
		var in struct {
			CardIDs CardIDList `json:"card_ids"`
		}
		if err := json.Unmarshal([]byte(body), &in); err == nil {
			t.Errorf("%s: expected an error, got %v", body, in.CardIDs)
		}
	}
}

func TestParseCardIDListRejectsEmptyAndMalformed(t *testing.T) {
	if _, err := parseCardIDList(nil); err == nil {
		t.Error("empty list should be an error, not a silent no-op")
	}
	if _, err := parseCardIDList([]string{uuid.NewString(), "not-a-uuid"}); err == nil {
		t.Error("a malformed id should fail the call, not be dropped from the batch")
	}
}

// TestParseCardIDsFromMCPArgs covers the type-switch path, which sees
// already-decoded `any` values rather than raw JSON.
func TestParseCardIDsFromMCPArgs(t *testing.T) {
	id1, id2 := uuid.NewString(), uuid.NewString()

	got, err := parseCardIDs(map[string]any{"card_ids": id1})
	if err != nil {
		t.Fatalf("string form: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("string form gave %d ids, want 1", len(got))
	}

	got, err = parseCardIDs(map[string]any{"card_ids": []any{id1, id2}})
	if err != nil {
		t.Fatalf("array form: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("array form gave %d ids, want 2", len(got))
	}

	if _, err := parseCardIDs(map[string]any{}); err == nil {
		t.Error("missing card_ids should error")
	}
	if _, err := parseCardIDs(map[string]any{"card_ids": []any{id1, 7}}); err == nil {
		t.Error("non-string array entry should error")
	}
	if _, err := parseCardIDs(map[string]any{"card_ids": 7}); err == nil {
		t.Error("numeric card_ids should error")
	}
}

// TestAckManyBriefingsGuards covers the checks that reject a batch before
// any card is touched. All of them return ahead of the first DB call, so
// this needs no pool.
func TestAckManyBriefingsGuards(t *testing.T) {
	svc := &CardService{}
	ids := []uuid.UUID{uuid.New()}

	if _, _, err := svc.AckManyBriefings(context.Background(), nil, ids, "bogus"); err == nil {
		t.Error("an invalid ack kind should be rejected before any card is acked")
	}
	if _, _, err := svc.AckManyBriefings(context.Background(), nil, nil, "archived"); err == nil {
		t.Error("an empty id list should error")
	}
	tooMany := make([]uuid.UUID, MaxBulkCards+1)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}
	if _, _, err := svc.AckManyBriefings(context.Background(), nil, tooMany, "archived"); err == nil {
		t.Errorf("more than %d cards should be rejected", MaxBulkCards)
	}
}

// TestAckBriefingInputAcceptsStringOrArray pins that the ack tool's own
// input struct decodes both shapes, not just update_briefing's.
func TestAckBriefingInputAcceptsStringOrArray(t *testing.T) {
	id := uuid.NewString()
	for _, body := range []string{
		`{"card_ids":"` + id + `","kind":"dismissed"}`,
		`{"card_ids":["` + id + `"],"kind":"dismissed"}`,
	} {
		var inp struct {
			CardIDs CardIDList `json:"card_ids"`
			Kind    string     `json:"kind"`
		}
		if err := json.Unmarshal([]byte(body), &inp); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		got, err := parseCardIDList(inp.CardIDs)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if len(got) != 1 {
			t.Errorf("%s gave %d ids, want 1", body, len(got))
		}
	}
}

func TestFormatBulkUpdateListsFailures(t *testing.T) {
	if got := formatBulkResult("Updated", 3, nil, "briefings"); got != "Updated 3 briefings." {
		t.Errorf("clean run = %q", got)
	}
	id := uuid.New()
	got := formatBulkResult("Updated", 2, []BulkCardFailure{{CardID: id, Err: errors.New("permission denied")}}, "briefings")
	// The failing id and its reason both have to appear, otherwise a partial
	// success needs a second call just to work out what didn't take.
	for _, want := range []string{id.String(), "permission denied", "2"} {
		if !strings.Contains(got, want) {
			t.Errorf("failure summary %q missing %q", got, want)
		}
	}
}
