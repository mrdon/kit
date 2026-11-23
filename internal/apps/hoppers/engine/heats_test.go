package engine

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func crowd(t *testing.T, cfg GameConfig, n int, seed uint64) *Game {
	t.Helper()
	g := NewGame(cfg, seed)
	for i := range n {
		g.Apply(Command{Type: CmdJoin, Player: fmt.Sprintf("p-%02d", i), Name: fmt.Sprintf("P%d", i), Color: "#2ecc71"}, t0)
	}
	return g
}

// runRace plays until the current heat's race is over (people flick at once).
func runRace(t *testing.T, g *Game, now time.Time) time.Time {
	t.Helper()
	for i := 0; g.Phase() == PhaseRacing && g.Sub() != SubRaceEnd; i++ {
		if i > 100000 {
			t.Fatal("race never finished")
		}
		if g.Sub() == SubFlick {
			if p := g.players[g.current()]; p != nil && !p.Bot {
				g.Apply(Command{Type: CmdFlick, Player: p.ID, Power: 0.7}, now)
				continue
			}
		}
		now = g.NextDeadline()
		g.Advance(now)
	}
	return now
}

// moveOn is the host pressing on from a race's results (which wait for them).
func moveOn(t *testing.T, g *Game, now time.Time) {
	t.Helper()
	if g.Phase() != PhaseRacing || g.Sub() != SubRaceEnd {
		return
	}
	if !g.NextDeadline().IsZero() {
		t.Fatal("results should wait for the host, with no deadline")
	}
	g.Apply(Command{Type: CmdHost}, now)
}

// playNight plays the whole night headless, the host starting every round.
func playNight(t *testing.T, g *Game) time.Time {
	t.Helper()
	now := t0
	g.Apply(Command{Type: CmdHost}, now)
	for i := 0; g.Phase() != PhaseFinal; i++ {
		if i > 10000 {
			t.Fatal("night never finished")
		}
		now = runRace(t, g, now)
		moveOn(t, g, now)
	}
	return now
}

func TestBotsFinishNight(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 7, HeatSize: 8, Rounds: 3, FlickTimeout: 8 * time.Second, SlipChance: 0.12}, Levels: SingleLevel(16)}, 1, 1)
	end := playNight(t, g)
	if g.round != 3 || len(g.heats) != 1 || len(g.heats[0].Frogs) != 7 {
		t.Fatalf("expected 3 single races of 7 frogs, got round %d heats %+v", g.round, g.heats)
	}
	for _, p := range g.players {
		if p.Bot && p.Score != 0 {
			t.Fatalf("bot %s scored %d", p.Name, p.Score)
		}
	}
	if g.champion != "" && g.champion != "p-00" {
		t.Fatalf("only a person can be champion, got %q", g.champion)
	}
	t.Logf("night took %s of game time, human scored %d", end.Sub(t0), g.players["p-00"].Score)
}

