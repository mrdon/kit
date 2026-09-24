package trivia

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/buildinfo"
)

// serveMux builds a mux with the app's public routes, plus a stand-in for the
// cards PWA catch-all, so requests run through the same routing production
// uses rather than being dispatched to a handler directly.
func (f *fixture) serveMux() *http.ServeMux {
	mux := http.NewServeMux()
	// GET /{slug}/ is the cards PWA catch-all in production. Registering a
	// stand-in here is the point of the routing test below: the failure mode
	// is the game page silently serving the card feed, which is exactly the
	// bug documented in vault/urls.go.
	mux.HandleFunc("GET /{slug}/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Served-By", "cards-catch-all")
		_, _ = w.Write([]byte("card feed"))
	})
	registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489"})
	return mux
}

func (f *fixture) request(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	f.serveMux().ServeHTTP(rec, req)
	return rec
}

func (f *fixture) gamePath(game *Game) string {
	return "/" + f.tenant.Slug + "/trivia/" + game.Name
}

// joinOverHTTP joins and returns the identity cookie the server issued.
func (f *fixture) joinOverHTTP(game *Game, name string) *http.Cookie {
	f.t.Helper()
	rec := f.request(http.MethodPost, f.gamePath(game)+"/join", joinRequest{Name: name}, nil)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("join returned %d: %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == TeamCookieName {
			return c
		}
	}
	f.t.Fatal("join issued no identity cookie")
	return nil
}

// THE ROUTING TEST. GET /{slug}/ is a catch-all served by the cards PWA, and
// Go 1.22's mux gives these longer literal patterns priority — but nothing
// else in the tree checks that, and the failure mode is the game page
// silently serving the card feed to a room full of phones.
func TestGameRoutesBeatTheCardsCatchAll(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	paths := []string{
		f.gamePath(game),
		f.gamePath(game) + "/tv",
		f.gamePath(game) + "/me",
		f.gamePath(game) + "/state",
	}
	for _, p := range paths {
		rec := f.request(http.MethodGet, p, nil, nil)
		if rec.Header().Get("X-Served-By") == "cards-catch-all" {
			t.Fatalf("%s was swallowed by the cards catch-all", p)
		}
	}
	// And the catch-all still serves everything else under the slug.
	rec := f.request(http.MethodGet, "/"+f.tenant.Slug+"/something-else", nil, nil)
	if rec.Header().Get("X-Served-By") != "cards-catch-all" {
		t.Fatal("the trivia routes stole a path that belongs to the cards SPA")
	}
}

// Spectator mode is a hard requirement: somebody who opens the URL with no
// cookie gets the full read-only view, including the stream. A 401 here would
// make the public URL useless to everyone not already playing.
func TestNoCookieCanStreamButCannotPlay(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())

	if rec := f.request(http.MethodGet, f.gamePath(game)+"/state", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("spectator poll returned %d, want 200", rec.Code)
	}
	var frame PlayerFrame
	if err := json.NewDecoder(strings.NewReader(
		f.request(http.MethodGet, f.gamePath(game)+"/state", nil, nil).Body.String())).Decode(&frame); err != nil {
		t.Fatalf("decoding spectator frame: %v", err)
	}
	if frame.You != nil {
		t.Fatal("a spectator frame carries a private `you` block")
	}

	// But writing needs an identity.
	rec := f.request(http.MethodPost, f.gamePath(game)+"/answer", answerRequest{Answer: "42"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("answering with no cookie returned %d, want 401", rec.Code)
	}
	rec = f.request(http.MethodPut, f.gamePath(game)+"/bets", betRequest{Chip: 0}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("betting with no cookie returned %d, want 401", rec.Code)
	}
}

