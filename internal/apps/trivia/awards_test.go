package trivia

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The award pool is pure, so these are table fixtures with no database in
// sight -- the same bargain scoring_test.go makes.
//
// Times below are built off a fixed PAST base. Nothing here is about a
// particular calendar date, only about ordering within a round, and a past
// literal cannot become a time bomb the way a future one does.
var awardBase = time.Date(2020, 3, 14, 19, 0, 0, 0, time.UTC)

// awardGame is a small builder for an AwardInput, so a test reads as the
// night it is describing rather than as struct literals.
type awardGame struct {
	t      *testing.T
	in     AwardInput
	byName map[string]uuid.UUID
}

func newAwardGame(t *testing.T, names ...string) *awardGame {
	t.Helper()
	g := &awardGame{t: t, byName: map[string]uuid.UUID{}}
	for _, n := range names {
		id := uuid.New()
		g.byName[n] = id
		g.in.Teams = append(g.in.Teams, AwardTeam{ID: id, Name: n})
	}
	return g
}

func (g *awardGame) id(name string) uuid.UUID {
	g.t.Helper()
	id, ok := g.byName[name]
	if !ok {
		g.t.Fatalf("no team named %q in this fixture", name)
	}
	return id
}

// joinsAt moves a table's first eligible round, for the latecomer tests.
func (g *awardGame) joinsAt(name string, ordinal int) *awardGame {
	for i := range g.in.Teams {
		if g.in.Teams[i].ID == g.id(name) {
			g.in.Teams[i].EligibleFrom = ordinal
		}
	}
	return g
}

// round adds a scored round. answers maps a team name to what it typed; the
// cards are built from those the way BuildSlots would, deduped by value, and
// the winning card is resolved by closest-without-going-over.
func (g *awardGame) round(correct float64, answers map[string]float64) *awardRoundBuilder {
	r := AwardRound{
		Ordinal: len(g.in.Rounds), Correct: correct,
		CorrectText: FormatValue(correct), Deltas: map[uuid.UUID]ScoreDelta{},
	}
	byValue := map[float64]*AwardSlot{}
	// Walk the fixture's team order, not the map, so card ids are stable
	// across runs and a failing test says the same thing twice.
	for _, t := range g.in.Teams {
		v, ok := answers[t.Name]
		if !ok {
			continue
		}
		r.Answers = append(r.Answers, AwardAnswer{
			TeamID: t.ID, Value: v,
			SubmittedAt: awardBase.Add(time.Duration(len(r.Answers)) * time.Second),
		})
		if s, seen := byValue[v]; seen {
			s.TeamIDs = append(s.TeamIDs, t.ID)
			continue
		}
		val := v
		r.Slots = append(r.Slots, AwardSlot{ID: uuid.New(), Value: &val, TeamIDs: []uuid.UUID{t.ID}})
		byValue[v] = &r.Slots[len(r.Slots)-1]
	}
	best := -1
	for i, s := range r.Slots {
		if *s.Value <= correct && (best < 0 || *s.Value > *r.Slots[best].Value) {
			best = i
		}
	}
	if best >= 0 {
		r.WinningSlotID = r.Slots[best].ID
	}
	g.in.Rounds = append(g.in.Rounds, r)
	return &awardRoundBuilder{g: g, r: &g.in.Rounds[len(g.in.Rounds)-1]}
}

type awardRoundBuilder struct {
	g *awardGame
	r *AwardRound
}

// bet puts one chip from a team onto the card carrying `on`'s answer.
func (b *awardRoundBuilder) bet(team, on string, amount int) *awardRoundBuilder {
	s, ok := slotOf(*b.r, b.g.id(on))
	if !ok {
		b.g.t.Fatalf("%s wrote nothing in this round, so there is no card to bet on", on)
	}
	b.r.Bets = append(b.r.Bets, AwardBet{TeamID: b.g.id(team), SlotID: s.ID, Amount: amount})
	return b
}

// delta records what the round did to a table's score.
func (b *awardRoundBuilder) delta(team string, board, bet int) *awardRoundBuilder {
	b.r.Deltas[b.g.id(team)] = ScoreDelta{BoardPoints: board, BetDelta: bet}
	return b
}

func (b *awardRoundBuilder) final() *awardRoundBuilder {
	b.r.IsFinal = true
	return b
}

