package menu

import (
	"fmt"
	"strings"
	"testing"
)

func sixtels(vars ...map[string]any) sqItem { return rawItem("KTG", sixtelItem, vars...) }

func halves(vars ...map[string]any) sqItem { return rawItem("HBT", halfBarrelItem, vars...) }

func growlers(vars ...map[string]any) sqItem { return rawItem("GF", growlerItem, vars...) }

func togoCatalog(counts map[string]string, items ...sqItem) sqCatalog {
	return sqCatalog{
		Items:  append([]sqItem{littleDipper(), gravityCrowlers()}, items...),
		Flight: flightList(sqMod{ID: "M1", Name: "Little Dipper", Cents: 0}),
		Counts: counts,
	}
}

func addTo(p sqPlan, item string) *sqAdd {
	for i := range p.Adds {
		if strings.Contains(p.Adds[i].Summary, "added to "+item+" ") {
			return &p.Adds[i]
		}
	}
	return nil
}

func questionFor(p sqPlan, prefix string) *sqQuestion {
	for i := range p.Questions {
		if strings.HasPrefix(p.Questions[i].Key, prefix) {
			return &p.Questions[i]
		}
	}
	return nil
}

// An old keg size on a beer's own item prices its missing keg listing,
// which is counted in like a crowler.
func TestPlanSquareKegListedFromOldSize(t *testing.T) {
	dipper := rawItem("LD", "Little Dipper", rawVar("LD-16", "16oz", 700), rawVar("LD-4", "4oz", 250),
		rawVar("LD-K", "1/6 BBL Keg", 13000), rawVar("LD-H", "1/2 BBL Keg", 23000))
	cat := togoCatalog(nil, sixtels(trackedVar("KTG-M", "Mars Water", 13000)), halves(trackedVar("HBT-M", "Mars Water", 23000)))
	cat.Items[0] = dipper
	p := planSquare([]Beer{beer("Little Dipper", "7", "2.50")}, cat, map[string]bool{})
	for item, want := range map[string]string{sixtelItem: "$130.00", halfBarrelItem: "$230.00"} {
		add := addTo(p, item)
		if add == nil || !strings.Contains(add.Summary, want) || len(add.Stock) != 1 {
			t.Errorf("want Little Dipper in %s at %s, counted in, got:\n%s", item, want, summaries(p))
		}
	}
}

// With no old size, a beer is priced like its nearest listed neighbour: on
// a card for a beer that already existed, without asking for a new one.
func TestPlanSquareKegPricedLikeNeighbour(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50"), beer("Mars Water", "7", "2.50")}
	mars := rawItem("MW", "Mars Water", rawVar("MW-16", "16oz", 700), rawVar("MW-4", "4oz", 250))
	cat := togoCatalog(nil, sixtels(trackedVar("KTG-L", "Little Dipper", 13000)), mars)

	p := planSquare(rows, cat, map[string]bool{})
	q := questionFor(p, "sixtel|KTG|mars water|13000")
	if q == nil || q.Options[0].Fix.Kind != "variation" || q.Options[0].Fix.ObjectID != "KTG" || q.Options[0].Fix.Name != "Mars Water" {
		t.Fatalf("want a card to add Mars Water to %s at $130, got %+v", sixtelItem, p.Questions)
	}

	fresh := planSquare(rows, cat, map[string]bool{"mars water": true})
	if add := addTo(fresh, sixtelItem); add == nil || !strings.Contains(add.Summary, "Mars Water: added to Keg To Go at $130.00") {
		t.Fatalf("want a new beer's sixtel added without asking, got:\n%s", summaries(fresh))
	}
}

func hiddenVar(id, name string, cents int) map[string]any {
	v := trackedVar(id, name, cents)
	obj(v, "item_variation_data")["sellable"] = false
	return v
}

func sellableChanges(p sqPlan) map[string]string {
	out := map[string]string{}
	for _, c := range p.Counts {
		if c.Sellable != nil {
			out[c.ObjectID] = fmt.Sprintf("sellable=%v count=%s", *c.Sellable, c.Quantity)
		}
	}
	return out
}

// A keg whose beer left the menu is hidden, never deleted or counted out; one
// whose beer is back is shown and counted in; one on tap that staff marked
// sold out is left sold out.
func TestPlanSquareKegsHideAndShowWithTheTap(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50"), beer("Mars Water", "7", "2.50")}
	mars := rawItem("MW", "Mars Water", rawVar("MW-16", "16oz", 700), rawVar("MW-4", "4oz", 250))
	counts := map[string]string{"CTG-L": "99", "CTG-S": "99", "KTG-L": "99", "KTG-M": "0", "KTG-S": "99"}
	cat := togoCatalog(counts, mars, sixtels(hiddenVar("KTG-L", "Little Dipper", 13000),
		trackedVar("KTG-M", "Mars Water", 13000), trackedVar("KTG-S", "Snickers Tsar", 14500)))
	p := planSquare(rows, cat, map[string]bool{})
	got := sellableChanges(p)
	if got["KTG-S"] != "sellable=false count=" || got["KTG-L"] != "sellable=true count="+inStock {
		t.Fatalf("want Snickers hidden and Little Dipper shown and counted in, got %v", got)
	}
	if c := changes(p); c["KTG-M"] != "" || c["KTG-S"] == "delete" || c["CTG-S"] != "delete" {
		t.Fatalf("want Mars Water left sold out, no keg deleted, the Snickers crowler deleted; got %v", c)
	}
}

