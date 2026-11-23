package engine

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// forkAt plays a scripted night until at(g) has held `nth` times, then
// returns the game, its script and the time.
func forkAt(t *testing.T, c goldenCase, at func(*Game) bool, nth int) (*Game, *scripted, time.Time) {
	t.Helper()
	g := NewGame(c.cfg(), c.seed)
	s := newScripted(g, c.people)
	now := t0
	seen := 0
	for ok := true; ok; {
		if at(g) {
			if seen++; seen == nth {
				return g, s, now
			}
		}
		now, ok = s.step(g, now)
	}
	return nil, nil, now
}

func snapJSON(t *testing.T, g *Game) string {
	t.Helper()
	b, err := json.Marshal(g.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A game saved at any point and restored plays on exactly like the one that
// was never saved: same commands at the same times, same snapshots to the end.
func TestSaveRestorePlaysOnIdentically(t *testing.T) {
	points := map[string]func(*Game) bool{
		"lobby":     func(g *Game) bool { return true },
		"mid-hop":   func(g *Game) bool { return g.sub == SubHop },
		"flick":     func(g *Game) bool { return g.sub == SubFlick && !g.botAt.IsZero() },
		"heat over": func(g *Game) bool { return g.sub == SubRaceEnd && !g.roundOver() },
		"race end":  func(g *Game) bool { return g.sub == SubRaceEnd },
		"break":     func(g *Game) bool { return g.roundOver() },
		"special":   func(g *Game) bool { return g.lastHop != nil && g.lastHop.Special != nil && g.sub == SubHop },
		"backwards": func(g *Game) bool { return len(g.backwardIDs()) > 0 },
		"final":     func(g *Game) bool { return g.phase == PhaseFinal },
	}
	forks := 0
	for _, c := range goldenCases {
		for _, seed := range []uint64{c.seed, c.seed + 1} {
			c.seed = seed
			for name, at := range points {
				for _, nth := range []int{2} {
					g, s, now := forkAt(t, c, at, nth)
					if g == nil {
						continue
					}
					forks++
					checkTwin(t, c.name+"/"+name, g, s, now)
				}
			}
		}
	}
	if forks < 30 {
		t.Fatalf("only %d save points were reached; the script should hit more", forks)
	}
}

func checkTwin(t *testing.T, label string, g *Game, s *scripted, now time.Time) {
	t.Helper()
	data, err := g.Save()
	if err != nil {
		t.Fatalf("%s: save: %v", label, err)
	}
	r, err := Restore(data)
	if err != nil {
		t.Fatalf("%s: restore: %v", label, err)
	}
	if again, _ := r.Save(); !bytes.Equal(again, data) {
		t.Fatalf("%s: restore then save changed the blob", label)
	}
	if r.Version() != g.Version() || snapJSON(t, r) != snapJSON(t, g) {
		t.Fatalf("%s: restored game looks different", label)
	}
	rs := *s
	rs.queue = slices.Clone(s.queue)
	gNow, rNow := now, now
	for steps, okG := 0, true; okG; steps++ {
		var okR bool
		gNow, okG = s.step(g, gNow)
		rNow, okR = rs.step(r, rNow)
		if okG != okR || !gNow.Equal(rNow) {
			t.Fatalf("%s: step %d: drivers diverged (%v %v, %v %v)", label, steps, okG, okR, gNow, rNow)
		}
		if a, b := snapJSON(t, g), snapJSON(t, r); a != b {
			t.Fatalf("%s: step %d: snapshots diverged\nsaved:   %s\nrestored: %s", label, steps, a, b)
		}
	}
	a, _ := g.Save()
	b, _ := r.Save()
	if !bytes.Equal(a, b) {
		t.Fatalf("%s: final saves differ", label)
	}
}

func TestSaveFormat(t *testing.T) {
	g := NewGameWith(Default(), Options{JoinURL: "http://bar.example/play", Build: "abc123", SeenWindow: time.Minute}, 3)
	g.Apply(Command{Type: CmdJoin, Player: "a", Name: "Al", Color: "#2ecc71"}, t0)
	g.Apply(Command{Type: CmdHost}, t0)
	data, err := g.Save()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != float64(SaveVersion) || m["stateVersion"] != float64(g.Version()) {
		t.Fatalf("save should carry its schema version and the state version: %v %v", m["version"], m["stateVersion"])
	}
	for _, leak := range []string{"bar.example", "abc123"} {
		if strings.Contains(string(data), leak) {
			t.Fatalf("server options must not be saved, found %q", leak)
		}
	}
	r, err := RestoreWith(data, Options{JoinURL: "http://new/play", Build: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Snapshot(); s.JoinURL != "http://new/play" || s.Build != "new" {
		t.Fatalf("RestoreWith should use the new options, got %q %q", s.JoinURL, s.Build)
	}
}

func TestRestoreRejectsBadSaves(t *testing.T) {
	g := NewGame(Default(), 1)
	g.Apply(Command{Type: CmdHost}, t0)
	good, err := g.Save()
	if err != nil {
		t.Fatal(err)
	}
	edit := func(f func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		b, _ := json.Marshal(m)
		return b
	}
	for name, data := range map[string][]byte{
		"not json":     []byte("{"),
		"old version":  edit(func(m map[string]any) { m["version"] = 0 }),
		"bad rng":      edit(func(m map[string]any) { m["rng"] = "AAAA" }),
		"no stacks":    edit(func(m map[string]any) { m["stacks"] = []any{} }),
		"stray frog":   edit(func(m map[string]any) { m["stacks"].([]any)[0] = []any{"ghost"} }),
		"bad config":   edit(func(m map[string]any) { m["config"].(map[string]any)["levels"] = []any{} }),
		"unknown rule": edit(func(m map[string]any) { m["config"].(map[string]any)["rules"].(map[string]any)["rouns"] = 1 }),
	} {
		if _, err := Restore(data); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestVersionCountsEveryChange(t *testing.T) {
	g := NewGame(GameConfig{Rules: Rules{MinFrogs: 3, Rounds: 1}, Levels: SingleLevel(12)}, 1)
	v := g.Version()
	g.Apply(Command{Type: CmdJoin, Player: "a", Name: "A", Color: "#2ecc71"}, t0)
	if g.Version() <= v || g.Snapshot().Version != g.Version() {
		t.Fatalf("join should bump the version (%d -> %d)", v, g.Version())
	}
	v = g.Version()
	if changed, _ := g.Apply(Command{Type: CmdFlick, Player: "a"}, t0); changed || g.Version() != v {
		t.Fatalf("a flick out of turn changes nothing, version %d -> %d", v, g.Version())
	}
	g.Apply(Command{Type: CmdHost}, t0)
	for g.Phase() == PhaseRacing {
		if g.Sub() == SubRaceEnd {
			g.Apply(Command{Type: CmdHost}, g.Now())
			continue
		}
		v = g.Version()
		changed := g.Advance(g.NextDeadline())
		if changed != (g.Version() > v) {
			t.Fatalf("Advance changed=%v but version %d -> %d", changed, v, g.Version())
		}
		if g.Version() < v {
			t.Fatal("version went down")
		}
	}
}