// fourRounds is a plain night that every structural test can run against.
func fourRounds(t *testing.T) *awardGame {
	t.Helper()
	g := newAwardGame(t, "Anvil", "Brick", "Cinder", "Dowel")
	g.round(100, map[string]float64{"Anvil": 100, "Brick": 90, "Cinder": 120, "Dowel": 80}).
		bet("Anvil", "Anvil", 100).bet("Brick", "Anvil", 100).
		bet("Cinder", "Cinder", 100).bet("Dowel", "Brick", 100).
		delta("Anvil", 100, 100).delta("Brick", 0, 100).delta("Cinder", 0, 0).delta("Dowel", 0, 0)
	g.round(50, map[string]float64{"Anvil": 40, "Brick": 50, "Cinder": 60, "Dowel": 45}).
		bet("Anvil", "Anvil", 100).bet("Brick", "Brick", 100).
		bet("Cinder", "Brick", 100).bet("Dowel", "Brick", 100).
		delta("Anvil", 0, 0).delta("Brick", 100, 100).delta("Cinder", 0, 100).delta("Dowel", 0, 100)
	g.round(200, map[string]float64{"Anvil": 150, "Brick": 210, "Cinder": 199, "Dowel": 100}).
		bet("Anvil", "Anvil", 100).bet("Brick", "Cinder", 100).
		bet("Cinder", "Cinder", 100).bet("Dowel", "Cinder", 100).
		delta("Anvil", 0, 0).delta("Brick", 0, 100).delta("Cinder", 100, 100).delta("Dowel", 0, 100)
	g.round(10, map[string]float64{"Anvil": 8, "Brick": 12, "Cinder": 9, "Dowel": 5}).
		bet("Anvil", "Anvil", 100).bet("Brick", "Cinder", 100).
		bet("Cinder", "Cinder", 100).bet("Dowel", "Anvil", 100).
		delta("Anvil", 0, 0).delta("Brick", 0, 100).delta("Cinder", 100, 100).delta("Dowel", 0, 0)
	return g
}

// One table, one award. This is the whole reason the pool is bigger than the
// number of slots.
func TestAwardsGiveEachTableAtMostOne(t *testing.T) {
	got := Awards(fourRounds(t).in)
	if len(got) == 0 {
		t.Fatal("a four-round night with four tables produced no awards at all")
	}
	seen := map[uuid.UUID]string{}
	for _, a := range got {
		if prev, dup := seen[a.TeamID]; dup {
			t.Errorf("%s holds both %q and %q; one table may hold at most one award", a.TeamName, prev, a.Title)
		}
		seen[a.TeamID] = a.Title
	}
}

func TestAwardsNeverExceedTheSlots(t *testing.T) {
	if got := len(Awards(fourRounds(t).in)); got > MaxAwards {
		t.Errorf("got %d awards, want at most %d", got, MaxAwards)
	}
}

// The count floats down rather than padding. Two tables cannot fill five
// slots, and repeating one to reach the number would undo the point.
func TestAwardsShrinkToTheRoom(t *testing.T) {
	g := newAwardGame(t, "Anvil", "Brick")
	g.round(100, map[string]float64{"Anvil": 100, "Brick": 90}).
		bet("Anvil", "Anvil", 100).bet("Brick", "Anvil", 100).
		delta("Anvil", 100, 100).delta("Brick", 0, 100)
	g.round(50, map[string]float64{"Anvil": 40, "Brick": 50}).
		bet("Anvil", "Brick", 100).bet("Brick", "Brick", 100).
		delta("Anvil", 0, 100).delta("Brick", 100, 100)
	g.round(20, map[string]float64{"Anvil": 19, "Brick": 25}).
		bet("Anvil", "Anvil", 100).bet("Brick", "Anvil", 100).
		delta("Anvil", 100, 100).delta("Brick", 0, 100)
	if got := len(Awards(g.in)); got > 2 {
		t.Errorf("got %d awards for a two-table room, want at most 2", got)
	}
}

// Go randomises map iteration, and ties in this pool are common. A podium
// that is re-rendered (a reload, a reconnect, a republished frame) must name
// the same tables every time, because the data behind it cannot change.
func TestAwardsAreStableAcrossRuns(t *testing.T) {
	in := fourRounds(t).in
	first := Awards(in)
	for i := range 50 {
		got := Awards(in)
		if len(got) != len(first) {
			t.Fatalf("run %d returned %d awards, first run returned %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j].Key != first[j].Key || got[j].TeamID != first[j].TeamID {
				t.Fatalf("run %d differs at %d: got %s/%s, want %s/%s",
					i, j, got[j].Key, got[j].TeamName, first[j].Key, first[j].TeamName)
			}
		}
	}
}

