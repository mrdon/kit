package posters

import (
	"os"
	"testing"
)

func loadGravityTokens(t *testing.T) *guideTokens {
	t.Helper()
	raw, err := os.ReadFile("testdata/gravity-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	block := guideJSONBlock(string(raw))
	if block == "" {
		t.Fatal("no json block found in the test guide")
	}
	g, err := parseGuideTokens(block)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// The derivation must reproduce the prototype's hand-written brand.ts from
// the guide's tokens alone: grounds are paper and ink (forest is not a
// ground), text/label/rule per ground, accents amber and ember, and the
// logo variant per ground from the guide's on_* keys.
func TestDeriveBrand_Gravity(t *testing.T) {
	b, problems := deriveBrand(loadGravityTokens(t), "h1")
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(b.Grounds) != 2 {
		t.Fatalf("grounds = %v, want paper and ink", b.Grounds)
	}
	paper := b.Grounds["paper"]
	if paper.Text != "ink" || paper.Label != "forest" || paper.Rule != "stone" || paper.Logo != "color" {
		t.Errorf("paper ground = %+v", paper)
	}
	ink := b.Grounds["ink"]
	if ink.Text != "paper" || ink.Label != "paper" || ink.Rule != "forest" || ink.Logo != "white" {
		t.Errorf("ink ground = %+v", ink)
	}
	if b.Accents["amber"].Text != "ink" || b.Accents["ember"].Text != "paper" {
		t.Errorf("accents = %+v", b.Accents)
	}
	if b.AccentRules.MaxElements != 1 || b.AccentRules.Together {
		t.Errorf("accent rules = %+v", b.AccentRules)
	}
	if b.Fonts.Display.Family != "Archivo Narrow" || b.Fonts.Display.Advance != 0.47 {
		t.Errorf("display font = %+v", b.Fonts.Display)
	}
	if got := b.Fonts.Text.Weights; len(got) != 2 || got[0] != 400 || got[1] != 600 {
		t.Errorf("text weights = %v", got)
	}
	if b.PortraitFormat != "ig_portrait" {
		t.Errorf("portrait format = %q", b.PortraitFormat)
	}
	story := b.Formats["story"]
	if story.Safe.Top != 100 || story.Safe.Bottom != 250 || story.Safe.Left != 60 {
		t.Errorf("story safe = %+v", story.Safe)
	}
	if len(b.Pairs) != 11 {
		t.Errorf("pairs = %d", len(b.Pairs))
	}
}

func TestDeriveBrand_ReportsMissingPieces(t *testing.T) {
	g := loadGravityTokens(t)
	g.ApprovedPairs = nil
	_, problems := deriveBrand(g, "h")
	if len(problems) == 0 {
		t.Fatal("expected problems with no pairings")
	}
	g = loadGravityTokens(t)
	for k, c := range g.Color {
		c.Role = "a color"
		g.Color[k] = c
	}
	_, problems = deriveBrand(g, "h")
	if len(problems) == 0 {
		t.Fatal("expected problems with no ground or accent roles")
	}
}

func TestGuideJSONBlock_AbsentInProse(t *testing.T) {
	if got := guideJSONBlock("# Guide\n\nOur colours are warm.\n\n```json\n{\"nope\": true}\n```\n"); got != "" {
		t.Errorf("found a block in prose: %q", got)
	}
}

func TestContrast(t *testing.T) {
	if r := contrast("#171A14", "#F6F2E7"); r < 15.5 || r > 16 {
		t.Errorf("ink/paper contrast = %.2f, want ~15.7", r)
	}
}