// Another team's cookie cannot submit your answer.
func TestAnotherTeamsCookieCannotActAsYou(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	cookieA := f.joinOverHTTP(game, "Bar Flies")
	cookieB := f.joinOverHTTP(game, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	// B answers with B's cookie. The answer must land on B, not on A.
	if rec := f.request(http.MethodPost, f.gamePath(game)+"/answer",
		answerRequest{Answer: "77"}, cookieB); rec.Code != http.StatusOK {
		t.Fatalf("B's answer returned %d: %s", rec.Code, rec.Body.String())
	}
	g := f.reload(game.ID)
	answers, err := ListAnswers(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 {
		t.Fatalf("%d answers recorded, want 1", len(answers))
	}
	teamIDA, _, _ := ParseCookieValue(cookieA.Value)
	if answers[0].TeamID == teamIDA {
		t.Fatal("B's cookie submitted an answer for A")
	}

	// A cookie whose token has been tampered with resolves to nobody.
	tampered := &http.Cookie{Name: TeamCookieName, Value: CookieValue(teamIDA, "not-the-token")}
	if rec := f.request(http.MethodPost, f.gamePath(game)+"/answer",
		answerRequest{Answer: "1"}, tampered); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a forged cookie returned %d, want 401", rec.Code)
	}
}

// The cookie is scoped to one game, so a cookie minted for game A is not an
// identity in game B.
func TestCookieFromAnotherGameIsNotAnIdentity(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	gameA := f.newGame(defaultSettings(), topicSet())
	gameB := f.newGame(defaultSettings(), topicSet())
	cookieA := f.joinOverHTTP(gameA, "Bar Flies")

	rec := f.request(http.MethodGet, f.gamePath(gameB)+"/me", nil, cookieA)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("game A's cookie resolved to an identity in game B (%d: %s)", rec.Code, rec.Body.String())
	}
}

// Both chips on one answer goes through over HTTP, and BOTH pay when that
// card wins. The stacking rule is only half-shipped if the second chip is
// accepted and then quietly ignored by scoring.
func TestHTTPAcceptsBothChipsOnOneAnswer(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	cookie := f.joinOverHTTP(game, "Bar Flies")
	f.joinOverHTTP(game, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	teamID, _, _ := ParseCookieValue(cookie.Value)
	_ = f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, teamID, "10")
	teams, _ := ListTeams(f.ctx, f.pool, f.tenant.ID, game.ID)
	for _, t2 := range teams {
		if t2.ID != teamID {
			_ = f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, t2.ID, "20")
		}
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	// Every seeded answer is above 100, so both guesses undershoot and the
	// higher card (20) is the one that wins. Stack both chips on it.
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	target := snap.Slots[len(snap.Slots)-1].ID.String()
	for chip := range 2 {
		rec := f.request(http.MethodPut, f.gamePath(game)+"/bets",
			betRequest{Chip: chip, SlotID: &target}, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("chip %d returned %d: %s", chip, rec.Code, rec.Body.String())
		}
	}

	f.do(game.ID, ActionRequest{Action: ActionScore, FromPhase: PhaseBetting})
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if snap.Scoring == nil {
		t.Fatal("the round scored but the snapshot carries no scoring block")
	}
	if got := snap.Scoring.Deltas[teamID].BetDelta; got != 300 {
		t.Fatalf("stacked chips paid %d, want 300 — both the $100 and the $200", got)
	}
	if got := snap.Standings[teamID]; got != 300 {
		t.Fatalf("standing = %d after a stacked win, want 300", got)
	}
}

