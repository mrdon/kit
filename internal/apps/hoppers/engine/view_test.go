package engine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func viewGame(t *testing.T) *Game {
	t.Helper()
	g := NewGameWith(GameConfig{Rules: Rules{MinFrogs: 3, Rounds: 2}, Levels: SingleLevel(12)}, Options{JoinURL: "http://x/play", Build: "b1"}, 1)
	g.Apply(Command{Type: CmdJoin, Player: "a", Name: "A", Color: "#2ecc71"}, t0)
	g.Apply(Command{Type: CmdJoin, Player: "b", Name: "B", Color: "#3498db"}, t0)
	return g
}

func TestSnapshotIsTheTVView(t *testing.T) {
	g := viewGame(t)
	g.Apply(Command{Type: CmdHost}, t0)
	a, _ := json.Marshal(g.Snapshot())
	b, _ := json.Marshal(g.View(RoleTV, "a"))
	if string(a) != string(b) {
		t.Fatal("Snapshot should be the TV view")
	}
	s := g.Snapshot()
	if s.You != nil || s.Host != nil || s.FlickSpot != 0 || strings.Contains(string(a), "flickSpot") {
		t.Fatal("the TV view has no phone, host or sweet-spot bits")
	}
	if s.Version != g.Version() || s.JoinURL != "http://x/play" || len(s.Stacks) == 0 {
		t.Fatalf("TV view is everything public: %+v", s)
	}
}

func TestPlayerView(t *testing.T) {
	g := viewGame(t)
	g.Apply(Command{Type: CmdHost}, t0)
	now := t0
	for g.Sub() != SubFlick || g.current() != "a" {
		if g.Sub() == SubFlick && g.current() == "b" {
			g.Apply(Command{Type: CmdFlick, Player: "b", Power: 0.6}, now)
			continue
		}
		now = g.NextDeadline()
		g.Advance(now)
	}
	s := g.View(RolePlayer, "a")
	if s.You == nil || !s.You.MyTurn || s.You.ID != "a" || !s.You.Racing || s.You.Place < 1 || s.You.HeatName != "Race 1" {
		t.Fatalf("a's phone should know it's their turn: %+v", s.You)
	}
	if s.FlickSpot != FlickSpot || s.FlickBand != BandWidth {
		t.Fatalf("phones get the sweet spot: spot %g band %g", s.FlickSpot, s.FlickBand)
	}
	if s.Stacks != nil || s.TurnOrder != nil || s.JoinURL != "" || len(s.Track.Specials) != 0 || s.Host != nil {
		t.Fatal("a phone doesn't get the TV's drawing data or host controls")
	}
	if b := g.View(RolePlayer, "b"); b.You == nil || b.You.MyTurn {
		t.Fatal("b's phone: not their turn")
	}
	if v := g.View(RolePlayer, "stranger"); v.You != nil {
		t.Fatal("an unknown phone gets no You")
	}
	if v := g.View(RolePlayer, "bot-0"); v.You != nil {
		t.Fatal("bots have no phone")
	}
	g.Apply(Command{Type: CmdFlick, Player: "a", Power: 0.7, Perfect: true}, now)
	if p := g.View(RolePlayer, "a").LastHop.Power; p != 0.7 {
		t.Fatalf("your own flick's power comes back to you, got %g", p)
	}
	for _, v := range []State{g.Snapshot(), g.View(RolePlayer, "b"), g.View(RoleHost, "")} {
		if v.LastHop.Power != 0 {
			t.Fatalf("%s: someone else's flick power would give away the sweet spot", v.Role)
		}
	}
}

func TestHostView(t *testing.T) {
	g := viewGame(t)
	h := g.View(RoleHost, "").Host
	if h == nil || !h.CanAdvance || h.Advance != "Start" || h.NextDeadline != 0 || h.Config.Rounds != 2 || len(h.Config.Levels) != 1 {
		t.Fatalf("lobby host view: %+v", h)
	}
	g.Apply(Command{Type: CmdHost}, t0)
	h = g.View(RoleHost, "").Host
	if h.CanAdvance || h.Advance != "" || h.NextDeadline != g.NextDeadline().UnixMilli() {
		t.Fatalf("mid-race the button does nothing and the deadline shows: %+v", h)
	}
	if h.Config.FlickTimeoutMs != 8000 {
		t.Fatalf("config summary: %+v", h.Config)
	}
}

func TestParseConfigJSON(t *testing.T) {
	c, err := ParseConfig([]byte(`{"rules": {"rounds": 2, "flickTimeout": "6s"}}`))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.Rules.Rounds != 2 || c.Rules.FlickTimeout != 6*time.Second ||
		c.Rules.SlipChance != d.Rules.SlipChance || !reflect.DeepEqual(c.Levels, d.Levels) || !reflect.DeepEqual(c.Rules.PlacePoints, d.Rules.PlacePoints) {
		t.Fatalf("JSON should decode over the defaults: %+v", c)
	}
	if c, err := ParseConfig([]byte(`{"rules": {"flickTimeout": 7000000000}}`)); err != nil || c.Rules.FlickTimeout != 7*time.Second {
		t.Fatalf("durations also read as nanoseconds: %v %+v", err, c.Rules)
	}
	c, err = ParseConfig([]byte(`{"levels": [{"layout": "straight", "track": {"length": 10}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Levels) != 1 || c.Levels[0].Name != "" || c.Levels[0].Theme != "" {
		t.Fatalf("a JSON level list replaces the defaults whole: %+v", c.Levels)
	}
	for _, bad := range []string{`{"rules": {"rouns": 2}}`, `{"colour": "red"}`, `{"rules": {"flickTimeout": "soon"}}`} {
		if _, err := ParseConfig([]byte(bad)); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	// a marshalled config reads back the same
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := ParseConfig(data); err != nil || !reflect.DeepEqual(back, d) {
		t.Fatalf("JSON round trip: %v\n%+v", err, back)
	}
	// YAML flow style still works
	if c, err := ParseConfig([]byte("{rules: {rounds: 4}}")); err != nil || c.Rules.Rounds != 4 {
		t.Fatalf("YAML flow mapping: %v", err)
	}
}

func TestBuiltinPacks(t *testing.T) {
	names := BuiltinPacks()
	if len(names) < 2 {
		t.Fatalf("want the shipped packs, got %v", names)
	}
	for _, n := range names {
		c, err := BuiltinPack(n)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		NewGame(c, 1)
	}
	if c, _ := BuiltinPack("default"); !reflect.DeepEqual(c, Default()) {
		t.Fatalf("the default pack should be Default():\n%+v", c)
	}
	if _, err := BuiltinPack("nope"); err == nil {
		t.Fatal("unknown pack: want an error")
	}
}
