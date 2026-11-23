package engine

import (
	"strings"
	"testing"
)

// tweakGame is a game on a plain 20-spot track with frogs a (spot 15,
// leading), b (spot 5) and c (spot 15, under a).
func tweakGame(t *testing.T, edit func(*Rules)) *Game {
	t.Helper()
	cfg := Default()
	cfg.Levels = SingleLevel(20)
	edit(&cfg.Rules)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	g := NewGame(cfg, 1)
	g.resetStacks()
	g.stacks[15] = []string{"c", "a"}
	g.stacks[5] = []string{"b"}
	return g
}

func TestTweaksOffByDefault(t *testing.T) {
	g := tweakGame(t, func(*Rules) {})
	r := g.cfg.Rules
	for _, id := range []string{"a", "b", "c"} {
		if g.slipChance(id) != r.SlipChance || g.hopRange(id, false) != r.MaxRoll || g.hopRange(id, true) != r.MaxRoll {
			t.Errorf("%s: knobs change play at their defaults", id)
		}
		if g.FlickBandFor(id) != r.FlickBand || g.trailBonus(id) != 0 {
			t.Errorf("%s: band %g, want %g", id, g.FlickBandFor(id), r.FlickBand)
		}
	}
}

func TestTweaks(t *testing.T) {
	g := tweakGame(t, func(r *Rules) {
		r.BackMaxRoll, r.LeaderMaxRoll, r.LeaderSlip, r.CatchUpBand, r.TrailBonus = 2, 2, 0.15, 2, 1
	})
	if g.trailBonus("b") != 1 || g.trailBonus("a") != 0 || g.trailBonus("c") != 0 {
		t.Errorf("trail bonus: b %d, a %d, c %d; want only last-place b to get 1", g.trailBonus("b"), g.trailBonus("a"), g.trailBonus("c"))
	}
	if got := g.slipChance("a"); got != 0.15 {
		t.Errorf("leader slip %g, want 0.15", got)
	}
	if got := g.slipChance("c"); got != 0.08 {
		t.Errorf("frog under the leader slips at %g, want 0.08", got)
	}
	g.backward["a"] = true
	if got := g.slipChance("a"); got != 0.5 {
		t.Errorf("a backward leader turns back at %g, want TurnBack 0.5", got)
	}
	if g.hopRange("a", false) != 2 || g.hopRange("b", false) != 3 || g.hopRange("b", true) != 2 {
		t.Errorf("hop ranges: leader %d, other %d, backward %d; want 2, 3, 2", g.hopRange("a", false), g.hopRange("b", false), g.hopRange("b", true))
	}
	// b is 10 of 20 spots behind: half a track, so 1 + 2*0.5 = twice the band
	if got, want := g.FlickBandFor("b"), 2*BandWidth; got < want-1e-9 || got > want+1e-9 {
		t.Errorf("b's band %g, want %g", got, want)
	}
	if g.FlickBandFor("a") != BandWidth || g.FlickBandFor("c") != BandWidth {
		t.Error("frogs on the leading spot get a wider band")
	}
	if v := g.View(RolePlayer, "b"); v.FlickBand != g.FlickBandFor("b") {
		t.Errorf("b's phone shows band %g, want %g", v.FlickBand, g.FlickBandFor("b"))
	}
}

func TestTweaksBandCapped(t *testing.T) {
	g := tweakGame(t, func(r *Rules) { r.CatchUpBand = 20 })
	if got := g.FlickBandFor("b"); got != MaxCatchUpBand {
		t.Errorf("band %g, want the cap %g", got, MaxCatchUpBand)
	}
}

func TestTweaksValidate(t *testing.T) {
	for name, edit := range map[string]func(*Rules){
		"back_max_roll":   func(r *Rules) { r.BackMaxRoll = 4 },
		"leader_max_roll": func(r *Rules) { r.LeaderMaxRoll = -1 },
		"leader_slip":     func(r *Rules) { r.LeaderSlip = 1.5 },
		"catch_up_band":   func(r *Rules) { r.CatchUpBand = -1 },
		"trail_bonus":     func(r *Rules) { r.TrailBonus = 4 },
	} {
		cfg := Default()
		edit(&cfg.Rules)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: got %v, want an error naming it", name, err)
		}
	}
}

func TestTweaksYAMLOmitted(t *testing.T) {
	y, err := Default().YAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"back_max_roll", "leader_max_roll", "leader_slip", "catch_up_band", "trail_bonus"} {
		if strings.Contains(string(y), k) {
			t.Errorf("default config writes %s; knobs that are off should stay out of level files", k)
		}
	}
	cfg, err := ParseConfig([]byte("rules:\n  back_max_roll: 2\n  leader_slip: 0.12\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules.BackMaxRoll != 2 || cfg.Rules.LeaderSlip != 0.12 {
		t.Errorf("parsed %+v", cfg.Rules)
	}
}
