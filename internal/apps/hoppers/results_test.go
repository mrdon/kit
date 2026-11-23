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

// playToTheEnd runs a one-race night to its final standings, pressing the
// host button whenever the engine offers it and keeping the people's phones
// pinging so they auto-hop rather than drop out.
func (f *fixture) playToTheEnd(game *Game, people ...*Player) {
	f.t.Helper()
	for range 400 {
		s := f.snap(game)
		if s.TV.Phase == engine.PhaseFinal {
			return
		}
		if s.Host.CanAdvance {
			f.advance(game)
			continue
		}
		f.wait(time.Second)
		for _, p := range people {
			if err := f.svc.Heard(f.ctx, f.tenant.ID, game.ID, p.ID, f.snap(game)); err != nil {
				f.t.Fatal(err)
			}
		}
		f.sweep(game)
	}
	f.t.Fatalf("the night never finished: %s/%s", f.snap(game).TV.Phase, f.snap(game).TV.Sub)
}

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
	ann := f.join(game, "Ann")
	f.playToTheEnd(game, ann)

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
	players, _ := ListPlayers(f.ctx, f.pool, f.tenant.ID, game.ID)
	f.playToTheEnd(game, players...)
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
