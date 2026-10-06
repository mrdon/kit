package menu

import (
	"strings"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/apps/square"
)

// Every clock here is passed in explicitly, so the calendar dates below are
// what the test is about rather than a fixture that ages.

var denver, _ = time.LoadLocation("America/Denver")

func weekdayHH() HappyHour {
	h := DefaultHappyHour()
	h.Enabled = true
	h.Beers = []string{"Light Lift Lager", "Mars Water", "Wicked Nebula"}
	return h
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, denver)
	if err != nil {
		panic(err)
	}
	return t
}

func TestActiveAt(t *testing.T) {
	h := weekdayHH()
	h.StartsOn = "2026-10-12" // a Monday
	cases := []struct {
		when string
		want bool
	}{
		{"2026-10-09 15:30", false}, // Friday before it starts
		{"2026-10-12 14:59", false},
		{"2026-10-12 15:00", true},
		{"2026-10-12 16:59", true},
		{"2026-10-12 17:00", false}, // end is exclusive
		{"2026-10-17 15:30", false}, // Saturday
		{"2026-10-16 15:30", true},  // Friday
	}
	for _, c := range cases {
		if got := h.ActiveAt(at(c.when), denver); got != c.want {
			t.Errorf("ActiveAt(%s) = %v, want %v", c.when, got, c.want)
		}
	}
	// The window is read in the workspace's zone, not the server's: 21:30 UTC
	// is 15:30 in Denver.
	if !h.ActiveAt(time.Date(2026, 10, 12, 21, 30, 0, 0, time.UTC), denver) {
		t.Error("window should be read in the workspace timezone")
	}
	h.Enabled = false
	if h.ActiveAt(at("2026-10-12 15:30"), denver) {
		t.Error("a disabled happy hour is never on")
	}
}

func TestValidateAndNormalize(t *testing.T) {
	h := weekdayHH()
	h.Days = []string{"fri", "mon", "WED"}
	h.Beers = []string{" Mars  Water ", "mars water", "Wicked Nebula"}
	h.Normalize()
	if strings.Join(h.Days, ",") != "mon,wed,fri" {
		t.Errorf("days = %v, want week order", h.Days)
	}
	if strings.Join(h.Beers, "|") != "Mars Water|Wicked Nebula" {
		t.Errorf("beers = %v, want trimmed and deduplicated", h.Beers)
	}
	if err := h.Validate(); err != nil {
		t.Fatalf("valid setting rejected: %v", err)
	}

	bad := weekdayHH()
	bad.End = "15:00"
	if bad.Validate() == nil {
		t.Error("an end at or before the start must be rejected")
	}
	bad = weekdayHH()
	bad.Days = []string{"funday"}
	if bad.Validate() == nil {
		t.Error("an unknown day must be rejected")
	}
	bad = weekdayHH()
	bad.Beers = nil
	if bad.Validate() == nil {
		t.Error("an enabled happy hour with no beers must be rejected")
	}
}

func catalogItem(name, kitchen string, sizes map[string]int) square.CatalogItem {
	it := square.CatalogItem{ID: "I_" + name, Name: name, KitchenName: kitchen}
	for s, c := range sizes {
		it.Variations = append(it.Variations, square.CatalogVariation{
			ID: "V_" + name + "_" + s, Name: s, PriceCents: c, Currency: "USD",
		})
	}
	return it
}

func TestPlanGroupsByDiscount(t *testing.T) {
	items := []square.CatalogItem{
		catalogItem("Light Lift Lager", "", map[string]int{"16oz": 650, "4oz": 300}),
		catalogItem("Mars Water", "", map[string]int{"16oz": 650}),
		catalogItem("Wicked Nebula", "", map[string]int{"16oz": 800}),
		catalogItem("Oktoberfest", "", map[string]int{"16oz": 700}),
	}
	plan := planHappyHour(weekdayHH(), items)
	if len(plan.Problems) != 0 {
		t.Fatalf("problems: %v", plan.Problems)
	}
	if len(plan.Groups) != 2 {
		t.Fatalf("groups = %d, want 2 ($1.50 off and $3 off)", len(plan.Groups))
	}
	if g := plan.Groups[0]; g.OffCents != 150 || len(g.Beers) != 2 {
		t.Errorf("first group = %+v, want $1.50 off two beers", g)
	}
	if g := plan.Groups[1]; g.OffCents != 300 || g.Beers[0].VariationID != "V_Wicked Nebula_16oz" {
		t.Errorf("second group = %+v, want $3 off Wicked Nebula's pint", g)
	}

	objs := hhObjects(weekdayHH(), plan, at("2026-10-12 00:00"))
	if len(objs) != 1+3*2 {
		t.Fatalf("objects = %d, want a time period plus three per group", len(objs))
	}
	rule := objs[3]["pricing_rule_data"].(map[string]any)
	if rule["discount_id"] != "#hh-discount-0" || rule["match_products_id"] != "#hh-set-0" {
		t.Errorf("rule does not tie its own discount and set: %v", rule)
	}
}