// The wager endpoint over HTTP: refused outside the phase, clamped inside it,
// and never reachable without a cookie.
//
// The 409 is the mechanic rather than a validation rule. Once the question is
// on the wall the amount is frozen, and a phone that missed the transition has
// to be told so rather than quietly having its bet accepted late.
func TestHTTPWagerOnlyInsideTheWagerPhase(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(oneCellSettings(), []string{"space"})
	cookie := f.joinOverHTTP(game, "Bar Flies")
	f.joinOverHTTP(game, "Quiz Khalifa")
	teamID, _, _ := ParseCookieValue(cookie.Value)
	teams, _ := ListTeams(f.ctx, f.pool, f.tenant.ID, game.ID)
	var a, b *Team
	for i := range teams {
		if teams[i].ID == teamID {
			a = &teams[i]
		} else {
			b = &teams[i]
		}
	}

	path := f.gamePath(game) + "/wager"
	// Lobby: nothing to bet on.
	if rec := f.request(http.MethodPut, path, wagerRequest{Amount: 100}, cookie); rec.Code != http.StatusConflict {
		t.Fatalf("wagering from the lobby returned %d, want 409", rec.Code)
	}
	// No cookie at all is a 401, not a silent no-op.
	if rec := f.request(http.MethodPut, path, wagerRequest{Amount: 100}, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wager with no identity returned %d, want 401", rec.Code)
	}

	f.openFinal(game, a, b)
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	bank := snap.Standings[a.ID]
	if bank <= 0 {
		t.Fatalf("the winner's bank is %d — nothing to wager", bank)
	}

	// Ten times the bank is CLAMPED, not rejected: a table must not lose its
	// final to a typo.
	rec := f.request(http.MethodPut, path, wagerRequest{Amount: bank * 10}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("wager returned %d: %s", rec.Code, rec.Body.String())
	}
	var frame PlayerFrame
	if err := json.Unmarshal(rec.Body.Bytes(), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.You == nil || frame.You.Stake == nil || *frame.You.Stake != bank {
		t.Fatalf("the phone was told its wager is %+v, want it clamped to %d", frame.You, bank)
	}
	// And the response is still the wager phase's frame: a category, no
	// question.
	if frame.Round == nil || frame.Round.Text != "" || frame.Round.Category == "" {
		t.Fatalf("the wager response leaked the question: %+v", frame.Round)
	}

	// The host asks the question; the bet is now frozen.
	f.do(game.ID, ActionRequest{Action: ActionAsk, FromPhase: PhaseWager})
	if rec := f.request(http.MethodPut, path, wagerRequest{Amount: 0}, cookie); rec.Code != http.StatusConflict {
		t.Fatalf("wagering after the question went up returned %d, want 409", rec.Code)
	}
}

// The twenty-first team is refused over HTTP with a 409 and a readable
// reason, not a 500.
func TestHTTPRefusesTheTwentyFirstTeam(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	for i := range MaxTeams {
		f.joinOverHTTP(game, "Table "+FormatValue(float64(i)))
	}
	rec := f.request(http.MethodPost, f.gamePath(game)+"/join", joinRequest{Name: "One Too Many"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("the 21st join returned %d, want 409", rec.Code)
	}
}

// A phone that scans the QR during the final gets a 400 and a sentence it can
// put on screen, not a 500 and not a seat it cannot use.
func TestHTTPRefusesAJoinOnceTheFinalBegins(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	game := f.newGame(s, []string{"space"})
	a := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, map[uuid.UUID]string{a.ID: "10"})
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	f.do(game.ID, ActionRequest{Action: ActionFinal, FromPhase: PhaseIntermission})

	rec := f.request(http.MethodPost, f.gamePath(game)+"/join", joinRequest{Name: "Latecomers"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a join during the final returned %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "this game is closing") {
		t.Fatalf("the refusal body is %q, which does not tell the phone why", rec.Body.String())
	}
}

// A game in one workspace is not reachable from another workspace's URL.
func TestGameIsNotReachableFromAnotherTenantsSlug(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t)
	game := f.newGame(defaultSettings(), nil)

	rec := f.request(http.MethodGet, "/"+other.tenant.Slug+"/trivia/"+game.Name+"/state", nil, nil)
	if rec.Code == http.StatusOK {
		t.Fatal("a game was served under another workspace's slug")
	}
}

// A path-shaped or unknown game name 404s rather than reaching a query.
func TestUnknownAndMalformedGameNames(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"brave-otter-lamp", "not-a-game", "..", "x"} {
		rec := f.request(http.MethodGet, "/"+f.tenant.Slug+"/trivia/"+name+"/state", nil, nil)
		if rec.Code == http.StatusOK {
			t.Fatalf("%q resolved to a game", name)
		}
	}
}

