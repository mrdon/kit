package trivia

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// fixture is a tenant with a question bank and a service, torn down after the
// test. Every test gets its own tenant so they can run in parallel against
// the shared pool.
type fixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	svc    *Service
	tenant *models.Tenant
	ctx    context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_trivia_test_" + uuid.NewString()
	slug := models.SanitizeSlug("trivia-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "trivia-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	return &fixture{t: t, pool: pool, svc: NewService(pool), tenant: tenant, ctx: ctx}
}

// seedBank writes n questions per topic into one dataset, so a board can be
// built. Returns the dataset id for tests that care about the selection.
func (f *fixture) seedBank(topics []string, perTopic int) uuid.UUID {
	return f.seedDataset("Test questions", topics, perTopic)
}

func (f *fixture) seedDataset(name string, topics []string, perTopic int) uuid.UUID {
	f.t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		f.t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	datasetID, err := UpsertDataset(f.ctx, tx, f.tenant.ID, name, "", "")
	if err != nil {
		f.t.Fatalf("creating dataset: %v", err)
	}
	n := 0
	for _, topic := range topics {
		for range perTopic {
			n++
			prompt := topic + " question " + uuid.NewString()
			if _, _, err := UpsertQuestion(f.ctx, tx, f.tenant.ID, datasetID, Question{
				Prompt: prompt, PromptKey: FoldKey(prompt),
				AnswerValue: float64(100 + n), AnswerText: FormatValue(float64(100 + n)),
				Topics: []Topic{{Key: FoldKey(topic), Label: topic}},
			}); err != nil {
				f.t.Fatalf("seeding question: %v", err)
			}
		}
	}
	if err := tx.Commit(f.ctx); err != nil {
		f.t.Fatalf("commit: %v", err)
	}
	return datasetID
}

// newGame creates a game with the given settings and a built board.
func (f *fixture) newGame(s Settings, topics []string) *Game {
	f.t.Helper()
	name, err := UniqueName(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		f.t.Fatalf("UniqueName: %v", err)
	}
	game, err := CreateGame(f.ctx, f.pool, f.tenant.ID, name, s, nil)
	if err != nil {
		f.t.Fatalf("CreateGame: %v", err)
	}
	if len(topics) > 0 {
		f.buildBoard(game, topics)
	}
	return f.reload(game.ID)
}

func (f *fixture) buildBoard(game *Game, topics []string) {
	f.t.Helper()
	keys := make([]string, len(topics))
	for i, t := range topics {
		keys[i] = FoldKey(t)
	}
	bank, err := QuestionsForTopics(f.ctx, f.pool, f.tenant.ID, keys, nil)
	if err != nil {
		f.t.Fatalf("QuestionsForTopics: %v", err)
	}
	cands := make([]BoardCandidate, 0, len(bank))
	for _, q := range bank {
		tk := make([]string, 0, len(q.Topics))
		for _, t := range q.Topics {
			tk = append(tk, t.Key)
		}
		cands = append(cands, BoardCandidate{QuestionID: q.ID.String(), TopicKeys: tk})
	}
	cells, err := BuildBoard(keys, game.BoardRows, game.CellValues, cands, 1)
	if err != nil {
		f.t.Fatalf("BuildBoard: %v", err)
	}
	rows := make([]BoardCell, 0, len(cells))
	for _, c := range cells {
		qid, err := uuid.Parse(c.QuestionID)
		if err != nil {
			f.t.Fatalf("bad question id: %v", err)
		}
		rows = append(rows, BoardCell{
			ColIndex: c.ColIndex, RowIndex: c.RowIndex,
			Topic: c.Topic, Points: c.Points, QuestionID: qid,
		})
	}
	if err := ReplaceBoard(f.ctx, f.pool, f.tenant.ID, game.ID, rows); err != nil {
		f.t.Fatalf("ReplaceBoard: %v", err)
	}
}

func (f *fixture) reload(gameID uuid.UUID) *Game {
	f.t.Helper()
	g, err := GetGame(f.ctx, f.pool, f.tenant.ID, gameID)
	if err != nil {
		f.t.Fatalf("GetGame: %v", err)
	}
	return g
}

func (f *fixture) join(gameID uuid.UUID, name string) *Team {
	f.t.Helper()
	team, _, err := f.svc.Join(f.ctx, f.tenant.ID, gameID, name)
	if err != nil {
		f.t.Fatalf("Join(%q): %v", name, err)
	}
	return team
}

// do runs a host action, failing the test on error.
func (f *fixture) do(gameID uuid.UUID, req ActionRequest) *Snapshot {
	f.t.Helper()
	snap, err := f.svc.Do(f.ctx, f.tenant.ID, gameID, req)
	if err != nil {
		f.t.Fatalf("action %s from %s: %v", req.Action, req.FromPhase, err)
	}
	return snap
}

// defaultSettings is the shipped board: 5 categories x 2 rows at $500/$1000,
// with fast timers so tests are not waiting on real clocks.
func defaultSettings() Settings {
	return Settings{
		BoardRows: 2, BoardColumns: 5,
		CellValues: []int{500, 1000}, TokenValues: []int{100, 200},
		FinalWager: true, AnswerSeconds: 60, RevealSeconds: 15, BetSeconds: 45,
	}
}

func topicSet() []string { return []string{"space", "sports", "film", "food", "history"} }
