package menu

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParsePanelsAcceptsEveryKind(t *testing.T) {
	raw := `[
	  {"kind":"agenda","label":"Every week","events":[{"when":"Every Wed","time":"6:30 pm","title":"Trivia","note":"Teams up to six"}]},
	  {"kind":"poster","label":"Don't miss","image":"asset:anniversary","alt":"A poster"},
	  {"kind":"cta","label":"Book","headline":"Your party, here.","body":"Text","contact":["a@b.c"]}
	]`
	panels, err := ParsePanels([]byte(raw))
	if err != nil {
		t.Fatalf("ParsePanels: %v", err)
	}
	if len(panels) != 3 {
		t.Fatalf("got %d panels, want 3", len(panels))
	}
	if panels[0].Events[0].Title != "Trivia" {
		t.Errorf("agenda event title = %q", panels[0].Events[0].Title)
	}
}

// An empty rail is a legitimate thing to ask for -- it is how you remove the
// last panel -- so it must not be mistaken for a mistake.
func TestParsePanelsAcceptsEmptyArray(t *testing.T) {
	panels, err := ParsePanels([]byte(`[]`))
	if err != nil {
		t.Fatalf("ParsePanels: %v", err)
	}
	if len(panels) != 0 {
		t.Fatalf("got %d panels, want 0", len(panels))
	}
}

func TestParsePanelsRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"unknown field", `[{"kind":"cta","headline":"Hi","bodyy":"typo"}]`, "bodyy"},
		{"unknown kind", `[{"kind":"banner","label":"x"}]`, "unknown kind"},
		{"cta with no headline", `[{"kind":"cta","body":"text"}]`, "no headline"},
		{"agenda with no events", `[{"kind":"agenda","label":"This week"}]`, "no events"},
		{"poster with no image", `[{"kind":"poster","label":"x"}]`, "no image"},
		{"image that is neither an asset nor a data URI", `[{"kind":"poster","image":"https://example.com/a.png"}]`, "must be"},
		{"not an array", `{"kind":"cta","headline":"Hi"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePanels([]byte(tc.raw))
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !errors.Is(err, ErrPayloadInvalid) {
				t.Errorf("error should wrap ErrPayloadInvalid, got %v", err)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

// The panel index in a validation message is 1-based and points at the panel
// the caller wrote, which is the only way to find it in an array of four.
func TestParsePanelsNamesTheOffendingPanel(t *testing.T) {
	raw := `[{"kind":"cta","headline":"Fine"},{"kind":"cta","body":"no headline"}]`
	_, err := ParsePanels([]byte(raw))
	if err == nil {
		t.Fatal("want an error, got none")
	}
	if !strings.Contains(err.Error(), "panel 2") {
		t.Errorf("error %q should name panel 2", err)
	}
}

func TestPanelsJSON(t *testing.T) {
	arr := `[{"kind":"cta","headline":"Hi"}]`
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"array as sent by an MCP client", arr, arr},
		{"array wrapped in a JSON string as sent by an agent", mustQuote(t, arr), arr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := panelsJSON(json.RawMessage(tc.in))
			if err != nil {
				t.Fatalf("panelsJSON: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPanelsJSONRequiresAValue(t *testing.T) {
	for _, in := range []string{"", "   ", "null"} {
		if _, err := panelsJSON(json.RawMessage(in)); err == nil {
			t.Errorf("panelsJSON(%q) should fail", in)
		}
	}
}

func TestPanelSummary(t *testing.T) {
	cases := []struct {
		name   string
		panels []Panel
		want   string
	}{
		{"none", nil, "no panels, so the rail is empty"},
		{"one", []Panel{{Kind: PanelCTA}}, "1 panel (1 cta)"},
		{"two kinds", []Panel{{Kind: PanelAgenda}, {Kind: PanelCTA}}, "2 panels (1 agenda and 1 cta)"},
		{
			"repeats counted, first-seen order",
			[]Panel{{Kind: PanelCTA}, {Kind: PanelAgenda}, {Kind: PanelCTA}, {Kind: PanelPoster}},
			"4 panels (2 cta, 1 agenda and 1 poster)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := panelSummary(tc.panels); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Round-tripping is what savePanels stores, so an optional key the caller
// omitted must not come back as an empty string in the document.
func TestParsedPanelsRoundTripWithoutEmptyKeys(t *testing.T) {
	panels, err := ParsePanels([]byte(`[{"kind":"cta","headline":"Hi"}]`))
	if err != nil {
		t.Fatalf("ParsePanels: %v", err)
	}
	out, err := json.Marshal(panels)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, absent := range []string{"image", "alt", "body", "contact", "events"} {
		if strings.Contains(string(out), `"`+absent+`"`) {
			t.Errorf("round-tripped panel should not carry %q: %s", absent, out)
		}
	}
}

func mustQuote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(b)
}

func TestDescribePanelsIsRoundTrippable(t *testing.T) {
	in := []Panel{
		{Kind: PanelCTA, Label: "Holiday parties", Headline: "Host your holiday party here.", Body: "Text", Contact: []string{"a@b.c"}},
		{Kind: PanelPoster, Label: "Don't miss", Image: "asset:oktoberfest", Alt: "A poster"},
	}
	out := describePanels(in)
	if !strings.Contains(out, "set_menu_panels") {
		t.Errorf("description should name the tool that accepts it: %s", out)
	}
	// The whole point: what it prints must parse straight back.
	start := strings.Index(out, "[")
	if start < 0 {
		t.Fatalf("no JSON array in %s", out)
	}
	back, err := ParsePanels([]byte(out[start:]))
	if err != nil {
		t.Fatalf("describePanels output did not round-trip: %v\n%s", err, out)
	}
	if len(back) != len(in) {
		t.Fatalf("got %d panels back, want %d", len(back), len(in))
	}
	if back[0].Headline != in[0].Headline || back[1].Image != in[1].Image {
		t.Errorf("round-trip lost content: %+v", back)
	}
}

func TestDescribePanelsEmptyRail(t *testing.T) {
	if got := describePanels(nil); !strings.Contains(got, "empty") {
		t.Errorf("got %q, want it to say the rail is empty", got)
	}
}