func TestPlanRefusesWhatItCannotMatch(t *testing.T) {
	items := []square.CatalogItem{
		catalogItem("Light Lift", "", map[string]int{"16oz": 650}), // near miss
		catalogItem("Mars Water", "", map[string]int{"9oz": 400}),  // no pint
		catalogItem("Mr. Radar", "Wicked Nebula", map[string]int{"16oz": 800}),
	}
	plan := planHappyHour(weekdayHH(), items)
	if len(plan.Problems) != 2 {
		t.Fatalf("problems = %v, want the near miss and the missing pint", plan.Problems)
	}
	if !strings.Contains(plan.Problems[0], "closest: Light Lift") {
		t.Errorf("near miss should suggest the catalog name: %s", plan.Problems[0])
	}
	// A kitchen name is a match, the same rule the flight sync uses.
	if len(plan.Groups) != 1 || plan.Groups[0].Beers[0].ItemName != "Mr. Radar" {
		t.Errorf("kitchen name should match: %+v", plan.Groups)
	}
}

func TestEvent(t *testing.T) {
	h := weekdayHH()
	h.Start, h.End = "15:30", "17:00"
	got := hhEvent(h, at("2026-10-12 00:00"))
	want := "DTSTART:20261012T153000\nDURATION:PT1H30M\nRRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"
	if got != want {
		t.Errorf("event =\n%s\nwant\n%s", got, want)
	}
	// Starting on a Saturday rolls DTSTART to the Monday, so it is itself an
	// occurrence.
	if d := firstDay(h, at("2026-10-10 00:00")); d.Format(time.DateOnly) != "2026-10-12" {
		t.Errorf("firstDay = %s, want the following Monday", d.Format(time.DateOnly))
	}
}

func TestApplyHappyHourMarksOnlyThePour(t *testing.T) {
	b := &Board{Taps: []Tap{
		{Section: "Lagers", Name: "Light Lift Lager", Price: "6.50"},
		{Section: "IPAs", Name: "Wicked Nebula", Price: "8", Size: "10oz"},
		{Section: "IPAs", Name: "Galactic Orbit", Price: "8"},
	}}
	h := weekdayHH()
	banner := applyHappyHour(b, &h, at("2026-10-12 15:30"), denver)
	if banner != "Happy hour · till 5pm" {
		t.Errorf("banner = %q", banner)
	}
	if b.Taps[0].HappyPrice != "5" {
		t.Errorf("Light Lift should show $5, got %q", b.Taps[0].HappyPrice)
	}
	if b.Taps[1].HappyPrice != "" {
		t.Error("a 10oz pour is not the $5 pint and must not be marked")
	}
	if b.Taps[2].HappyPrice != "" {
		t.Error("a beer not on happy hour must not be marked")
	}

	html, err := Render(b, nil, "v")
	if err != nil {
		t.Fatal(err)
	}
	b.HappyBanner = banner
	html2, err := Render(b, nil, "v")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, `class="head-tag head-hh"`) || !strings.Contains(html2, "Happy hour · till 5pm") {
		t.Error("banner should render only when set")
	}
	if !strings.Contains(html2, `<p class="tap-was">6.50</p>`) {
		t.Error("the regular price should render struck beside the happy price")
	}
}

func TestHappyStampFlipsWithTheClock(t *testing.T) {
	h := weekdayHH()
	before := happyStamp(&h, at("2026-10-12 14:59"), denver)
	during := happyStamp(&h, at("2026-10-12 15:00"), denver)
	if before == during {
		t.Error("the version stamp must change when happy hour starts, or the wall never flips")
	}
	if happyStamp(nil, at("2026-10-12 15:00"), denver) != "" {
		t.Error("no happy hour, no stamp")
	}
}

func TestParseHappyHourArgs(t *testing.T) {
	args, err := parseHappyHourArgs([]byte(`{"price": 5, "beers": "Mars Water, Wicked Nebula", "days": ["MON","Tue"]}`))
	if err != nil {
		t.Fatal(err)
	}
	h, err := args.merge(weekdayHH())
	if err != nil {
		t.Fatal(err)
	}
	if h.PriceCents != 500 || len(h.Beers) != 2 || h.Days[0] != "mon" || h.Start != "15:00" {
		t.Errorf("merged = %+v", h)
	}
	if _, err := parsePrice("$5.50"); err != nil {
		t.Error(err)
	}
	if c, _ := parsePrice("5.5"); c != 550 {
		t.Errorf("5.5 = %d cents", c)
	}
}