// A growler the menu prices rings from Growler Fill at that price, never as
// a size of the beer's own item, and a listing that has drifted is asked
// about.
func TestPlanSquareMenuGrowler(t *testing.T) {
	dipper := beer("Little Dipper", "7", "2.50")
	dipper.Pours = append(dipper.Pours, Pour{Size: "64oz", Label: "64oz Growler", Price: "18"})
	rows := []Beer{dipper}

	p := planSquare(rows, togoCatalog(nil, growlers(trackedVar("GF-M", "Mars Water", 2000))), map[string]bool{})
	if add := addTo(p, growlerItem); add == nil || !strings.Contains(add.Summary, "$18.00") {
		t.Fatalf("want Little Dipper in %s at $18, got:\n%s", growlerItem, summaries(p))
	}
	if strings.Contains(summaries(p), "added 64oz Growler") {
		t.Errorf("want no growler size on the beer's own item, got:\n%s", summaries(p))
	}

	drifted := planSquare(rows, togoCatalog(nil, growlers(trackedVar("GF-L", "Little Dipper", 2000))), map[string]bool{})
	if q := questionFor(drifted, "price|GF-L|"); q == nil || q.Options[0].Fix.Cents != 1800 {
		t.Fatalf("want a card to ring the menu's $18, got %+v", drifted.Questions)
	}
}

// Sodas get crowlers but no growler or keg, and a workspace without a to-go
// item is told so rather than failing.
func TestPlanSquareSodaNoKegAndMissingItemNoted(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50"), soda("Graviton Grapefruit Soda")}
	cat := togoCatalog(nil, graviton(), sixtels(trackedVar("KTG-L", "Little Dipper", 13000)))
	p := planSquare(rows, cat, map[string]bool{})
	for _, a := range p.Adds {
		if strings.HasPrefix(a.Summary, "Graviton") && strings.Contains(a.Summary, sixtelItem) {
			t.Errorf("want no keg for a soda, got %q", a.Summary)
		}
	}
	notes := strings.Join(p.Notes, "\n")
	if !strings.Contains(notes, "no "+halfBarrelItem+" item") || !strings.Contains(notes, "no "+growlerItem+" item") {
		t.Errorf("want missing to-go items noted, got:\n%s", notes)
	}
}

// A new beer's crowler and growler are priced at the share of its own draft
// price per ounce the listed ones charge, without asking, and the briefing
// says so; its keg copies the tier of the listed beer nearest per ounce.
func TestPlanSquareFreshBeerPricedByTheOunce(t *testing.T) {
	strong := Beer{Section: "Stouts", Name: "Tsar Bomba", ABV: "11%", Pours: pours("9oz Draft", "8", "4oz Draft", "4")}
	rows := []Beer{beer("Little Dipper", "6.50", "3"), beer("Mars Water", "6.50", "3"), strong}
	tsar := rawItem("TB", "Tsar Bomba", rawVar("TB9", "9oz", 800), rawVar("TB4", "4oz", 400))
	cat := togoCatalog(nil, tsar,
		rawItem("MW", "Mars Water", rawVar("MW-16", "16oz", 650), rawVar("MW-4", "4oz", 300)),
		growlers(trackedVar("GF-L", "Little Dipper", 1800), trackedVar("GF-M", "Mars Water", 1800)),
		sixtels(trackedVar("KTG-L", "Little Dipper", 13000), trackedVar("KTG-M", "Mars Water", 13500)))
	// 1800 / (64oz at $6.50 a pint) is 69%; at $8 for 9oz that is $39.
	p := planSquare(rows, cat, map[string]bool{"tsar bomba": true})
	growler := addTo(p, growlerItem)
	if growler == nil || !strings.Contains(growler.Summary, "Tsar Bomba: added to Growler Fill at $39.00, in stock (69% of its draft price per ounce") {
		t.Fatalf("want the growler priced by the ounce, got:\n%s", summaries(p))
	}
	keg := addTo(p, sixtelItem)
	if keg == nil || !strings.Contains(keg.Summary, "priced like") {
		t.Fatalf("want the sixtel priced like its nearest neighbour, got:\n%s", summaries(p))
	}
	for _, q := range p.Questions {
		if strings.Contains(q.Title, "Tsar Bomba") && strings.HasPrefix(q.Title, "Sell a") {
			t.Errorf("want a new beer's listings added without asking, got %q", q.Title)
		}
	}
}
