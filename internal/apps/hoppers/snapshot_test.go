package hoppers

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// midRace is a game a few hops into its first race, with three people and a
// bot or two, so every projection has something to differ on.
func midRace(t *testing.T) (*Game, *engine.Game) {
	t.Helper()
	g := engine.NewGameWith(engine.Default(), engine.Options{Speed: 1000, SeenWindow: engine.DefaultSeenWindow}, 7)
	now := time.Date(2026, 1, 2, 20, 0, 0, 0, time.UTC)
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, id := range ids {
		if _, err := g.Apply(engine.Command{Type: engine.CmdJoin, Player: id, Name: []string{"Ann", "Bob", "Cat"}[i]}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Apply(engine.Command{Type: engine.CmdHost}, now); err != nil {
		t.Fatal(err)
	}
	// Play until a person has just flicked, so the last hop carries a power.
	for range 400 {
		now = now.Add(50 * time.Millisecond)
		g.Advance(now)
		cur := g.View(engine.RoleTV, "").Current
		if g.Phase() == engine.PhaseRacing && g.Sub() == engine.SubFlick && slices.Contains(ids, cur) {
			if _, err := g.Apply(engine.Command{Type: engine.CmdFlick, Player: cur, Power: 0.5}, now); err != nil {
				t.Fatal(err)
			}
			if hop := g.View(engine.RoleTV, "").LastHop; hop != nil && hop.Frog == cur && g.Version() > 30 {
				break
			}
		}
	}
	return &Game{ID: uuid.New(), TenantID: uuid.New(), Name: "jumping-lion"}, g
}

// The snapshot carries the engine's views pre-built, and the projections
// assemble each phone's from the spectator's plus that phone's bits. That is
// an optimisation, so it has to be exact: every projection must equal what
// the engine itself would have said.
func TestProjectionsEqualTheEngineViews(t *testing.T) {
	row, g := midRace(t)
	snap := buildSnapshot(row, g)
	now := time.Now()
	same := func(name string, got Frame, want engine.State) {
		t.Helper()
		got.ServerNow, want.ServerNow = 0, 0
		if !reflect.DeepEqual(got.State, want) {
			t.Errorf("%s: projection differs from the engine's view\n got: %+v\nwant: %+v", name, got.State, want)
		}
	}
	same("tv", ProjectTV(snap, now), g.View(engine.RoleTV, ""))
	same("host", ProjectHost(snap, now), g.View(engine.RoleHost, ""))
	same("spectator", ProjectPlayer(snap, "", now), g.View(engine.RolePlayer, ""))
	for _, p := range g.View(engine.RoleTV, "").Players {
		if p.Bot {
			continue // no phone
		}
		same("player "+p.Name, ProjectPlayer(snap, p.ID, now), g.View(engine.RolePlayer, p.ID))
	}
	if g.View(engine.RoleTV, "").LastHop == nil {
		t.Fatal("no hop happened; the test is not exercising the flick power")
	}
}

// The relay carries snapshots between web processes as JSON, so a relayed
// snapshot must project exactly like the local one.
func TestSnapshotSurvivesTheRelay(t *testing.T) {
	row, g := midRace(t)
	snap := buildSnapshot(row, g)
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var back Snapshot
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, id := range append([]string{""}, keys(snap.Mine)...) {
		a, _ := json.Marshal(ProjectPlayer(snap, id, now))
		b, _ := json.Marshal(ProjectPlayer(&back, id, now))
		if string(a) != string(b) {
			t.Fatalf("player %q: relayed projection differs\n%s\n%s", id, a, b)
		}
	}
	a, _ := json.Marshal(ProjectHost(snap, now))
	b, _ := json.Marshal(ProjectHost(&back, now))
	if string(a) != string(b) {
		t.Fatal("host: relayed projection differs")
	}
}

// Only the flicker's own phone learns its flick's power: next to "perfect" it
// would give away where the sweet spot is.
func TestOnlyTheFlickerSeesThePower(t *testing.T) {
	row, g := midRace(t)
	snap := buildSnapshot(row, g)
	hop := snap.TV.LastHop
	if hop == nil || snap.HopPower == 0 {
		t.Fatal("midRace should end on a person's flick")
	}
	now := time.Now()
	if ProjectTV(snap, now).LastHop.Power != 0 {
		t.Fatal("the TV sees the flick power")
	}
	for id := range snap.Mine {
		got := ProjectPlayer(snap, id, now).LastHop.Power
		if (id == hop.Frog) != (got != 0) {
			t.Errorf("player %s sees power %v (flicker %s)", id, got, hop.Frog)
		}
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
