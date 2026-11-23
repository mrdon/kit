package engine

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestConfigYAMLRoundTrip(t *testing.T) {
	c := Default()
	c.Levels[1].Track.Specials = []Special{{At: 3, Kind: SpotLaunch, To: 8}, {At: 14, Kind: SpotChute, To: 9}}
	c.Levels[2].Track.Random = &RandomSpecials{Launches: 2, Chutes: 1}
	c.Rules.FlickTimeout = 6500 * time.Millisecond
	data, err := c.YAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "flick_timeout: 6.5s") || !strings.Contains(string(data), "kind: launch") {
		t.Fatalf("want readable YAML, got:\n%s", data)
	}
	back, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if !reflect.DeepEqual(back, c) {
		t.Fatalf("round trip changed the config:\nwant %+v\ngot  %+v", c, back)
	}
}

func TestParseConfigKeepsDefaults(t *testing.T) {
	c, err := ParseConfig([]byte("rules:\n  rounds: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.Rules.Rounds != 2 || c.Rules.SlipChance != d.Rules.SlipChance || !reflect.DeepEqual(c.Levels, d.Levels) {
		t.Fatalf("left-out fields should keep their defaults: %+v", c)
	}
	if c, err := ParseConfig(nil); err != nil || !reflect.DeepEqual(c, d) {
		t.Fatalf("an empty file is the default config: %v", err)
	}
}

func TestParseConfigRejectsUnknownFields(t *testing.T) {
	for _, y := range []string{
		"rules:\n  rouns: 2\n",
		"levels:\n  - layout: straight\n    track: {length: 14, specails: []}\n",
		"colour: red\n",
	} {
		if _, err := ParseConfig([]byte(y)); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("%q: want an unknown-field error, got %v", y, err)
		}
	}
}

func TestValidateErrors(t *testing.T) {
	track := func(sp ...Special) GameConfig {
		c := Default()
		c.Levels = []Level{{Layout: LayoutSwitchback, Track: Track{Length: 20, Specials: sp}}}
		return c
	}
	for _, tc := range []struct {
		name string
		cfg  GameConfig
		want string
	}{
		{"unknown kind", track(Special{At: 5, Kind: "trampoline", To: 9}), `unknown kind "trampoline"`},
		{"off the track", track(Special{At: 25, Kind: SpotChute, To: 9}), "off the track"},
		{"on the start", track(Special{At: 0, Kind: SpotLaunch, To: 9}), "is the start"},
		{"on the finish", track(Special{At: 20, Kind: SpotChute, To: 9}), "is the finish"},
		{"to the finish", track(Special{At: 5, Kind: SpotLaunch, To: 20}), "to 20 is out of range"},
		{"to before start", track(Special{At: 5, Kind: SpotChute, To: -1}), "out of range"},
		{"backwards launch", track(Special{At: 9, Kind: SpotLaunch, To: 4}), "a launch must go forward"},
		{"forwards chute", track(Special{At: 9, Kind: SpotChute, To: 12}), "a chute must go back"},
		{"two on a spot", track(Special{At: 9, Kind: SpotChute, To: 4}, Special{At: 9, Kind: SpotLaunch, To: 14}), "one special per spot"},
		{"chain", track(Special{At: 4, Kind: SpotLaunch, To: 9}, Special{At: 9, Kind: SpotChute, To: 2}), "can't chain"},
		{"both kinds of specials", func() GameConfig {
			c := track(Special{At: 4, Kind: SpotLaunch, To: 9})
			c.Levels[0].Track.Random = &RandomSpecials{Launches: 1}
			return c
		}(), "both specials and random"},
		{"too many random", func() GameConfig {
			c := track()
			c.Levels[0].Track.Random = &RandomSpecials{Launches: 5, Chutes: 5}
			return c
		}(), "don't fit"},
		{"unknown layout", func() GameConfig { c := track(); c.Levels[0].Layout = "spiral"; return c }(), `layout: unknown "spiral"`},
		{"short track", func() GameConfig { c := track(); c.Levels[0].Track.Length = 3; return c }(), "track.length: 3"},
		{"no levels", func() GameConfig { c := Default(); c.Levels = nil; return c }(), "levels: empty"},
		{"no rounds", func() GameConfig { c := Default(); c.Rules.Rounds = 0; return c }(), "rules.rounds"},
		{"bare-number timeout", func() GameConfig { c := Default(); c.Rules.FlickTimeout = 8; return c }(), "write it like 8s"},
	} {
		err := tc.cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		}
	}
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
	if _, err := ParseConfig([]byte("levels:\n  - layout: straight\n    track: {length: 14, specials: [{at: 3, kind: slide, to: 1}]}\n")); err == nil {
		t.Fatal("ParseConfig must validate")
	}
}