// The identity cookie must be HttpOnly and scoped to this game's path, so one
// phone can hold two games at once and no script can read the token.
func TestIdentityCookieIsScopedAndHttpOnly(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	c := f.joinOverHTTP(game, "Bar Flies")

	if !c.HttpOnly {
		t.Fatal("the identity cookie is readable from JS")
	}
	if want := CookiePath(f.tenant.Slug, game.Name); c.Path != want {
		t.Fatalf("cookie path = %q, want %q", c.Path, want)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax — the phone arrives from a QR scan", c.SameSite)
	}
	teamID, token, ok := ParseCookieValue(c.Value)
	if !ok || teamID == uuid.Nil || token == "" {
		t.Fatalf("cookie value %q does not parse", c.Value)
	}
	// Only the hash is stored.
	var stored string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT token_hash FROM app_trivia_teams WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, teamID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("the raw token is stored in the database")
	}
	if stored != HashToken(token) {
		t.Fatal("the stored value is not the token's hash")
	}
}

// The TV page renders self-contained: fonts inlined, no reference to the
// console bundle, and no meta-refresh that would kill the stream.
func TestDisplayPageIsSelfContained(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	rec := f.request(http.MethodGet, f.gamePath(game)+"/tv", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tv page returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"data:font/woff2;base64,", "<svg", "__KIT_TRIVIA_STREAM__", game.Name} {
		if !strings.Contains(body, want) {
			t.Fatalf("tv page is missing %q", want)
		}
	}
	if strings.Contains(body, "/console/assets/") {
		t.Fatal("the tv page depends on the console bundle being reachable")
	}
	// html/template rewrites data: URIs in the wrong context to #ZgotmplZ,
	// which paints a blank wall with nothing in the logs.
	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("html/template neutered a typed field — the page would render blank")
	}
	if strings.Contains(body, "http-equiv=\"refresh\"") {
		t.Fatal("a meta refresh would tear down the SSE stream mid-question")
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

// The host-issued reclaim code is the only re-entry path, and it is
// single-use.
func TestReclaimCodeWorksOnceAndRotatesTheIdentity(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	old := f.joinOverHTTP(game, "Bar Flies")
	teamID, _, _ := ParseCookieValue(old.Value)

	code := ReclaimCode()
	if err := f.svc.IssueReclaim(f.ctx, f.tenant.ID, game.ID, teamID, code); err != nil {
		t.Fatal(err)
	}
	// Issuing a code invalidates the old cookie: the phone it was on is gone.
	if rec := f.request(http.MethodGet, f.gamePath(game)+"/me", nil, old); rec.Code != http.StatusNoContent {
		t.Fatal("the old cookie still works after a reclaim was issued")
	}

	rec := f.request(http.MethodPost, f.gamePath(game)+"/reclaim",
		reclaimRequest{TeamID: teamID.String(), Code: code}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeeming the code returned %d: %s", rec.Code, rec.Body.String())
	}
	// The code is burned on use: four digits read out loud must not stay live.
	again := f.request(http.MethodPost, f.gamePath(game)+"/reclaim",
		reclaimRequest{TeamID: teamID.String(), Code: code}, nil)
	if again.Code == http.StatusOK {
		t.Fatal("the reclaim code was accepted twice")
	}
	// A wrong code never works.
	bad := f.request(http.MethodPost, f.gamePath(game)+"/reclaim",
		reclaimRequest{TeamID: teamID.String(), Code: "0000"}, nil)
	if bad.Code == http.StatusOK {
		t.Fatal("a wrong reclaim code was accepted")
	}
}

// The stable TV address: point a screen at it once and it follows the newest
// game, so nobody has to retype a URL at the TV each week.
func TestStableTVAddressFollowsTheNewestGame(t *testing.T) {
	f := newFixture(t)

	// Before any game exists it must still answer, and say so readably.
	rec := f.request(http.MethodGet, "/"+f.tenant.Slug+"/trivia/tv", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty workspace returned %d, want 200", rec.Code)
	}
	// "No game yet", not "No quiz tonight": this screen goes up on the wall
	// BEFORE the night is ready, so it must not read as a cancellation to a
	// room that is about to play.
	if !strings.Contains(rec.Body.String(), "No game yet") {
		t.Fatal("no placeholder for a workspace with no games")
	}
	// "empty" plus the build: a screen parked on the stable address with no
	// game yet must notice both the first game AND a deploy.
	if v := f.request(http.MethodGet, "/"+f.tenant.Slug+"/trivia/tv.version", nil, nil); !strings.HasPrefix(v.Body.String(), "empty-") {
		t.Fatalf("version = %q, want it to start with empty-", v.Body.String())
	}

	first := f.newGame(defaultSettings(), nil)
	rec = f.request(http.MethodGet, "/"+f.tenant.Slug+"/trivia/tv", nil, nil)
	if !strings.Contains(rec.Body.String(), first.Name) {
		t.Fatal("the stable address does not show the only game")
	}
	v1 := f.request(http.MethodGet, "/"+f.tenant.Slug+"/trivia/tv.version", nil, nil).Body.String()

	second := f.newGame(defaultSettings(), nil)
	rec = f.request(http.MethodGet, "/"+f.tenant.Slug+"/trivia/tv", nil, nil)
	if !strings.Contains(rec.Body.String(), second.Name) {
		t.Fatal("the stable address did not follow the newer game")
	}
	v2 := f.request(http.MethodGet, "/"+f.tenant.Slug+"/trivia/tv.version", nil, nil).Body.String()
	if v1 == v2 {
		t.Fatal("the version did not change when a newer game appeared — a parked screen would never reload")
	}
}

// The stamp must NOT move while a game is being played. It covers the
// rendered chrome, not the live state, or a screen would reload itself in the
// middle of a question — several times a round.
func TestTVVersionIsStableWhileAGameIsPlayed(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	path := "/" + f.tenant.Slug + "/trivia/" + game.Name + "/tv.version"
	before := f.request(http.MethodGet, path, nil, nil).Body.String()

	a := f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "42"); err != nil {
		t.Fatal(err)
	}

	after := f.request(http.MethodGet, path, nil, nil).Body.String()
	if before != after {
		t.Fatalf("the version moved during play (%s -> %s) — the TV would reload mid-question",
			before, after)
	}

	// But renaming the night IS a change the rendered page has to pick up.
	if _, err := UpdateSettings(f.ctx, f.pool, f.tenant.ID, game.ID, Settings{
		Title: "Renamed Night", BoardRows: 2, BoardColumns: 5,
		CellValues: []int{100, 200}, TokenValues: []int{100, 200},
		AnswerSeconds: 60, RevealSeconds: 15, BetSeconds: 45, WagerSeconds: 30,
	}); err != nil {
		t.Fatal(err)
	}
	renamed := f.request(http.MethodGet, path, nil, nil).Body.String()
	if renamed == after {
		t.Fatal("renaming the night did not change the version — the TV would show the old name")
	}
}