func TestHeatSplitsAreEven(t *testing.T) {
	for _, c := range []struct {
		n    int
		want []int
	}{{1, []int{1}}, {8, []int{8}}, {9, []int{5, 4}}, {15, []int{8, 7}}, {16, []int{8, 8}}, {17, []int{6, 6, 5}}, {30, []int{8, 8, 7, 7}}} {
		if got := heatSizes(c.n, 8); !slices.Equal(got, c.want) {
			t.Errorf("heatSizes(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}

func TestHeatsRoundOne(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 7, HeatSize: 8, Rounds: 3, FlickTimeout: 8 * time.Second}, Levels: SingleLevel(16)}, 10, 1)
	if bots := len(g.players) - 10; bots != 2 {
		t.Fatalf("10 people in two heats of 5 need 2 bots each, lobby has %d", bots)
	}
	g.Apply(Command{Type: CmdHost}, t0)
	if len(g.heats) != 2 {
		t.Fatalf("want 2 heats, got %d", len(g.heats))
	}
	seen := map[string]int{}
	for i, h := range g.heats {
		if g.humansIn(h) != 5 || len(h.Frogs) != 7 {
			t.Fatalf("heat %d: %d people, %d frogs; want 5 and 7", i+1, g.humansIn(h), len(h.Frogs))
		}
		for _, f := range h.Frogs {
			if !g.players[f].Bot {
				seen[f]++
				if g.players[f].Heat != i+1 {
					t.Fatalf("%s is in heat %d but Player.Heat=%d", f, i+1, g.players[f].Heat)
				}
			}
		}
	}
	if len(seen) != 10 {
		t.Fatalf("every person should race exactly once, got %v", seen)
	}
	s := g.Snapshot()
	if s.Heat != 1 || s.HeatName != "Heat 1" || s.Heats[0].Status != HeatRacing || s.Heats[1].Status != HeatNext {
		t.Fatalf("bad heat snapshot: heat=%d name=%q statuses=%s/%s", s.Heat, s.HeatName, s.Heats[0].Status, s.Heats[1].Status)
	}
	if len(s.Ranking) != 7 {
		t.Fatalf("only heat 1's frogs should be on the track, got %d", len(s.Ranking))
	}
	// Heat 1 finishes: its results wait for the host, who starts heat 2.
	now := runRace(t, g, t0)
	if g.Advance(now.Add(time.Hour)) || g.Sub() != SubRaceEnd || g.Snapshot().RoundOver {
		t.Fatalf("heat 1's results should wait for the host, got sub=%s", g.Sub())
	}
	if h := g.View(RoleHost, "").Host; !h.CanAdvance || h.Advance != "Next heat" {
		t.Fatalf("host button after heat 1: %+v", h)
	}
	g.Apply(Command{Type: CmdHost}, now)
	if g.Sub() != SubReady || g.heat != 1 || g.heats[1].Status != HeatRacing || g.heats[0].Status != HeatDone {
		t.Fatalf("host should start heat 2's countdown, got sub=%s heat=%d", g.Sub(), g.heat)
	}
}

func TestPointsByPlace(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 1, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 5, 1)
	g.Apply(Command{Type: CmdHost}, t0)
	// Rig the finish: p-04 rides p-03 over the line, p-00 trails behind them.
	g.resetStacks()
	g.stacks[15] = []string{"p-03", "p-04"}
	g.stacks[10] = []string{"p-01", "p-00"}
	g.stacks[2] = []string{"p-02"}
	g.cfg.Rules.SlipChance = 0
	g.cfg.Rules.MaxRoll = 1
	g.order, g.hopIndex, g.sub = []string{"p-03"}, 0, SubFlick
	g.Apply(Command{Type: CmdFlick, Player: "p-03"}, t0)
	g.Advance(g.NextDeadline())
	if g.Sub() != SubRaceEnd || g.winner != "p-04" {
		t.Fatalf("expected p-04 to win riding p-03, got sub=%s winner=%s", g.Sub(), g.winner)
	}
	// every place scores, the winner clearly ahead
	want := map[string]int{"p-04": 11, "p-03": 9, "p-00": 8, "p-01": 7, "p-02": 6}
	for id, pts := range want {
		if got := g.players[id].Score; got != pts {
			t.Errorf("%s scored %d, want %d", id, got, pts)
		}
	}
	if g.results[0].Place != 1 || g.results[0].Player != "p-04" || g.players["p-04"].Wins != 1 {
		t.Fatalf("bad results %+v", g.results)
	}
	if s := g.Snapshot(); !s.RoundOver || !s.LastRace || s.Deadline != 0 {
		t.Fatalf("the night's last results: roundOver %v lastRace %v deadline %d", s.RoundOver, s.LastRace, s.Deadline)
	}
	g.Apply(Command{Type: CmdHost}, g.Now())
	if g.Phase() != PhaseFinal || g.champion != "p-04" {
		t.Fatalf("one race night: champion should be the points leader, got phase=%s champion=%s", g.Phase(), g.champion)
	}
}

func TestFinalRoundSeededByStandings(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 7, HeatSize: 8, Rounds: 2, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 20, 3)
	// Fake round 1 as played: the people with the highest numbers did best.
	g.round = 1
	g.phase = PhaseLobby // between rounds: the host button starts the next one
	for i := range 20 {
		g.players[fmt.Sprintf("p-%02d", i)].Score = i
	}
	g.Apply(Command{Type: CmdHost}, t0)
	if len(g.heats) != 3 {
		t.Fatalf("20 people should race in 3 heats, got %d", len(g.heats))
	}
	final := g.heats[2]
	if !final.Final || final.Name != "The Final" || g.heats[0].Final {
		t.Fatalf("The Final should run last: %+v", g.heats)
	}
	// sizes 7,7,6: the top 7 scorers are p-13..p-19, in The Final
	top := slices.Sorted(slices.Values(final.Frogs))
	if want := []string{"p-13", "p-14", "p-15", "p-16", "p-17", "p-18", "p-19"}; !slices.Equal(top, want) {
		t.Fatalf("The Final should hold the top 7 %v, got %v", want, top)
	}
	// and the bottom heat runs first
	for _, f := range g.heats[0].Frogs {
		if g.players[f].Score > 5 {
			t.Fatalf("heat 1 should be the lowest seeds, has %s with %d", f, g.players[f].Score)
		}
	}
	if s := g.Snapshot(); s.Final || s.HeatName != "Heat 1" {
		t.Fatalf("heat 1 is on, not the final: %+v", s.HeatName)
	}
	// Play it out: the champion is whoever has the most points.
	now := t0
	for g.Phase() == PhaseRacing {
		now = runRace(t, g, now)
		moveOn(t, g, now)
	}
	if g.Phase() != PhaseFinal {
		t.Fatalf("expected the night to end, phase %s", g.Phase())
	}
	best := ""
	for i := range 20 {
		id := fmt.Sprintf("p-%02d", i)
		if best == "" || g.players[id].Score > g.players[best].Score {
			best = id
		}
	}
	s := g.Snapshot()
	if g.players[g.champion].Score != g.players[best].Score || s.Champion != g.champion || s.Standings[0].Player != g.champion {
		t.Fatalf("champion %s (%d pts) should have the most points (%s has %d)", g.champion, g.players[g.champion].Score, best, g.players[best].Score)
	}
}