// Perfectly tied tables must resolve the same way every time, and the tie
// goes to whoever joined first.
func TestAwardTiesGoToTheEarlierTable(t *testing.T) {
	g := newAwardGame(t, "Anvil", "Brick", "Cinder")
	for range 4 {
		g.round(100, map[string]float64{"Anvil": 90, "Brick": 90, "Cinder": 200}).
			bet("Anvil", "Anvil", 100).bet("Brick", "Brick", 100).bet("Cinder", "Anvil", 100).
			delta("Anvil", 100, 100).delta("Brick", 100, 100).delta("Cinder", 0, 100)
	}
	scores := evalMostCorrect(g.in, newQualifier(g.in))
	if len(scores) < 2 {
		t.Fatalf("expected both tied tables to be scored, got %d", len(scores))
	}
	for i := range 50 {
		a, ok := resolve(awardDef{Key: "most_correct", Higher: true, Eval: evalMostCorrect}, g.in, newQualifier(g.in))
		if !ok {
			t.Fatal("a tied award resolved to nobody")
		}
		if a.TeamName != "Anvil" {
			t.Fatalf("run %d gave the tie to %s, want the earlier table Anvil", i, a.TeamName)
		}
	}
}

// The two chip-share awards are opposite ends of one axis, so they can never
// be the same table. This is the strongest distinctness property in the pool.
func TestChipShareAwardsCannotCollide(t *testing.T) {
	in := fourRounds(t).in
	q := newQualifier(in)
	own, _ := resolve(awardDef{Key: "backed_themselves", Higher: true, Eval: evalBackedThemselves}, in, q)
	other, _ := resolve(awardDef{Key: "trusted_room", Higher: true, Eval: evalTrustedRoom}, in, q)
	if own.TeamID == other.TeamID {
		t.Errorf("backed themselves and trusted the room both went to %s", own.TeamName)
	}
}

// A table that walked in near the end has a sample of one or two, and every
// average over it is noise.
func TestLatecomersCannotWinAwards(t *testing.T) {
	g := fourRounds(t).joinsAt("Dowel", 3)
	q := newQualifier(g.in)
	if q.OK(g.id("Dowel")) {
		t.Error("a table eligible from round 3 of a four-round night qualified for awards")
	}
	for _, a := range Awards(g.in) {
		if a.TeamName == "Dowel" {
			t.Errorf("latecomer Dowel won %q", a.Title)
		}
	}
}

// Wildness is judged against the room, so multiplying every number in a
// round by a thousand must not change who was wild. Raw distance fails this
// and would make the award a measure of which question had the big answer.
func TestWildestIsScaleFree(t *testing.T) {
	build := func(scale float64) []Award {
		g := newAwardGame(t, "Anvil", "Brick", "Cinder", "Dowel")
		for range 4 {
			g.round(55*scale, map[string]float64{
				"Anvil": 50 * scale, "Brick": 52 * scale,
				"Cinder": 48 * scale, "Dowel": 900000 * scale,
			}).delta("Anvil", 100, 0).delta("Brick", 0, 0).
				delta("Cinder", 0, 0).delta("Dowel", 0, 0)
		}
		q := newQualifier(g.in)
		a, ok := resolve(awardDef{Key: "wildest", Higher: true, Eval: evalWildest}, g.in, q)
		if !ok {
			t.Fatal("nobody won wildest guesses")
		}
		return []Award{a}
	}
	small, large := build(1), build(1000)
	if small[0].TeamName != "Dowel" {
		t.Errorf("wildest went to %s at scale 1, want Dowel", small[0].TeamName)
	}
	if large[0].TeamName != small[0].TeamName {
		t.Errorf("wildest went to %s at scale 1000 and %s at scale 1; it must not depend on scale",
			large[0].TeamName, small[0].TeamName)
	}
}

// A room in complete agreement has a spread of zero, which is a division by
// zero AND the single best moment of the night. It must not be skipped.
func TestWildestSurvivesAnAgreeingRoom(t *testing.T) {
	g := newAwardGame(t, "Anvil", "Brick", "Cinder", "Dowel")
	for range 4 {
		g.round(1969, map[string]float64{
			"Anvil": 1969, "Brick": 1969, "Cinder": 1969, "Dowel": 4000000,
		}).delta("Anvil", 100, 0).delta("Brick", 100, 0).
			delta("Cinder", 100, 0).delta("Dowel", 0, 0)
	}
	a, ok := resolve(awardDef{Key: "wildest", Higher: true, Eval: evalWildest}, g.in, newQualifier(g.in))
	if !ok {
		t.Fatal("a room that all wrote 1969 against one table on four million produced no wildest award")
	}
	if a.TeamName != "Dowel" {
		t.Errorf("wildest went to %s, want Dowel", a.TeamName)
	}
	if want := "The answer was 1,969. They said 4,000,000."; a.Detail != want {
		t.Errorf("detail = %q, want %q", a.Detail, want)
	}
}