// The host's title is what the TV shows in the corner, not the URL slug.
func TestDisplayShowsTheHostsTitle(t *testing.T) {
	f := newFixture(t)
	s := defaultSettings()
	s.Title = "Tuesday Quiz"
	game := f.newGame(s, nil)

	body := f.request(http.MethodGet, f.gamePath(game)+"/tv", nil, nil).Body.String()
	if !strings.Contains(body, "Tuesday Quiz") {
		t.Fatal("the TV page does not carry the name the host set in setup")
	}
	if !strings.Contains(body, `>Tuesday Quiz<`) {
		t.Fatal("the corner heading is not the host's title")
	}
	// THE SLUG MUST NOT BE PRESENTED AS A NAME. It appears inside the join
	// URL, which is unavoidable and fine, but nowhere as a label — it used to
	// be the biggest text on the wall.
	for word := range strings.SplitSeq(game.Name, "-") {
		if strings.Contains(body, `class="join-word">`+strings.ToUpper(word)) {
			t.Fatalf("the slug word %q is rendered as the join screen's hero", word)
		}
	}
}

// A game always has a human name, so no surface ever has to fall back to the
// slug — a fallback is a branch every surface has to remember, and one always
// forgets.
func TestGamesAreAlwaysCreatedWithATitle(t *testing.T) {
	f := newFixture(t)
	app := &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489"}

	mux := http.NewServeMux()
	tenantMW := auth.TenantFromPath(f.pool)
	mux.Handle("POST /{slug}/api/trivia/games", tenantMW(http.HandlerFunc(app.handleCreateGame)))

	req := httptest.NewRequest(http.MethodPost,
		"/"+f.tenant.Slug+"/api/trivia/games", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body.String())
	}
	var out gameJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.Title) == "" {
		t.Fatal("a game was created with no title — every surface would fall back to the slug")
	}
	if out.Title == out.Name {
		t.Fatal("the title is the slug")
	}
}

