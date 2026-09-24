package trivia

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakePoster records what would have gone to Slack, or fails with err.
type fakePoster struct {
	posts []string
	err   error
}

func (p *fakePoster) post(_ context.Context, _ uuid.UUID, text string) error {
	if p.err != nil {
		return p.err
	}
	p.posts = append(p.posts, text)
	return nil
}

func (f *fixture) feedbackRequest(game *Game, poster feedbackPoster, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	mux := http.NewServeMux()
	registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489", feedback: poster})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, f.gamePath(game)+"/feedback", strings.NewReader(mustJSON(f.t, body)))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) toPodium(game *Game) *Game {
	f.t.Helper()
	if _, err := SetPhaseUnconditional(f.ctx, f.pool, f.tenant.ID, game.ID, PhasePodium, nil, nil); err != nil {
		f.t.Fatalf("moving to the podium: %v", err)
	}
	return f.reload(game.ID)
}

// A rating is for a game that is over, from a table that played it.
func TestFeedbackOnlyFromATableOnceTheGameEnds(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	cookie := f.joinOverHTTP(game, "Bar Flies")
	poster := &fakePoster{}

	if rec := f.feedbackRequest(game, poster, feedbackRequest{Stars: 5}, cookie); rec.Code != http.StatusConflict {
		t.Fatalf("rating from the lobby returned %d, want 409", rec.Code)
	}
	game = f.toPodium(game)
	if rec := f.feedbackRequest(game, poster, feedbackRequest{Stars: 5}, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a rating with no identity returned %d, want 401", rec.Code)
	}
	for _, stars := range []int{0, 6} {
		if rec := f.feedbackRequest(game, poster, feedbackRequest{Stars: stars}, cookie); rec.Code != http.StatusBadRequest {
			t.Fatalf("%d stars returned %d, want 400", stars, rec.Code)
		}
	}
	long := strings.Repeat("x", maxFeedbackRunes+1)
	if rec := f.feedbackRequest(game, poster, feedbackRequest{Stars: 3, Comment: long}, cookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("an over-long comment returned %d, want 400", rec.Code)
	}
	if len(poster.posts) != 0 {
		t.Fatalf("refused ratings reached Slack: %+v", poster)
	}
}

// A rating is one Slack message: the stars, then the comment quoted.
func TestFeedbackPostsStarsAndComment(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	cookie := f.joinOverHTTP(game, "Bar Flies")
	game = f.toPodium(game)
	poster := &fakePoster{}

	req := feedbackRequest{Stars: 4, Comment: "  Great night\nmore music please  "}
	if rec := f.feedbackRequest(game, poster, req, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("rating returned %d: %s", rec.Code, rec.Body.String())
	}
	if len(poster.posts) != 1 {
		t.Fatalf("got %d posts, want 1", len(poster.posts))
	}
	got := poster.posts[0]
	for _, want := range []string{"★★★★☆", "*Bar Flies*", "4/5", "\n> Great night\n> more music please"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q is missing %q", got, want)
		}
	}
}

// A rating that does not reach Slack is never the table's problem: a Slack
// failure and a workspace with no channel both answer the phone with success.
func TestFeedbackSlackFailureIsHiddenFromThePhone(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	cookie := f.joinOverHTTP(game, "Bar Flies")
	game = f.toPodium(game)

	for _, err := range []error{errors.New("slack is down"), errFeedbackOff} {
		poster := &fakePoster{err: err}
		if rec := f.feedbackRequest(game, poster, feedbackRequest{Stars: 2}, cookie); rec.Code != http.StatusNoContent {
			t.Fatalf("post failing with %q returned %d, want 204", err, rec.Code)
		}
	}
}

// No channel is picked until an admin picks one, and then it is that
// workspace's alone.
func TestFeedbackChannelHasNoDefault(t *testing.T) {
	f := newFixture(t)
	c, err := GetFeedbackChannel(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Off() {
		t.Fatalf("a fresh workspace posts to %+v, want nowhere", c)
	}
	if err := SaveFeedbackChannel(f.ctx, f.pool, f.tenant.ID, FeedbackChannel{ID: "C9", Name: "quiz"}); err != nil {
		t.Fatal(err)
	}
	if c, _ = GetFeedbackChannel(f.ctx, f.pool, f.tenant.ID); c.ID != "C9" || c.Name != "quiz" {
		t.Fatalf("saved channel read back as %+v", c)
	}
	other := newFixture(t)
	if c, _ = GetFeedbackChannel(other.ctx, other.pool, other.tenant.ID); !c.Off() {
		t.Fatalf("another workspace sees %+v", c)
	}
	// And with nothing picked, the real poster declines before touching Slack.
	s := &slackFeedback{pool: other.pool}
	if err := s.post(other.ctx, other.tenant.ID, "x"); !errors.Is(err, errFeedbackOff) {
		t.Fatalf("posting with no channel returned %v, want errFeedbackOff", err)
	}
}

// Names and comments are typed by strangers; Slack markup arrives as text.
func TestFeedbackTextEscapesSlackMarkup(t *testing.T) {
	got := feedbackText("Tuesday <quiz>", "<!channel> & co", 1, "hi <@U123>")
	for _, want := range []string{"★☆☆☆☆", "&lt;!channel&gt; &amp; co", "Tuesday &lt;quiz&gt;", "> hi &lt;@U123&gt;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q is missing %q", got, want)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
