package hoppers

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	"github.com/mrdon/kit/internal/livegame"
)

func frogNamed(s *Snapshot, id uuid.UUID) string {
	for _, p := range s.TV.Players {
		if p.ID == id.String() {
			return p.Name
		}
	}
	return ""
}

func connected(s *Snapshot, id uuid.UUID) bool {
	for _, p := range s.TV.Players {
		if p.ID == id.String() {
			return p.Connected
		}
	}
	return false
}

func TestCreateJoinAndStart(t *testing.T) {
	f := newFixture(t)
	game := f.newGame()
	if game.Phase != string(engine.PhaseLobby) || game.JoinCode == "" || !livegame.IsValidGameName(game.Name) {
		t.Fatalf("new game = %+v", game)
	}
	ann := f.join(game, "Ann")
	f.join(game, "Bob")
	before := f.snap(game).StateVersion

	snap, err := f.svc.HostAdvance(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.TV.Phase != engine.PhaseRacing || snap.StateVersion <= before {
		t.Fatalf("after start: phase %s, version %d (was %d)", snap.TV.Phase, snap.StateVersion, before)
	}
	if snap.Mine[ann.ID.String()].You == nil {
		t.Fatal("a joined player has no You in the snapshot")
	}
	row, err := GetGame(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.StateVersion != snap.StateVersion || row.Phase != string(engine.PhaseRacing) || row.NextDueAt == nil {
		t.Fatalf("row not mirrored: version %d phase %s due %v", row.StateVersion, row.Phase, row.NextDueAt)
	}
}

// A race runs on its own: the countdown, the bots' hops and an absent
// person's flick timeout are all due times the sweeper acts on.
func TestTheSweeperRunsTheRace(t *testing.T) {
	f := newFixture(t)
	game := f.newGame()
	f.join(game, "Ann")
	if _, err := f.svc.HostAdvance(f.ctx, f.tenant.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	start := f.snap(game)
	for range 200 {
		f.wait(time.Second)
		// keep Ann's phone alive, so she auto-hops rather than dropping out
		if err := f.svc.Heard(f.ctx, f.tenant.ID, game.ID, mustPlayer(t, f, game), f.snap(game)); err != nil {
			t.Fatal(err)
		}
		f.sweep(game)
		if f.snap(game).TV.Sub == engine.SubRaceEnd {
			break
		}
	}
	end := f.snap(game)
	if end.TV.Sub != engine.SubRaceEnd || end.TV.Winner == "" {
		t.Fatalf("the race never finished on the sweeper: sub %s after %d versions", end.TV.Sub, end.StateVersion-start.StateVersion)
	}
}

func mustPlayer(t *testing.T, f *fixture, game *Game) uuid.UUID {
	t.Helper()
	ps, err := ListPlayers(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil || len(ps) == 0 {
		t.Fatalf("players: %v %v", ps, err)
	}
	return ps[0].ID
}

// Presence: a phone whose stream keeps pinging stays in; one that goes quiet
// for the engine's seen window drops out; and its next ping brings it back at
// once, without waiting for anything else to touch the game.
func TestHeartbeatsDecideWhoIsInTheRoom(t *testing.T) {
	f := newFixture(t)
	game := f.newGame()
	ann := f.join(game, "Ann")
	bob := f.join(game, "Bob")

	for range 4 { // Ann pings every 8s; Bob is silent
		f.wait(8 * time.Second)
		if err := f.svc.Heard(f.ctx, f.tenant.ID, game.ID, ann.ID, f.snap(game)); err != nil {
			t.Fatal(err)
		}
	}
	f.sweep(game)
	s := f.snap(game)
	if !connected(s, ann.ID) {
		t.Fatal("a phone that kept pinging was timed out")
	}
	if connected(s, bob.ID) {
		t.Fatal("a silent phone is still counted in the room")
	}

	if err := f.svc.Heard(f.ctx, f.tenant.ID, game.ID, bob.ID, s); err != nil {
		t.Fatal(err)
	}
	if !connected(f.snap(game), bob.ID) {
		t.Fatal("a returning phone's ping did not bring it back")
	}
}

// Editing a frog is a join with the phone's own cookie: same player, no new
// token, new name.
func TestJoinWithACookieEditsTheFrog(t *testing.T) {
	f := newFixture(t)
	game := f.newGame()
	ann := f.join(game, "Ann")
	p, token, err := f.svc.Join(f.ctx, f.tenant.ID, game.ID, ann.ID, Frog{Name: "  Annie  ", Color: "#3498db"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != ann.ID || token != "" || p.Name != "Annie" {
		t.Fatalf("edit gave player %v token %q name %q", p.ID, token, p.Name)
	}
	if got := frogNamed(f.snap(game), ann.ID); got != "Annie" {
		t.Fatalf("the engine's frog is called %q, want Annie", got)
	}
	if _, _, err := f.svc.Join(f.ctx, f.tenant.ID, game.ID, uuid.Nil, Frog{Name: "   "}); err == nil {
		t.Fatal("a frog with no name was let in")
	}
}

// The host's code moves a frog to a new phone, once. The old phone's cookie
// stops working the moment the code is issued.
func TestReclaimIsHostIssuedAndSingleUse(t *testing.T) {
	f := newFixture(t)
	game := f.newGame()
	ann := f.join(game, "Ann")
	old, _ := GetPlayer(f.ctx, f.pool, f.tenant.ID, game.ID, ann.ID)

	code := livegame.ReclaimCode()
	if err := f.svc.IssueReclaim(f.ctx, f.tenant.ID, game.ID, ann.ID, code); err != nil {
		t.Fatal(err)
	}
	if _, err := FindPlayerByToken(f.ctx, f.pool, f.tenant.ID, game.ID, ann.ID, old.TokenHash); err == nil {
		t.Fatal("the lost phone's token still works after the code was issued")
	}
	token, err := f.svc.RedeemReclaim(f.ctx, f.tenant.ID, game.ID, ann.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FindPlayerByToken(f.ctx, f.pool, f.tenant.ID, game.ID, ann.ID, livegame.HashToken(token)); err != nil {
		t.Fatal("the new phone's token does not resolve")
	}
	if _, err := f.svc.RedeemReclaim(f.ctx, f.tenant.ID, game.ID, ann.ID, code); err == nil {
		t.Fatal("a reclaim code worked twice")
	}
}

// Every change holds the row lock from load to save, so concurrent changes
// (a flick, a sweep, a ping from another process) serialise instead of one
// overwriting the other's engine.
func TestConcurrentChangesSerialise(t *testing.T) {
	f := newFixture(t)
	game := f.newGame()
	var wg sync.WaitGroup
	const n = 12
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			_, _, errs[i] = f.svc.Join(f.ctx, f.tenant.ID, game.ID, uuid.Nil, Frog{Name: "Frog"})
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	people := 0
	for _, p := range f.snap(game).TV.Players {
		if !p.Bot {
			people++
		}
	}
	if people != n {
		t.Fatalf("%d of %d concurrent joins survived", people, n)
	}
}