// The console must serve the STABLE screen address, not just the per-game
// one. Advertising only the per-game URL is what sends a host to retype
// something at the television every week.
func TestGameJSONCarriesTheStableScreenURL(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	app := &App{pool: f.pool, svc: f.svc, baseURL: "https://kit.example.com"}

	j := app.gameToJSON(game, f.tenant.Slug, 0, 0, 0, "")
	want := "https://kit.example.com/" + f.tenant.Slug + "/trivia/tv"
	if j.ScreenURL != want {
		t.Fatalf("screen_url = %q, want %q", j.ScreenURL, want)
	}
	// And it must NOT be the per-game one, which is the bug this guards.
	if j.ScreenURL == j.TVURL {
		t.Fatal("the stable screen URL is the same as the per-game one")
	}
	if !strings.HasSuffix(j.TVURL, "/trivia/"+game.Name+"/tv") {
		t.Fatalf("tv_url = %q, want it to pin this game", j.TVURL)
	}
}

// The short join link. It exists to make the QR scannable from across a room,
// so the thing worth testing is that it resolves — and that it cannot be used
// to reach a workspace that has trivia switched off, since this is the one
// public route with no {slug} for the enablement gate to wrap.
func TestShortJoinLinkResolvesToTheGame(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	if game.JoinCode == "" {
		t.Fatal("a new game has no join code")
	}
	if !IsValidJoinCode(game.JoinCode) {
		t.Fatalf("generated code %q does not validate", game.JoinCode)
	}

	mux := http.NewServeMux()
	registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489"})

	req := httptest.NewRequest(http.MethodGet, "/j/t/"+game.JoinCode, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body %q)", rec.Code, rec.Body.String())
	}
	want := "/" + f.tenant.Slug + "/trivia/" + game.Name
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	// A cached redirect would pin a phone to a game that has finished.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

// Junk and unknown codes 404 rather than reaching a query.
func TestShortJoinLinkRejectsJunk(t *testing.T) {
	f := newFixture(t)
	f.newGame(defaultSettings(), nil)
	mux := http.NewServeMux()
	registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489"})

	for _, code := range []string{"zzzzz", "../../etc", "AAAAA", "ab", "iloux", strings.Repeat("a", 40)} {
		req := httptest.NewRequest(http.MethodGet, "/j/t/"+code, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusFound {
			t.Errorf("%q resolved to a game", code)
		}
	}
}

// Codes are drawn from an alphabet with the characters that get misread
// stripped out — a code is read aloud across a bar when a camera will not
// focus, and "was that an i or a 1" is the failure that causes.
func TestJoinCodesAvoidAmbiguousCharacters(t *testing.T) {
	for range 200 {
		code := NewJoinCode()
		if len(code) != joinCodeLength {
			t.Fatalf("code %q is not %d characters", code, joinCodeLength)
		}
		for _, bad := range []string{"i", "l", "o", "u"} {
			if strings.Contains(code, bad) {
				t.Fatalf("code %q contains the ambiguous character %q", code, bad)
			}
		}
	}
}

// A deploy has to reach a wall that is already switched on, which is the
// whole point of putting the build in the token: a fix shipped at question
// four is no use to the screen it is fixing if that screen only repaints when
// somebody creates a new game.
func TestDisplayVersionChangesWithTheBuild(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)

	before := displayVersion(game)
	restore := buildinfo.Commit
	buildinfo.Commit = "deadbee"
	defer func() { buildinfo.Commit = restore }()
	after := displayVersion(game)

	if before == after {
		t.Errorf("the same game reported %q across two builds; a deploy would never reach an open screen", before)
	}
	// The game half still has to move on its own, or a screen on the stable
	// address would stop following the newest game.
	if displayVersion(f.newGame(defaultSettings(), nil)) == after {
		t.Error("two different games reported the same version on one build")
	}
	// And nothing is lost when there is no game at all.
	if displayVersion(nil) == "" {
		t.Error("an empty workspace reported no version")
	}
}
