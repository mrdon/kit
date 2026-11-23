package engine

import (
	"testing"
	"time"
)

// A driver that wakes only at NextDeadline (and for commands) misses nothing:
// nothing is due just before it, something always happens at it, and it is
// zero only when the game waits on a command.
func TestNextDeadlineMissesNothing(t *testing.T) {
	// Quick: the seen window on (it adds the timeouts to every other due
	// time) and the first golden night. Long: both windows, every night.
	windows, cases := []time.Duration{25 * time.Second}, goldenCases[:1]
	if longTests {
		windows, cases = []time.Duration{0, 25 * time.Second}, goldenCases
	}
	for _, window := range windows {
		opts := Options{SeenWindow: window}
		for _, c := range cases {
			g := NewGameWith(c.cfg(), opts, c.seed)
			s := newScripted(g, c.people)
			now := t0
			for steps, ok := 0, true; ok; steps++ {
				checkDeadline(t, c.name, g, opts)
				now, ok = s.step(g, now)
			}
		}
	}
}

func checkDeadline(t *testing.T, label string, g *Game, opts Options) {
	t.Helper()
	data, err := g.Save()
	if err != nil {
		t.Fatal(err)
	}
	fork := func() *Game {
		r, err := RestoreWith(data, opts)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	d := g.NextDeadline()
	if d.IsZero() {
		if (g.phase == PhaseRacing && g.sub != SubRaceEnd) || g.nextExpiry() != (time.Time{}) {
			t.Fatalf("%s: zero deadline mid-race or with someone to time out (%s/%s)", label, g.phase, g.sub)
		}
		if fork().Advance(g.now.Add(24 * time.Hour)) {
			t.Fatalf("%s: zero deadline but the game moved on by itself (%s)", label, g.phase)
		}
		return
	}
	if f := fork(); f.Advance(d.Add(-time.Millisecond)) {
		t.Fatalf("%s: something happened before NextDeadline %v (%s/%s)", label, d, g.phase, g.sub)
	}
	if f := fork(); !f.Advance(d) {
		t.Fatalf("%s: nothing happened at NextDeadline %v (%s/%s)", label, d, g.phase, g.sub)
	}
}

func TestSeenKeepsPeopleConnected(t *testing.T) {
	w := 20 * time.Second
	g := NewGameWith(GameConfig{Rules: Rules{MinFrogs: 3, Rounds: 1}, Levels: SingleLevel(12)}, Options{SeenWindow: w}, 1)
	g.Apply(Command{Type: CmdJoin, Player: "a", Name: "A", Color: "#2ecc71"}, t0)
	if d := g.NextDeadline(); !d.Equal(t0.Add(w)) {
		t.Fatalf("lobby with one person: next deadline should be their timeout, got %v", d)
	}
	bots := len(g.players) - 1
	g.Seen("a", t0.Add(15*time.Second))
	if d := g.NextDeadline(); !d.Equal(t0.Add(35 * time.Second)) {
		t.Fatalf("a heartbeat should push the timeout back, got %v", d)
	}
	v := g.Version()
	if g.Advance(t0.Add(34 * time.Second)) {
		t.Fatal("still inside the window: nothing should change")
	}
	if !g.Advance(t0.Add(35*time.Second)) || g.players["a"].Connected || g.Version() == v {
		t.Fatal("unseen for the window: should drop out and bump the version")
	}
	if len(g.players)-1 == bots {
		t.Fatal("dropping out should send in another bot")
	}
	if !g.NextDeadline().IsZero() {
		t.Fatal("nobody left to time out: the lobby just waits")
	}
	if changed, _ := g.Apply(Command{Type: CmdSeen, Player: "a"}, t0.Add(40*time.Second)); !changed || !g.players["a"].Connected {
		t.Fatal("seen again: back in")
	}
	if changed, _ := g.Apply(Command{Type: CmdSeen, Player: "a"}, t0.Add(41*time.Second)); changed {
		t.Fatal("a heartbeat from someone already connected changes nothing visible")
	}
	g.Apply(Command{Type: CmdLeave, Player: "a"}, t0.Add(42*time.Second))
	if g.players["a"].Connected {
		t.Fatal("leave still disconnects at once")
	}
	g.Apply(Command{Type: CmdSeen, Player: "nobody"}, t0.Add(43*time.Second))
	if _, ok := g.players["nobody"]; ok {
		t.Fatal("seen never adds a player")
	}
}

// With no SeenWindow nobody ever times out (the headless harness and old
// servers rely on it).
func TestNoSeenWindowNoTimeouts(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 3, Rounds: 1}, Levels: SingleLevel(12)}, 1)
	g.Apply(Command{Type: CmdJoin, Player: "a", Name: "A", Color: "#2ecc71"}, t0)
	if g.Advance(t0.Add(time.Hour)) || !g.players["a"].Connected || !g.NextDeadline().IsZero() {
		t.Fatal("without a window people stay connected")
	}
}
