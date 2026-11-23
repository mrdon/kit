package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// skillGame is a one-person race rigged so p-00 is about to flick from from.
func skillGame(t *testing.T, cfg GameConfig, from int, back bool) *Game {
	t.Helper()
	cfg.Levels, cfg.Rules.Rounds, cfg.Rules.FlickTimeout = SingleLevel(30), 1, time.Second
	if cfg.Rules.MinFrogs == 0 {
		cfg.Rules.MinFrogs = 3
	}
	g := crowd(t, cfg, 1, 1)
	g.Apply(Command{Type: CmdHost}, t0)
	g.resetStacks()
	g.stacks[from] = []string{"p-00"}
	g.stacks[0] = []string{"bot-0", "bot-1"}
	g.backward = map[string]bool{"p-00": back}
	g.order, g.hopIndex, g.sub = []string{"p-00", "bot-0", "bot-1"}, 0, SubFlick
	g.deadline = t0.Add(time.Second)
	return g
}

func TestPerfectFlickBonusApplies(t *testing.T) {
	g := skillGame(t, GameConfig{Rules: Rules{MaxRoll: 1, SkillCap: 2}}, 5, false)
	g.Apply(Command{Type: CmdFlick, Player: "p-00", Power: 0.5, Perfect: true}, t0)
	if h := g.lastHop; !h.Perfect || h.Bonus != 1 || h.Roll != 2 || h.To != 7 {
		t.Fatalf("perfect flick should hop 1+1, got %+v", h)
	}
	// a flick outside the band never hurts and never helps
	g = skillGame(t, GameConfig{Rules: Rules{MaxRoll: 1, SkillCap: 2}}, 5, false)
	g.Apply(Command{Type: CmdFlick, Player: "p-00", Power: 0.95}, t0)
	if h := g.lastHop; h.Perfect || h.Bonus != 0 || h.Roll != 1 {
		t.Fatalf("plain flick should hop 1, got %+v", h)
	}
}

func TestPerfectFlickAlwaysAddsOne(t *testing.T) {
	for seed := range 30 {
		g := skillGame(t, GameConfig{Rules: Rules{MaxRoll: 3}}, 5, false) // cap defaults to MaxRoll+1
		g.reseed(uint64(seed))
		g.Apply(Command{Type: CmdFlick, Player: "p-00", Perfect: true}, t0)
		if h := g.lastHop; h.Bonus != 1 || h.Roll < 2 || h.Roll > 4 || h.To != 5+h.Roll {
			t.Fatalf("seed %d: a perfect flick always adds 1 (2..4), got %+v", seed, h)
		}
	}
}

func TestPerfectFlickBonusCaps(t *testing.T) {
	capped := 0
	for seed := range 50 {
		g := skillGame(t, GameConfig{Rules: Rules{MaxRoll: 3, SkillCap: 3}}, 5, false)
		g.reseed(uint64(seed))
		g.Apply(Command{Type: CmdFlick, Player: "p-00", Perfect: true}, t0)
		h := g.lastHop
		if h.Roll > 3 || h.Roll < 2 {
			t.Fatalf("seed %d: with a bonus capped at 3 a hop is 2 or 3, got %+v", seed, h)
		}
		if h.Roll == 3 && h.Bonus == 0 {
			capped++ // rolled a 3: nothing to add
			if h.Perfect {
				t.Fatalf("seed %d: Perfect means the bonus applied, got %+v", seed, h)
			}
		}
	}
	if capped == 0 {
		t.Fatal("a rolled 3 should get no bonus at cap 3")
	}
}

// Going the wrong way, a perfect flick wins nothing: the hop is the plain
// roll, and Perfect stays false so the screens don't celebrate.
func TestNoBonusGoingBackwards(t *testing.T) {
	for seed := range 40 {
		g := skillGame(t, GameConfig{Rules: Rules{MaxRoll: 2}}, 10, true)
		g.stacks[10] = []string{"bot-1", "p-00"}
		g.stacks[0] = []string{"bot-0"}
		g.cfg.Rules.TurnBack = 0.0000001 // keep going the wrong way
		g.reseed(uint64(seed))
		g.Apply(Command{Type: CmdFlick, Player: "p-00", Perfect: true}, t0)
		h := g.lastHop
		if !h.Back || h.Bonus != 0 || h.Perfect || h.Roll < 1 || h.To != 10-h.Roll {
			t.Fatalf("seed %d: a backward hop gets no bonus, got %+v", seed, h)
		}
		if last := g.events[len(g.events)-1].Text; strings.Contains(last, "Perfect") || strings.Contains(last, "flick") {
			t.Fatalf("seed %d: no perfect-flick line going backwards: %q", seed, last)
		}
	}
}

