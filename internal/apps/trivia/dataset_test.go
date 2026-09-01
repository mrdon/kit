package trivia

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A game draws only from the datasets it selects. This is the whole point of
// the feature: a Christmas quiz must not pull a question from the sports pack.
func TestGameDrawsOnlyFromSelectedDatasets(t *testing.T) {
	f := newFixture(t)
	xmas := f.seedDataset("Christmas", []string{"xmas"}, 6)
	f.seedDataset("Sports", []string{"sportsball"}, 6)

	game := f.newGame(defaultSettings(), nil)
	if err := SetGameDatasets(f.ctx, f.pool, f.tenant.ID, game.ID, []uuid.UUID{xmas}); err != nil {
		t.Fatal(err)
	}

	hist, err := TopicHistogram(f.ctx, f.pool, f.tenant.ID, []uuid.UUID{xmas})
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Key != "xmas" {
		t.Fatalf("topics = %+v, want only xmas", hist)
	}
	got, err := QuestionsForTopics(f.ctx, f.pool, f.tenant.ID, []string{"xmas", "sportsball"}, []uuid.UUID{xmas})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d questions, want the 6 in the selected set", len(got))
	}
	for _, q := range got {
		for _, tp := range q.Topics {
			if tp.Key == "sportsball" {
				t.Fatalf("a question from an unselected dataset leaked in: %q", q.Prompt)
			}
		}
	}
}

// No selection means EVERY dataset. A game created before its datasets
// existed, or one whose only selected set was deleted, has to stay playable.
func TestNoSelectionMeansEveryDataset(t *testing.T) {
	f := newFixture(t)
	f.seedDataset("Christmas", []string{"xmas"}, 3)
	f.seedDataset("Sports", []string{"sportsball"}, 3)
	game := f.newGame(defaultSettings(), nil)

	ids, err := GameDatasetIDs(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("a new game already has a selection: %v", ids)
	}
	hist, err := TopicHistogram(f.ctx, f.pool, f.tenant.ID, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("topics = %+v, want both datasets", hist)
	}
}

