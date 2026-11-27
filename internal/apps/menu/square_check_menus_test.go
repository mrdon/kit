package menu

import (
	"strings"
	"testing"
)

// onScreen puts an item on the Bar screen, as the Menus editor does.
func onScreen(it sqItem) sqItem {
	data := obj(it.Raw, "item_data")
	data["categories"] = append(data["categories"].([]any), map[string]any{"id": "BAR", "ordinal": float64(7)})
	return parseSqItem(it.Raw)
}

func dipperRows() []Beer {
	return []Beer{beer("Little Dipper", "6.50", "3"), beer("Pumpkin Little Dipper", "7.50", "3")}
}

func dipperPair(pumpkin sqItem) sqCatalog {
	dipper := onScreen(rawItem("LD", "Little Dipper", rawVar("LD-16", "16oz", 650), rawVar("LD-4", "4oz", 300)))
	return sqCatalog{Items: []sqItem{dipper, pumpkin}, MenuCats: map[string]string{"BAR": "Bar"}}
}

func pumpkin(vars ...map[string]any) sqItem {
	if len(vars) == 0 {
		vars = []map[string]any{rawVar("PLD-16", "16oz", 750), rawVar("PLD-4", "4oz", 300)}
	}
	it := rawItem("PLD", "Pumpkin Little Dipper", vars...)
	data := obj(it.Raw, "item_data")
	data["description"] = "Now with pumpkin"
	data["description_html"] = "<p>Now with pumpkin</p>"
	data["description_plaintext"] = "Now with pumpkin"
	return parseSqItem(it.Raw)
}

// A menu drink taken off the register screen goes back on, sent whole so
// no size is lost, and keeps its description.
func TestPlanSquareButtonPutBack(t *testing.T) {
	p := planSquare(dipperRows(), dipperPair(pumpkin()), map[string]bool{})
	if len(p.Adds) != 1 {
		t.Fatalf("want one add, got:\n%s", summaries(p))
	}
	add := p.Adds[0]
	if !strings.Contains(add.Summary, "put back on Bar like Little Dipper") {
		t.Errorf("summary = %q", add.Summary)
	}
	data := obj(add.Objects[0], "item_data")
	var ids []string
	for _, c := range objs(data, "categories") {
		ids = append(ids, str(c, "id"))
	}
	if strings.Join(ids, ",") != "CAT,BAR" {
		t.Errorf("categories = %v, want the reporting one kept and Bar added", ids)
	}
	if len(objs(data, "variations")) != 2 {
		t.Errorf("the upsert must carry every size, got %d", len(objs(data, "variations")))
	}
	if _, ok := data["description"]; ok || data["description_html"] != "<p>Now with pumpkin</p>" {
		t.Errorf("description must go back as description_html only: %v", data)
	}
	// The catalog it was read from is untouched.
	if len(objs(obj(pumpkin().Raw, "item_data"), "categories")) != 1 {
		t.Error("planning changed the catalog")
	}
}

func TestPlanSquareButtonLeftAlone(t *testing.T) {
	if p := planSquare(dipperRows(), dipperPair(onScreen(pumpkin())), map[string]bool{}); len(p.Adds) != 0 {
		t.Errorf("an item with a button needs nothing, got:\n%s", summaries(p))
	}
	cat := dipperPair(pumpkin())
	cat.MenuCats = nil
	if p := planSquare(dipperRows(), cat, map[string]bool{}); len(p.Adds) != 0 {
		t.Errorf("without the screens read, buttons are left alone, got:\n%s", summaries(p))
	}
}

// The button goes back before a missing size is added, so the whole-item
// upsert cannot drop the new size.
func TestPlanSquareButtonBeforeNewSize(t *testing.T) {
	p := planSquare(dipperRows(), dipperPair(pumpkin(rawVar("PLD-16", "16oz", 750))), map[string]bool{})
	if len(p.Adds) < 2 || !strings.Contains(p.Adds[0].Summary, "register button") {
		t.Fatalf("want the button first, got:\n%s", summaries(p))
	}
}

func TestPlanSquareSoldOutNotice(t *testing.T) {
	out := rawVar("PLD-16", "16oz", 750)
	out["version"] = float64(42)
	obj(out, "item_variation_data")["location_overrides"] = []any{map[string]any{"location_id": "L", "sold_out": true}}
	p := planSquare(dipperRows(), dipperPair(onScreen(pumpkin(out, rawVar("PLD-4", "4oz", 300)))), map[string]bool{})
	var got *sqQuestion
	for i, q := range p.Questions {
		if strings.HasPrefix(q.Key, "soldout|") {
			got = &p.Questions[i]
		}
	}
	if got == nil {
		t.Fatalf("want a sold-out notice, got %+v", p.Questions)
	}
	if !got.Notice || got.Key != "soldout|PLD|PLD-16@42" || !strings.Contains(got.Body, "**16oz**") {
		t.Errorf("notice = %+v", *got)
	}
}