func TestNoBonusOnAutoHopOrBots(t *testing.T) {
	g := skillGame(t, GameConfig{Rules: Rules{MaxRoll: 1, SkillCap: 2}}, 5, false)
	g.Advance(g.deadline) // nobody flicked: the frog hops itself
	if h := g.lastHop; !h.Auto || h.Perfect || h.Bonus != 0 || h.Roll != 1 {
		t.Fatalf("auto-hop must not get a bonus, got %+v", h)
	}
	// a bot's turn: a "perfect" flick sent for it earns nothing
	g = skillGame(t, GameConfig{Rules: Rules{MaxRoll: 1, SkillCap: 2}}, 5, false)
	g.order = []string{"bot-0", "p-00", "bot-1"}
	g.Apply(Command{Type: CmdFlick, Player: "bot-0", Perfect: true}, t0)
	if h := g.lastHop; h == nil || h.Frog != "bot-0" || h.Bonus != 0 || h.Perfect {
		t.Fatalf("bots never get a bonus, got %+v", h)
	}
}

func TestEveryPlaceScores(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{}, Levels: SingleLevel(16)}, 1)
	prev := 1 << 30
	for place := 1; place <= 12; place++ {
		p := g.placePoints(place)
		if p < 1 || p > prev {
			t.Fatalf("place %d scores %d (after %d): every place should score, never more than the place above", place, p, prev)
		}
		prev = p
	}
	if g.placePoints(1)-g.placePoints(2) < 2 {
		t.Fatalf("the winner should be clearly ahead: %v", g.cfg.Rules.PlacePoints)
	}
}

// 24 people share 12 colours two each: with 3 heats of 8, nobody should race
// a frog of their own colour.
func TestHeatsSpreadColours(t *testing.T) {
	for seed := range uint64(10) {
		g := NewGame(GameConfig{Rules: Rules{MinFrogs: 7, HeatSize: 8, Rounds: 2, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, seed)
		for i := range 24 {
			g.Apply(Command{Type: CmdJoin, Player: fmt.Sprintf("p-%02d", i), Name: "P", Color: palette[i%len(palette)]}, t0)
		}
		g.Apply(Command{Type: CmdHost}, t0)
		for i, h := range g.heats {
			seen := map[string]string{}
			for _, f := range h.Frogs {
				c := g.players[f].Color
				if o, dup := seen[c]; dup {
					t.Fatalf("seed %d heat %d: %s and %s are both %s", seed, i+1, o, f, c)
				}
				seen[c] = f
			}
		}
	}
}

// Bots are recoloured as each heat lines up so they never match a person in it.
func TestBotColoursUniqueWithinHeat(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 7, HeatSize: 8, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 2)
	// 13 people: heats of 7 and 6 (+1 bot); everyone picks from the first 6 colours
	for i := range 13 {
		g.Apply(Command{Type: CmdJoin, Player: fmt.Sprintf("p-%02d", i), Name: "P", Color: palette[i%6]}, t0)
	}
	now := t0
	g.Apply(Command{Type: CmdHost}, now)
	check := func(h *Heat) {
		seen := map[string]string{}
		for _, f := range h.Frogs {
			c := g.players[f].Color
			if o, dup := seen[c]; dup && (g.players[f].Bot || g.players[o].Bot) {
				t.Fatalf("%s: bot shares %s with %s/%s", h.Name, c, o, f)
			}
			seen[c] = f
		}
	}
	check(g.curHeat())
	moveOn(t, g, runRace(t, g, now))
	if g.Sub() != SubReady || g.heat != 1 {
		t.Fatalf("expected heat 2's countdown, got %s", g.Sub())
	}
	check(g.curHeat())
}

// After the countdown, turns rotate with no pause. Every frog hops
// once per rotation, nobody twice in a row, and "up next" is always right,
// even across the rotation boundary.
func TestTurnsRotateWithoutBreaks(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 5, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(60)}, 4)
	g.Apply(Command{Type: CmdHost}, t0)
	if g.Sub() != SubReady {
		t.Fatalf("race should open with the countdown, got %s", g.Sub())
	}
	var turns []string
	wantNext, lastTurn := "", 0
	for len(turns) < 5*4 {
		g.Advance(g.NextDeadline())
		switch g.Sub() {
		case SubFlick:
			if g.Turn() != lastTurn+1 {
				t.Fatalf("turn counter should tick by one, %d after %d", g.Turn(), lastTurn)
			}
			lastTurn = g.Turn()
			cur := g.current()
			if wantNext != "" && cur != wantNext {
				t.Fatalf("turn %d: onDeck said %s, but %s is hopping", len(turns), wantNext, cur)
			}
			if s := g.Snapshot(); s.TurnOrder[0] != cur || s.Current != cur || len(s.TurnOrder) < 5 {
				t.Fatalf("turnOrder should start with the current frog: %+v", s.TurnOrder)
			}
			wantNext = g.onDeck()
			turns = append(turns, cur)
		case SubHop:
		case SubReady, SubRaceEnd:
			t.Fatalf("after the countdown only flick/hop should happen, got %s", g.Sub())
		default:
			t.Fatalf("after the countdown only flick/hop should happen, got %q", g.Sub())
		}
	}
	for r := range 4 {
		seen := map[string]bool{}
		for _, f := range turns[r*5 : r*5+5] {
			seen[f] = true
		}
		if len(seen) != 5 {
			t.Fatalf("rotation %d should have all 5 frogs once: %v", r, turns[r*5:r*5+5])
		}
	}
	for i := 1; i < len(turns); i++ {
		if turns[i] == turns[i-1] {
			t.Fatalf("%s hopped twice in a row at turn %d", turns[i], i)
		}
	}
}
