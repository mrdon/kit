package hoppers

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// playToTheEnd takes a night to its final standings. The racing is played
// in memory on the pure engine (microseconds), up to the last race's
// results; then the host's one press through the service finishes it, which
// is the transition the history and the ratings hang off.
func (f *fixture) playToTheEnd(game *Game) {
	f.t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	row, err := lockGame(f.ctx, tx, f.tenant.ID, game.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	g, err := engine.RestoreWith(row.Engine, f.svc.options(row))
	if err != nil {
		f.t.Fatal(err)
	}
	now := g.Now()
	for range 100000 {
		s := g.View(engine.RoleHost, "")
		if s.Phase == engine.PhaseRacing && s.Sub == engine.SubRaceEnd && s.RoundOver && s.LastRace {
			break
		}
		if s.Host.CanAdvance {
			_, _ = g.Apply(engine.Command{Type: engine.CmdHost}, now)
			continue
		}
		now = now.Add(time.Second)
		for _, p := range s.Players { // every phone stays in the room
			g.Seen(p.ID, now)
		}
		g.Advance(now)
	}
	if err := f.svc.store(f.ctx, tx, row, g); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	f.svc.cache.Delete(game.ID)
	f.mu.Lock()
	f.clock = now
	f.mu.Unlock()
	if s := f.advance(game); s.TV.Phase != engine.PhaseFinal {
		f.t.Fatalf("the last press left the night in %s", s.TV.Phase)
	}
}

// shortNight is a one-race night.
func (f *fixture) shortNight() *Game {
	f.t.Helper()
	g, err := f.svc.CreateGame(f.ctx, f.tenant.ID, nil, NewGame{Title: "Tuesday frogs", Pack: "default", Rules: RuleTweaks{Rounds: 1}})
	if err != nil {
		f.t.Fatal(err)
	}
	return g
}

// The night's standings go into the history when it ends, and stay there
// after the game itself is deleted.
func TestAFinishedNightIsRecorded(t *testing.T) {
	f := newFixture(t)
	game := f.shortNight()
	f.join(game, "Ann")
	f.playToTheEnd(game)

	results, err := ListResults(f.ctx, f.pool, f.tenant.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Tuesday frogs" || len(results[0].Standings) != 1 || results[0].Standings[0].Name != "Ann" {
		t.Fatalf("results = %+v", results)
	}
	f.sweep(game) // nothing new happens at the end: no second row
	if err := DeleteGame(f.ctx, f.pool, f.tenant.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	results, _ = ListResults(f.ctx, f.pool, f.tenant.ID, 10)
	if len(results) != 1 || results[0].GameID != nil {
		t.Fatalf("after delete: %+v", results)
	}
}

type fakePoster struct{ posts []string }

func (p *fakePoster) Post(_ context.Context, _ uuid.UUID, text string) error {
	p.posts = append(p.posts, text)
	return nil
}

// A rating is for the end of the night, from a phone that played it.
func TestFeedbackOnlyAtTheEnd(t *testing.T) {
	f := newFixture(t)
	game := f.shortNight()
	cookie := f.joinOverHTTP(game, "Ann")
	poster := &fakePoster{}
	send := func(c *http.Cookie) int {
		mux := http.NewServeMux()
		registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489", feedback: poster})
		rec := f.requestOn(mux, http.MethodPost, f.gamePath(game)+"/feedback", feedbackRequest{Stars: 4, Comment: "great <!channel>"}, c)
		return rec.Code
	}
	if code := send(cookie); code != http.StatusConflict {
		t.Fatalf("a rating mid-night got %d, want 409", code)
	}
	f.playToTheEnd(game)
	if code := send(nil); code != http.StatusUnauthorized {
		t.Fatalf("a spectator's rating got %d", code)
	}
	if code := send(cookie); code != http.StatusNoContent {
		t.Fatalf("a rating at the end got %d", code)
	}
	if len(poster.posts) != 1 || !strings.Contains(poster.posts[0], "Ann") || strings.Contains(poster.posts[0], "<!channel>") {
		t.Fatalf("posted %q", poster.posts)
	}
}