// specialGame: a one-level race with the given specials, p-00 about to hop
// exactly one coaster (no slips) from `from`.
func specialGame(t *testing.T, from int, back bool, sp ...Special) *Game {
	t.Helper()
	cfg := GameConfig{Rules: Rules{MinFrogs: 3, Rounds: 1, MaxRoll: 1, SkillCap: 1, FlickTimeout: time.Second},
		Levels: []Level{{Layout: LayoutSwitchback, Track: Track{Length: 30, Specials: sp}}}}
	g := crowd(t, cfg, 1, 1)
	g.Apply(Command{Type: CmdHost}, t0)
	g.cfg.Rules.SlipChance, g.cfg.Rules.TurnBack = 0, 0.0000001
	g.resetStacks()
	g.stacks[from] = []string{"p-00"}
	g.backward = map[string]bool{"p-00": back}
	g.order, g.hopIndex, g.sub = []string{"p-00", "bot-0", "bot-1", "bot-2"}, 0, SubFlick
	g.deadline = t0.Add(time.Second)
	return g
}

func TestLaunchCarriesStackOntoDestination(t *testing.T) {
	g := specialGame(t, 5, false, Special{At: 6, Kind: SpotLaunch, To: 12})
	g.stacks[5] = []string{"bot-0", "p-00", "bot-1"} // p-00 carries bot-1
	g.stacks[12] = []string{"bot-2"}
	g.Apply(Command{Type: CmdFlick, Player: "p-00"}, t0)
	h := g.lastHop
	if h.Special == nil || *h.Special != (Special{At: 6, Kind: SpotLaunch, To: 12}) || h.Landed != 6 || h.To != 12 || h.From != 5 {
		t.Fatalf("want a launch from 6 to 12, got %+v (special %+v)", h, h.Special)
	}
	if got := g.stacks[12]; !slices.Equal(got, []string{"bot-2", "p-00", "bot-1"}) {
		t.Fatalf("p-00 and its rider should land on top of bot-2, got %v", got)
	}
	if len(g.stacks[6]) != 0 || !slices.Equal(g.stacks[5], []string{"bot-0"}) {
		t.Fatalf("nobody stays on the pad, the bottom frog stays put: 5=%v 6=%v", g.stacks[5], g.stacks[6])
	}
	if h.LandedOn != "bot-2" || h.Under != "" {
		t.Fatalf("landedOn should be the frog at the destination, got %+v", h)
	}
	if line := g.events[len(g.events)-1].Text; !strings.Contains(line, "launch pad") {
		t.Fatalf("announcer should call the launch: %q", line)
	}
	s := g.Snapshot()
	if s.LastHop.Special == nil || s.LastHop.Landed != 6 || s.Track.Length != 30 || len(s.Track.Specials) != 1 || s.TrackLen != 30 {
		t.Fatalf("snapshot should carry the track and the special: %+v", s)
	}
}

func TestChuteDropsStack(t *testing.T) {
	g := specialGame(t, 15, false, Special{At: 16, Kind: SpotChute, To: 9})
	g.stacks[15] = []string{"p-00", "bot-1"}
	g.Apply(Command{Type: CmdFlick, Player: "p-00"}, t0)
	if h := g.lastHop; h.Special == nil || h.Special.Kind != SpotChute || h.To != 9 || h.Landed != 16 {
		t.Fatalf("want a chute 16 -> 9, got %+v", h)
	}
	if got := g.stacks[9]; !slices.Equal(got, []string{"p-00", "bot-1"}) {
		t.Fatalf("p-00 and rider should be on 9, got %v", got)
	}
	if line := g.events[len(g.events)-1].Text; !strings.Contains(line, "chute") {
		t.Fatalf("announcer should call the chute: %q", line)
	}
}