// Deleting the only selected dataset must leave the game able to build a
// board, not strand it with a selection pointing at nothing.
func TestDeletingTheSelectedDatasetLeavesTheGamePlayable(t *testing.T) {
	f := newFixture(t)
	doomed := f.seedDataset("Doomed", []string{"xmas"}, 4)
	f.seedDataset("Survivor", []string{"sportsball"}, 4)
	game := f.newGame(defaultSettings(), nil)
	if err := SetGameDatasets(f.ctx, f.pool, f.tenant.ID, game.ID, []uuid.UUID{doomed}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDataset(f.ctx, f.pool, f.tenant.ID, doomed); err != nil {
		t.Fatal(err)
	}

	ids, err := GameDatasetIDs(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("the selection still points at a deleted set: %v", ids)
	}
	hist, err := TopicHistogram(f.ctx, f.pool, f.tenant.ID, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) == 0 {
		t.Fatal("the game can no longer see any topics — it has been stranded")
	}
}

// Two datasets may hold the same question; a game drawing on both must still
// only ask it once. The board's unique index is on question_id, which would
// NOT catch this — they are two different rows saying the same thing.
func TestTheSameQuestionInTwoDatasetsIsAskedOnce(t *testing.T) {
	f := newFixture(t)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	shared := Question{
		Prompt: "How many holes on a golf course?", PromptKey: FoldKey("How many holes on a golf course?"),
		AnswerValue: 18, AnswerText: "18",
		Topics: []Topic{{Key: "sport", Label: "Sport"}},
	}
	var ids []uuid.UUID
	for _, name := range []string{"General", "Sports"} {
		dsID, err := UpsertDataset(f.ctx, tx, f.tenant.ID, name, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := UpsertQuestion(f.ctx, tx, f.tenant.ID, dsID, shared); err != nil {
			t.Fatalf("the same question was refused in a second dataset: %v", err)
		}
		ids = append(ids, dsID)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}

	got, err := QuestionsForTopics(f.ctx, f.pool, f.tenant.ID, []string{"sport"}, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates for one question held in two datasets, want 1", len(got))
	}
	hist, err := TopicHistogram(f.ctx, f.pool, f.tenant.ID, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Total != 1 {
		t.Fatalf("histogram counted it twice: %+v", hist)
	}
}

// Re-uploading a set REPLACES its contents. Merging would make a removed
// question impossible to remove.
func TestReuploadReplacesADatasetsContents(t *testing.T) {
	f := newFixture(t)
	ds := f.seedDataset("Weekly", []string{"first"}, 5)

	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ClearDataset(f.ctx, tx, f.tenant.ID, ds); err != nil {
		t.Fatal(err)
	}
	replacement := Question{
		Prompt: "A brand new question?", PromptKey: FoldKey("A brand new question?"),
		AnswerValue: 1, AnswerText: "1", Topics: []Topic{{Key: "second", Label: "Second"}},
	}
	if _, _, err := UpsertQuestion(f.ctx, tx, f.tenant.ID, ds, replacement); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}

	sets, err := ListDatasets(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 1 {
		t.Fatalf("got %d datasets, want 1 — the re-upload made a second one", len(sets))
	}
	if sets[0].Questions != 1 {
		t.Fatalf("dataset holds %d questions after a replace, want 1", sets[0].Questions)
	}
}

// A dataset whose questions are on a game's board cannot be deleted: the cell
// would no longer be able to say what it asked.
func TestDatasetOnALiveBoardCannotBeDeleted(t *testing.T) {
	f := newFixture(t)
	ds := f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())

	err := DeleteDataset(f.ctx, f.pool, f.tenant.ID, ds)
	if !errors.Is(err, ErrDatasetInUse) {
		t.Fatalf("deleting a dataset on a live board returned %v, want ErrDatasetInUse", err)
	}
	// And the board is untouched.
	cells, err := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 10 {
		t.Fatalf("the board lost cells to a refused delete: %d", len(cells))
	}
}

// Two datasets cannot share a name in one workspace, so a re-upload replaces
// rather than duplicating.
func TestDatasetNamesAreUniquePerWorkspace(t *testing.T) {
	f := newFixture(t)
	a := f.seedDataset("Christmas", []string{"xmas"}, 2)
	b := f.seedDataset("  christmas  ", []string{"xmas"}, 2)
	if a != b {
		t.Fatal("names differing only in case and space made two datasets")
	}
}

// Datasets are tenant-scoped like everything else.
func TestDatasetsAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t)
	f.seedDataset("Mine", []string{"xmas"}, 2)

	sets, err := ListDatasets(other.ctx, other.pool, other.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 0 {
		t.Fatalf("another workspace can see %d datasets", len(sets))
	}
}

// The case that motivated the round snapshot. A dataset used by a FINISHED
// game must be deletable — otherwise a venue running a weekly quiz
// accumulates sets it can never remove — while a game still in play must
// still block it.
func TestFinishedGameDoesNotBlockDatasetDeletion(t *testing.T) {
	f := newFixture(t)
	ds := f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, map[uuid.UUID]string{a.ID: "10"})

	// Still in play: blocked, and the message names the game.
	err := DeleteDataset(f.ctx, f.pool, f.tenant.ID, ds)
	if !errors.Is(err, ErrDatasetInUse) {
		t.Fatalf("a live game did not block the delete: %v", err)
	}
	if !strings.Contains(err.Error(), game.Name) {
		t.Fatalf("the error does not say which game: %v", err)
	}

	// Finished: allowed.
	f.do(game.ID, ActionRequest{Action: ActionFinish, FromPhase: PhaseScoring})
	if err := DeleteDataset(f.ctx, f.pool, f.tenant.ID, ds); err != nil {
		t.Fatalf("a finished game still blocks the delete: %v", err)
	}

	// And the recap of that finished game still reads correctly, because the
	// round carries its own copy of the question.
	out, err := dispatchCore(f.ctx, f.caller(), f.pool, f.svc,
		"trivia_results", json.RawMessage(`{"game":"`+game.Name+`"}`))
	if err != nil {
		t.Fatalf("the recap broke after its dataset was deleted: %v", err)
	}
	if !strings.Contains(out, "Round by round") {
		t.Fatalf("the recap lost its rounds:\n%s", out)
	}
	if strings.Contains(out, "—  —") || strings.Contains(out, ". — ") {
		t.Fatalf("the recap has an empty question:\n%s", out)
	}
}

// A live round must be marked against the answer the room was actually asked,
// even if somebody re-uploads a corrected sheet mid-game.
func TestEditingTheBankMidRoundDoesNotChangeTheAnswer(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	g := f.reload(game.ID)
	round, err := GetRound(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	asked := round.AnswerValue

	// Somebody corrects the bank while the question is on the wall.
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_trivia_questions SET answer_value = answer_value + 999 WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, *round.QuestionID); err != nil {
		t.Fatal(err)
	}

	after, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if after.Round.CorrectValue != asked {
		t.Fatalf("the answer changed under a live round: %v -> %v", asked, after.Round.CorrectValue)
	}
}
