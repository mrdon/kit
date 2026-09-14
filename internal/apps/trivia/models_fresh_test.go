package trivia

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// noRepeats is the SHIPPED rule, which the shared fixture default turns off
// so the scoring and phase tests can run several games off one small bank.
// Everything in this file is about the rule itself, so it opts back in.
func noRepeats() Settings {
	s := defaultSettings()
	s.RepeatQuestions = false
	return s
}

// oneCellNoRepeats is a whole game in a single question, which is all a test
// about the bank needs to spend one.
func oneCellNoRepeats() Settings {
	s := noRepeats()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	return s
}

// playEveryCell runs the game out: every cell opened, asked and scored, so
// every question on the board has a round row behind it.
func (f *fixture) playEveryCell(game *Game) {
	f.t.Helper()
	f.join(game.ID, "The Only Table")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	for {
		f.playOneRound(game, nil)
		if f.unplayedCells(game) == 0 {
			return
		}
		f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	}
}

// playOneCell opens exactly ONE cell of a board and stops, which is the state
// the repeat rule has to get right: nine questions the room never heard,
// sitting under a board that has been and gone. Returns what was asked.
func (f *fixture) playOneCell(game *Game) uuid.UUID {
	f.t.Helper()
	f.join(game.ID, "The Only Table")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, nil)

	var asked uuid.UUID
	cells, err := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		f.t.Fatalf("ListBoardCells: %v", err)
	}
	for _, c := range cells {
		if c.PlayedAt != nil {
			asked = c.QuestionID
		}
	}
	if asked == uuid.Nil {
		f.t.Fatal("no cell was played")
	}
	return asked
}

func (f *fixture) unplayedCells(game *Game) int {
	f.t.Helper()
	cells, err := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		f.t.Fatalf("ListBoardCells: %v", err)
	}
	n := 0
	for _, c := range cells {
		if c.PlayedAt == nil {
			n++
		}
	}
	return n
}

// seedShared writes ONE named question into its own dataset, for the tests
// about the same question living in two packs.
func (f *fixture) seedShared(setName, prompt, topic string) uuid.UUID {
	f.t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		f.t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	datasetID, err := UpsertDataset(f.ctx, tx, f.tenant.ID, setName, "", "")
	if err != nil {
		f.t.Fatalf("creating dataset: %v", err)
	}
	if _, _, err := UpsertQuestion(f.ctx, tx, f.tenant.ID, datasetID, Question{
		Prompt: prompt, PromptKey: FoldKey(prompt),
		AnswerValue: 464, AnswerText: FormatValue(464),
		Topics: []Topic{{Key: FoldKey(topic), Label: topic}},
	}); err != nil {
		f.t.Fatalf("seeding question: %v", err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		f.t.Fatalf("commit: %v", err)
	}
	return datasetID
}

// A bank exactly the size of one board is one night's worth. The second game
// has nothing left and says so in the words a host can act on.
func TestASecondGameCannotReuseTheFirstNightsQuestions(t *testing.T) {
	f := newFixture(t)
	// Five topics x two questions is exactly a 5x2 board.
	f.seedBank(topicSet(), 2)

	first := f.newGame(noRepeats(), topicSet())
	f.playEveryCell(first)

	second := f.newGame(noRepeats(), nil)
	err := f.tryBuildBoard(second, topicSet())
	var se *ShortfallError
	if !errors.As(err, &se) {
		t.Fatalf("the second game built a board from a bank the first one used up (err = %v)", err)
	}
	if !se.Fresh {
		t.Fatalf("the shortfall does not say the count was of fresh questions: %v", se)
	}
	if !strings.Contains(se.Error(), "fresh questions") {
		t.Fatalf("shortfall reads %q — a host will go looking for questions that are already there", se)
	}
}

// Deleting the night gives its questions back. This is the ONLY reset, and it
// is an operation the host already understands.
func TestDeletingAGameFreesItsQuestions(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 2)

	first := f.newGame(noRepeats(), topicSet())
	f.playEveryCell(first)
	second := f.newGame(noRepeats(), nil)
	if err := f.tryBuildBoard(second, topicSet()); err == nil {
		t.Fatal("the bank was not used up to begin with — this test proves nothing")
	}

	if err := DeleteGame(f.ctx, f.pool, f.tenant.ID, first.ID); err != nil {
		t.Fatalf("DeleteGame: %v", err)
	}
	if err := f.tryBuildBoard(second, topicSet()); err != nil {
		t.Fatalf("deleting the first game did not free its questions: %v", err)
	}
}