// A wrong-way frog landing on a chute still takes it, and lands on top of the
// stack at the bottom (not under it, as a plain backward hop would).
func TestBackwardHopOntoChute(t *testing.T) {
	g := specialGame(t, 17, true, Special{At: 16, Kind: SpotChute, To: 9})
	g.stacks[9] = []string{"bot-0"}
	g.Apply(Command{Type: CmdFlick, Player: "p-00"}, t0)
	h := g.lastHop
	if !h.Back || h.Special == nil || h.Landed != 16 || h.To != 9 {
		t.Fatalf("want a backward hop onto the chute, got %+v", h)
	}
	if got := g.stacks[9]; !slices.Equal(got, []string{"bot-0", "p-00"}) {
		t.Fatalf("should land on top of bot-0, got %v", got)
	}
}

func TestPlainHopHasNoSpecial(t *testing.T) {
	g := specialGame(t, 5, false, Special{At: 9, Kind: SpotLaunch, To: 12})
	g.Apply(Command{Type: CmdFlick, Player: "p-00"}, t0)
	if h := g.lastHop; h.Special != nil || h.Landed != 6 || h.To != 6 {
		t.Fatalf("plain hop: %+v", h)
	}
	data, _ := json.Marshal(g.Snapshot())
	for _, want := range []string{`"special":null`, `"landed":6`, `"track":{"length":30,"specials":[{"at":9,"kind":"launch","to":12}]}`, `"layout":"switchback"`, `"theme":""`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("snapshot JSON missing %s", want)
		}
	}
}

func TestNoSpecialsIsEmptyList(t *testing.T) {
	data, _ := json.Marshal(NewGame(Default(), 1).Snapshot())
	if !strings.Contains(string(data), `"track":{"length":14,"specials":[]}`) || !strings.Contains(string(data), `"layout":"straight","theme":"bar"`) {
		t.Fatalf("lobby shows race 1's level with an empty specials list: %s", data)
	}
}

func TestRandomTrackAlwaysValid(t *testing.T) {
	for seed := range uint64(2000) {
		rng := rand.New(rand.NewPCG(seed, 9))
		n := 14 + int(seed%7)
		l, c := int(seed%3), int(seed/3%3)
		tr, err := RandomTrack(rng, n, l, c)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if err := tr.Validate(); err != nil || len(tr.Specials) != l+c {
			t.Fatalf("seed %d: invalid track %+v: %v", seed, tr, err)
		}
		for _, s := range tr.Specials {
			if (s.Kind == SpotLaunch && s.At > n/2) || (s.Kind == SpotChute && s.At < n/2) {
				t.Fatalf("seed %d: launches go in the first half, chutes in the second: %+v", seed, tr)
			}
		}
	}
	if _, err := RandomTrack(rand.New(rand.NewPCG(1, 1)), 14, 4, 4); err == nil {
		t.Fatal("8 specials can't fit on 14 coasters")
	}
}

// Random specials are placed at game start from the seed: same seed, same track.
func TestRandomSpecialsResolvedFromSeed(t *testing.T) {
	cfg := Default().WithRandomSpecials(2, 2)
	a, b, c := NewGame(cfg, 7).Config(), NewGame(cfg, 7).Config(), NewGame(cfg, 8).Config()
	if !reflect.DeepEqual(a, b) || reflect.DeepEqual(a, c) {
		t.Fatal("random specials should depend only on the seed")
	}
	for _, l := range a.Levels {
		if l.Track.Random != nil || len(l.Track.Specials) != 4 {
			t.Fatalf("want 4 concrete specials, got %+v", l.Track)
		}
	}
}