// Closest without going over pays these tables nothing, every round. The
// award is the only thing that notices.
func TestSoCloseFindsTheNearestOvershoot(t *testing.T) {
	g := newAwardGame(t, "Anvil", "Brick", "Cinder")
	for range 4 {
		// Brick is nearest but over every time; Cinder is over by more.
		g.round(100, map[string]float64{"Anvil": 90, "Brick": 101, "Cinder": 400}).
			delta("Anvil", 100, 0).delta("Brick", 0, 0).delta("Cinder", 0, 0)
	}
	a, ok := resolve(awardDef{Key: "so_close", Higher: true, Eval: evalSoClose}, g.in, newQualifier(g.in))
	if !ok {
		t.Fatal("nobody won so close all night")
	}
	if a.TeamName != "Brick" {
		t.Errorf("so close went to %s, want Brick", a.TeamName)
	}
}

// The detail lines are read off a TV, so "1 times" is a shipping defect.
func TestAwardDetailsCountInEnglish(t *testing.T) {
	for _, tc := range []struct {
		n    int
		noun string
		want string
	}{
		{1, "time", "1 time"},
		{2, "time", "2 times"},
		{1, "chip", "1 chip"},
		{11, "question", "11 questions"},
	} {
		if got := countWord(tc.n, tc.noun); got != tc.want {
			t.Errorf("countWord(%d, %q) = %q, want %q", tc.n, tc.noun, got, tc.want)
		}
	}
}

// Titles carry whatever joke an award has; the line underneath states the
// fact and stops. The house copy skill bans both of these outright.
func TestAwardCopyHasNoBannedPunctuation(t *testing.T) {
	g := fourRounds(t)
	q := newQualifier(g.in)
	for _, def := range awardPool {
		a, ok := resolve(def, g.in, q)
		if !ok {
			continue
		}
		for _, s := range []string{a.Title, a.Detail} {
			for _, bad := range []string{"—", "–", "!"} {
				if contains(s, bad) {
					t.Errorf("award %s copy %q contains %q", def.Key, s, bad)
				}
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Awards are shown gentle to funny, which is a different order from the one
// they are selected in.
func TestAwardsComeBackInDisplayOrder(t *testing.T) {
	got := Awards(fourRounds(t).in)
	display := map[string]int{}
	for _, def := range awardPool {
		display[def.Key] = def.Display
	}
	for i := 1; i < len(got); i++ {
		if display[got[i-1].Key] > display[got[i].Key] {
			t.Errorf("award %d (%s) sorts after %d (%s)", i-1, got[i-1].Key, i, got[i].Key)
		}
	}
}

// Nerve is a fraction of your own bank, not an amount. The leader can stake
// more money than anybody while risking less of itself, and the award is
// about the risk.
func TestFinalWagerAwardsMeasureShareNotAmount(t *testing.T) {
	g := newAwardGame(t, "Anvil", "Brick", "Cinder")
	for range 3 {
		g.round(100, map[string]float64{"Anvil": 100, "Brick": 90, "Cinder": 80}).
			delta("Anvil", 1000, 0).delta("Brick", 100, 0).delta("Cinder", 100, 0)
	}
	// Anvil stakes more money but a tenth of its bank; Brick stakes less
	// money and nearly all of its own.
	g.round(50, map[string]float64{"Anvil": 50, "Brick": 40, "Cinder": 30}).
		final().bet("Anvil", "Anvil", 300).bet("Brick", "Brick", 280).bet("Cinder", "Cinder", 30).
		delta("Anvil", 0, 300).delta("Brick", 0, -280).delta("Cinder", 0, -30)

	q := newQualifier(g.in)
	bold, ok := resolve(awardDef{Key: "went_for_it", Higher: true, Eval: evalWentForIt}, g.in, q)
	if !ok {
		t.Fatal("nobody won went for it")
	}
	if bold.TeamName != "Brick" {
		t.Errorf("went for it went to %s, want Brick, who risked most of its own bank", bold.TeamName)
	}
	safe, ok := resolve(awardDef{Key: "played_safe", Higher: false, Eval: evalPlayedSafe}, g.in, q)
	if !ok {
		t.Fatal("nobody won played it safe")
	}
	if safe.TeamID == bold.TeamID {
		t.Error("went for it and played it safe are the same table")
	}
}

// A night nobody played has no honourable mentions in it, and must not panic
// trying to find some.
func TestAwardsHandleAnEmptyNight(t *testing.T) {
	if got := Awards(AwardInput{}); len(got) != 0 {
		t.Errorf("an empty game produced %d awards", len(got))
	}
	g := newAwardGame(t, "Anvil", "Brick")
	if got := Awards(g.in); len(got) != 0 {
		t.Errorf("a game with tables but no rounds produced %d awards", len(got))
	}
}