func TestThirtyPlayerNightFinishes(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 7, HeatSize: 8, Rounds: 3, FlickTimeout: 8 * time.Second, SlipChance: 0.08}, Levels: SingleLevel(16)}, 30, 7)
	if bots := len(g.players) - 30; bots != 0 {
		t.Fatalf("heats of 7-8 need no bots, lobby has %d", bots)
	}
	end := playNight(t, g)
	raced := 0
	for _, p := range g.players {
		if !p.Bot && p.Heat > 0 {
			raced++
		}
	}
	if raced != 30 || len(g.heats) != 4 || !g.heats[3].Final {
		t.Fatalf("final round should seat all 30 in 4 heats ending in The Final, got %d seated, %d heats", raced, len(g.heats))
	}
	if g.champion == "" || g.Snapshot().Standings[0].Player != g.champion {
		t.Fatalf("champion %q must top the standings", g.champion)
	}
	total := 0
	for _, p := range g.players {
		total += p.Score
	}
	sum := func(n int) (t int) {
		for _, p := range DefaultPlacePoints[:n] {
			t += p
		}
		return t
	}
	// heats of 8, 8, 7, 7: every person scores in every race
	if want := 3 * (2*sum(8) + 2*sum(7)); total != want {
		t.Fatalf("12 races should hand out %d points, got %d", want, total)
	}
	t.Logf("30-person night: %s of game time (no beer breaks)", end.Sub(t0))
}

func TestLateJoinerGetsAPendingHeat(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 7, HeatSize: 8, Rounds: 2, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 12, 1)
	g.Apply(Command{Type: CmdHost}, t0)
	g.Apply(Command{Type: CmdJoin, Player: "late", Name: "Late", Color: "#fff"}, t0)
	if p := g.players["late"]; p.Heat != 2 || !slices.Contains(g.heats[1].Frogs, "late") {
		t.Fatalf("late joiner should race in heat 2, got heat %d", p.Heat)
	}
	if got := len(g.heats[1].Frogs); got != 7 {
		t.Fatalf("they should take a bot's seat, heat 2 has %d frogs", got)
	}
}

func TestFlickPowerRecorded(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 1, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 1, 1)
	g.Apply(Command{Type: CmdHost}, t0)
	g.Advance(g.NextDeadline())
	g.Apply(Command{Type: CmdFlick, Player: "p-00", Power: 0.9}, g.Now())
	if g.lastHop == nil || g.lastHop.Power != 0.9 {
		t.Fatalf("flick power should ride along on the hop, got %+v", g.lastHop)
	}
}

func TestDefaultHeatSizeIsOneRaceUpToTen(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 7, Rounds: 1, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 10, 1)
	g.Apply(Command{Type: CmdHost}, t0)
	if len(g.heats) != 1 || g.heats[0].Name != "Race 1" || len(g.heats[0].Frogs) != 10 {
		t.Fatalf("10 people should be one race, got %+v", g.heats)
	}
}

// The last-round announcement counts people, not the bots filling The Final.
func TestFinalAnnouncementCountsPeople(t *testing.T) {
	g := crowd(t, GameConfig{Rules: Rules{MinFrogs: 7, HeatSize: 6, Rounds: 2, FlickTimeout: time.Second}, Levels: SingleLevel(16)}, 16, 3)
	g.round = 1
	g.phase = PhaseLobby // between rounds: the host button starts the next one
	g.Apply(Command{Type: CmdHost}, t0)
	final := g.heats[len(g.heats)-1]
	people := 0
	for _, f := range final.Frogs {
		if !g.players[f].Bot {
			people++
		}
	}
	if people == len(final.Frogs) {
		t.Fatalf("want bots filling The Final for this test, got %v", final.Frogs)
	}
	want := fmt.Sprintf("Last round. Top %d make the final.", people)
	for _, e := range g.events {
		if e.Text == want {
			return
		}
	}
	t.Fatalf("want %q in %+v", want, g.events)
}