func TestGenerateCup(t *testing.T) {
	for seed := range uint64(200) {
		rng := rand.New(rand.NewPCG(seed, 3))
		c, err := Generate(rng, Preferences{Rounds: 5}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Levels) != 5 || c.Rules.Rounds != 5 || c.Levels[0].Layout != LayoutStraight {
			t.Fatalf("seed %d: want 5 races opening with a straight, got %+v", seed, c.Levels)
		}
		for i := 1; i < len(c.Levels); i++ {
			if c.Levels[i].Name == c.Levels[i-1].Name {
				t.Fatalf("seed %d: %s twice in a row", seed, c.Levels[i].Name)
			}
		}
		for _, l := range c.Levels {
			if len(l.Track.Specials) != 0 {
				t.Fatalf("specials are off by default: %+v", l)
			}
		}
	}
	c, err := Generate(rand.New(rand.NewPCG(1, 1)), Preferences{Layouts: []Layout{LayoutSwitchback}, Themes: []string{"lake"}, Launches: 2, Chutes: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range c.Levels {
		want := 4
		if i == 0 {
			want = 0 // the opener never has specials
		}
		if l.Layout != LayoutSwitchback || l.Theme != "lake" || len(l.Track.Specials) != want {
			t.Fatalf("preferences not honoured: %+v", l)
		}
	}
	if _, err := Generate(rand.New(rand.NewPCG(1, 1)), Preferences{Layouts: []Layout{"spiral"}}, nil); err == nil {
		t.Fatal("unknown layout in preferences should error")
	}
}

func TestEditCup(t *testing.T) {
	c := Default()
	if err := c.EditLevel(1, func(l *Level) { l.Track.Specials = append(l.Track.Specials, Special{At: 4, Kind: SpotChute, To: 9}) }); err == nil {
		t.Fatal("an invalid edit must error")
	}
	if len(c.Levels[1].Track.Specials) != 0 {
		t.Fatal("an invalid edit must not change the config")
	}
	if err := c.EditLevel(1, func(l *Level) { l.Theme, l.Track.Length = "lake", 18 }); err != nil || c.Levels[1].Track.Length != 18 {
		t.Fatalf("edit: %v", err)
	}
	old := c.Levels[2]
	if err := c.RerollLevel(2, rand.New(rand.NewPCG(1, 1)), Preferences{}, nil); err != nil || reflect.DeepEqual(c.Levels[2], old) {
		t.Fatalf("reroll should swap in a different level: %v %+v", err, c.Levels[2])
	}
	if err := c.SetLevel(3, BuiltinLevels()[0]); err != nil || len(c.Levels) != 4 {
		t.Fatalf("setting one past the end adds a race: %v", err)
	}
	if err := c.SetLevel(9, BuiltinLevels()[0]); err == nil {
		t.Fatal("no race 10")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Round N races level N (cycling), and the track length follows the level.
func TestRoundsUseTheirLevel(t *testing.T) {
	cfg := Default()
	cfg.Rules.Rounds = 4
	g := crowd(t, cfg, 2, 1)
	if g.Snapshot().Track.Length != 14 {
		t.Fatal("the lobby shows race 1's track")
	}
	now := t0
	g.Apply(Command{Type: CmdHost}, now)
	var lens []int
	var layouts []Layout
	for g.Phase() != PhaseFinal {
		s := g.Snapshot()
		if len(lens) < g.round {
			lens, layouts = append(lens, len(s.Stacks)-1), append(layouts, s.Layout)
		}
		now = runRace(t, g, now)
		moveOn(t, g, now)
	}
	if !slices.Equal(lens, []int{14, 20, 20, 14}) || layouts[0] != LayoutStraight || layouts[1] != LayoutSwitchback {
		t.Fatalf("rounds should race the cup in order and cycle: %v %v", lens, layouts)
	}
}

func TestWarmUpAvoidsSpecials(t *testing.T) {
	cfg := Default()
	cfg.Levels = []Level{{Layout: LayoutStraight, Track: Track{Length: 14, Specials: []Special{{At: 1, Kind: SpotLaunch, To: 5}, {At: 2, Kind: SpotLaunch, To: 6}}}}}
	for seed := range uint64(20) {
		g := crowd(t, cfg, 5, seed)
		g.Apply(Command{Type: CmdHost}, t0)
		if len(g.stacks[1])+len(g.stacks[2]) != 0 {
			t.Fatalf("no frog should start on a special: %v", g.stacks)
		}
	}
}

func TestCommandTypes(t *testing.T) {
	g := NewGame(Default(), 1)
	if _, err := g.Apply(Command{Type: "jump"}, t0); err == nil {
		t.Fatal("unknown command types must error")
	}
}

func TestFirstRaceIsShortPlainStraight(t *testing.T) {
	for seed := range uint64(50) {
		cfg, err := Generate(rand.New(rand.NewPCG(seed, 1)), DefaultPreferences(), nil)
		if err != nil {
			t.Fatal(err)
		}
		first := cfg.Levels[0]
		if first.Layout != LayoutStraight || len(first.Track.Specials) != 0 || first.Track.Random != nil {
			t.Fatalf("seed %d: first race %+v, want a plain straight", seed, first)
		}
		for _, l := range cfg.Levels[1:] {
			if len(l.Track.Specials) != 2 {
				t.Fatalf("seed %d: later race %s has %d specials, want 1 launch + 1 chute", seed, l.Name, len(l.Track.Specials))
			}
		}
	}
}