// A cell nobody opened was never asked. This is the whole reason the rule is
// about rounds rather than last_used_at, which a board stamps on ten
// questions whether or not the night gets to them.
func TestAnUnopenedCellIsStillFresh(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 2)

	first := f.newGame(noRepeats(), topicSet())
	asked := f.playOneCell(first)

	// The second game can see everything except the one prompt that was
	// actually read out.
	bank, err := QuestionsForTopics(f.ctx, f.pool, f.tenant.ID, foldAll(topicSet()), nil,
		Freshness{GameID: uuid.New()})
	if err != nil {
		t.Fatalf("QuestionsForTopics: %v", err)
	}
	if len(bank) != 9 {
		t.Fatalf("got %d fresh questions after one cell was played, want 9 of 10", len(bank))
	}
	for _, q := range bank {
		if q.ID == asked {
			t.Fatalf("the one question the room was actually asked is still on offer: %q", q.Prompt)
		}
	}
}

// The escape hatch. A venue whose bank has run thin ticks the box and plays.
func TestRepeatsOnLetsAGameReuseEverything(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 2)

	first := f.newGame(noRepeats(), topicSet())
	f.playEveryCell(first)

	second := f.newGame(defaultSettings(), nil) // repeats on
	if err := f.tryBuildBoard(second, topicSet()); err != nil {
		t.Fatalf("a game with repeats allowed still refused the used bank: %v", err)
	}
}

// The final draws from the bank too, and it is the worst round in the game to
// serve a rerun on.
func TestTheFinalsDrawRespectsTheRepeatSetting(t *testing.T) {
	f := newFixture(t)
	f.seedBank([]string{"space"}, 1)

	first := f.newGame(oneCellNoRepeats(), []string{"space"})
	f.playEveryCell(first)

	second := f.newGame(noRepeats(), nil)
	_, err := LeastUsedQuestion(f.ctx, f.pool, f.tenant.ID, nil, freshnessOf(second))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("the final drew a question another night already asked (err = %v)", err)
	}

	// The same game with repeats on finds it.
	second.RepeatQuestions = true
	if _, err := LeastUsedQuestion(f.ctx, f.pool, f.tenant.ID, nil, freshnessOf(second)); err != nil {
		t.Fatalf("with repeats allowed the final found nothing: %v", err)
	}
}

// Two packs holding the same question are two rows saying one thing. Asking
// it from either spends it in both, which is why the match is on prompt_key
// rather than on the question id.
func TestAskingAQuestionSpendsItInEveryDataset(t *testing.T) {
	f := newFixture(t)
	shared := "Which planet is the hottest?"
	f.seedShared("General", shared, "space")
	f.seedShared("Space pack", shared, "space")

	// One game asks it, from whichever of the two rows the draw picked.
	game := f.newGame(oneCellNoRepeats(), []string{"space"})
	f.playEveryCell(game)

	// A later game drawing on BOTH packs must not find it again.
	next := f.newGame(noRepeats(), nil)
	bank, err := QuestionsForTopics(f.ctx, f.pool, f.tenant.ID, []string{"space"}, nil, freshnessOf(next))
	if err != nil {
		t.Fatalf("QuestionsForTopics: %v", err)
	}
	if len(bank) != 0 {
		t.Fatalf("the same question came back from the other pack: %+v", bank[0].Prompt)
	}

	// And the bank page says the set is spent rather than full.
	sets, err := ListDatasets(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatalf("ListDatasets: %v", err)
	}
	for _, d := range sets {
		if d.Questions != 1 || d.Fresh != 0 {
			t.Fatalf("set %q reports %d fresh of %d, want 0 of 1", d.Name, d.Fresh, d.Questions)
		}
	}
}

// The setup page's per-topic bars count fresh questions, and honour the
// game's own repeat setting -- with repeats on everything is available, so
// the bar says so rather than showing a number the host cannot act on.
func TestTopicCountsFollowTheAskedHistory(t *testing.T) {
	f := newFixture(t)
	f.seedBank([]string{"space"}, 3)

	game := f.newGame(oneCellNoRepeats(), []string{"space"})
	f.playEveryCell(game)

	next := f.newGame(noRepeats(), nil)
	hist, err := TopicHistogram(f.ctx, f.pool, f.tenant.ID, nil, freshnessOf(next))
	if err != nil {
		t.Fatalf("TopicHistogram: %v", err)
	}
	if len(hist) != 1 || hist[0].Total != 3 || hist[0].Unused != 2 {
		t.Fatalf("topic bar = %+v, want 2 unused of 3 after one question was asked", hist)
	}

	next.RepeatQuestions = true
	hist, err = TopicHistogram(f.ctx, f.pool, f.tenant.ID, nil, freshnessOf(next))
	if err != nil {
		t.Fatalf("TopicHistogram: %v", err)
	}
	if hist[0].Unused != 3 {
		t.Fatalf("with repeats on the bar reads %d unused of 3, want all 3 available", hist[0].Unused)
	}
}

func foldAll(topics []string) []string {
	out := make([]string, len(topics))
	for i, t := range topics {
		out[i] = FoldKey(t)
	}
	return out
}
