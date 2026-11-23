package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// virtual clock start for tests
var t0 = time.Date(2026, 1, 1, 19, 0, 0, 0, time.UTC)

func TestSameSeedSameGame(t *testing.T) {
	play := func() string {
		g := NewGame(GameConfig{Rules: Rules{MinFrogs: 5, Rounds: 1, FlickTimeout: 8 * time.Second, SlipChance: 0.2}, Levels: SingleLevel(16)}, 42)
		now := t0
		g.Apply(Command{Type: CmdHost}, now)
		for g.Phase() == PhaseRacing && g.Sub() != SubRaceEnd {
			now = g.NextDeadline()
			g.Advance(now)
		}
		return fmt.Sprint(g.winner, g.turn, g.stacks)
	}
	if a, b := play(), play(); a != b {
		t.Fatalf("same seed diverged:\n%s\n%s", a, b)
	}
}

func TestHumanColourBumpsBot(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 5, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 1)
	botColour := g.players["bot-0"].Color
	g.Join("p-1", "Human", botColour, "none")
	seen := map[string]string{}
	for _, p := range g.players {
		if other, dup := seen[p.Color]; dup {
			t.Fatalf("%s and %s share colour %s", other, p.Name, p.Color)
		}
		seen[p.Color] = p.Name
	}
}

func TestTurnedAroundFrogKeepsGoingBackwards(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 3, Rounds: 1, FlickTimeout: time.Second, SlipChance: 1}, Levels: SingleLevel(30)}, 1)
	g.HostAdvance()
	// bot-0 on 20 carrying bot-1; bot-2 sitting on 17.
	g.resetStacks()
	g.stacks[20] = []string{"bot-0", "bot-1"}
	g.stacks[17] = []string{"bot-2"}
	g.order = []string{"bot-0"}
	g.hopIndex = 0
	g.doHop(false, 0, false) // slips (chance 1): turned around, goes backwards
	h := g.lastHop
	if !h.Back || !h.Turned || h.To >= 20 {
		t.Fatalf("expected a turning backwards hop, got %+v", h)
	}
	if got := g.stacks[h.To]; got[0] != "bot-0" || got[1] != "bot-1" {
		t.Fatalf("expected bot-0 at the bottom carrying bot-1 at %d, got %v", h.To, got)
	}
	if h.To == 17 && g.stacks[17][2] != "bot-2" {
		t.Fatalf("expected to slide under bot-2, got %v", g.stacks[17])
	}
	// With no more slips it keeps going backwards.
	g.cfg.Rules.SlipChance, g.cfg.Rules.TurnBack = 0, 0
	from := h.To
	g.doHop(false, 0, false)
	if h2 := g.lastHop; !h2.Back || h2.To >= from {
		t.Fatalf("expected to keep hopping backwards from %d, got %+v", from, h2)
	}
}

func TestBackwardFrogBonksStart(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 1, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 1)
	g.HostAdvance()
	g.resetStacks()
	g.stacks[0] = []string{"bot-0"}
	g.backward["bot-0"] = true
	g.order = []string{"bot-0"}
	g.hopIndex = 0
	g.doHop(false, 0, false)
	if h := g.lastHop; h.Back || h.To <= 0 || g.backward["bot-0"] {
		t.Fatalf("expected to bonk the start and hop forward, got %+v", h)
	}
}

// Regression: the announcer said "X takes the lead" when X already led,
// because the old leader was computed after lifting the hopping frogs off.
func TestNoFalseLeadChange(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 3, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 3)
	g.Apply(Command{Type: CmdHost}, t0)
	g.resetStacks()
	g.stacks[5] = []string{"bot-0", "bot-1"} // bot-1 leads, riding bot-0
	g.stacks[1] = []string{"bot-2"}
	g.order = []string{"bot-0"}
	g.hopIndex = 0
	g.sub = SubFlick
	g.doHop(false, 0, false)
	if last := g.events[len(g.events)-1].Text; strings.Contains(strings.ToLower(last), "takes the lead") {
		t.Fatalf("leader didn't change but announcer said: %q", last)
	}
}

// A lead change is only called once the new leader is past halfway.
func TestLeadCallOnlyInSecondHalf(t *testing.T) {
	for _, c := range []struct {
		at   int
		call bool
	}{{2, false}, {7, false}, {8, true}, {12, true}} {
		g := NewGame(GameConfig{Rules: Rules{MinFrogs: 3, Rounds: 1, FlickTimeout: time.Second, MaxRoll: 1, SlipChance: 1e-9}, Levels: SingleLevel(16)}, 3)
		g.Apply(Command{Type: CmdHost}, t0)
		g.resetStacks()
		g.stacks[c.at] = []string{"bot-0"}
		g.stacks[c.at+1] = []string{"bot-1"} // bot-0 hops 1 onto bot-1 and leads at at+1
		g.stacks[0] = []string{"bot-2"}
		g.order, g.hopIndex, g.sub = []string{"bot-0"}, 0, SubFlick
		g.doHop(false, 0, false)
		last := g.events[len(g.events)-1].Text
		if g.leader() != "bot-0" || strings.Contains(last, "lead") != c.call {
			t.Fatalf("lead taken at %d of 16: want call %v, got %q", c.at+1, c.call, last)
		}
	}
}

func TestTwoPeopleNeverShareAColour(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 2, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 1)
	g.Apply(Command{Type: CmdJoin, Player: "a", Name: "A", Color: "#9b59b6"}, t0)
	g.Apply(Command{Type: CmdJoin, Player: "b", Name: "B", Color: "#9b59b6"}, t0)
	if g.players["a"].Color == g.players["b"].Color {
		t.Fatalf("both people got %s", g.players["a"].Color)
	}
	if g.players["a"].Color != "#9b59b6" {
		t.Fatalf("first person should keep their colour, got %s", g.players["a"].Color)
	}
}

func TestBotsFillToMinFrogs(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 7, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 1)
	count := func() (people, bots int) {
		for _, p := range g.players {
			if p.Bot {
				bots++
			} else if p.Connected {
				people++
			}
		}
		return
	}
	if _, b := count(); b != 7 {
		t.Fatalf("empty lobby should have 7 bots, got %d", b)
	}
	g.Apply(Command{Type: CmdJoin, Player: "a", Name: "A", Color: "#2ecc71"}, t0)
	g.Apply(Command{Type: CmdJoin, Player: "b", Name: "B", Color: "#3498db"}, t0)
	if p, b := count(); p != 2 || b != 5 {
		t.Fatalf("2 people should get 5 bots, got %d people %d bots", p, b)
	}
	g.Apply(Command{Type: CmdHost}, t0) // race starts: field is frozen
	g.Apply(Command{Type: CmdJoin, Player: "c", Name: "C", Color: "#f1c40f"}, t0)
	if _, b := count(); b != 5 {
		t.Fatalf("bots must not change mid-race, got %d", b)
	}
	colours := map[string]bool{}
	for _, p := range g.players {
		if colours[p.Color] {
			t.Fatalf("duplicate colour %s", p.Color)
		}
		colours[p.Color] = true
	}
}

// reseed swaps the game's RNG for a fresh one (for tests that try many rolls
// from one rigged position).
func (g *Game) reseed(seed uint64) {
	c := g.Clone(seed)
	g.src, g.rng = c.src, c.rng
}
