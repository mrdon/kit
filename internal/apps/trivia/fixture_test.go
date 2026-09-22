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
	if err := f.tryBuildBoard(game, topics); err != nil {
		f.t.Fatalf("building a board: %v", err)
	}
}

// tryBuildBoard is the same draw the console does, errors and all, for the
// tests that are about a build FAILING. Going through drawBoard rather than
// assembling the bank by hand is the point: a test that built its own
// candidate list would not notice the repeat rule at all.
func (f *fixture) tryBuildBoard(game *Game, topics []string) error {
	f.t.Helper()
	keys := make([]string, len(topics))
	for i, t := range topics {
		keys[i] = FoldKey(t)
	}
	// A fixed seed, so a test can pin a bank to a board.
	rows, err := drawBoard(f.ctx, f.pool, f.tenant.ID, game, keys, nil, 1)
	if err != nil {
		return err
	}
	return ReplaceBoard(f.ctx, f.pool, f.tenant.ID, game.ID, rows)
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

// defaultSettings is the test board. It DELIBERATELY differs from the shipped
// DefaultSettings, which has cells and chips at the same $100/$200 so betting
// outweighs knowing.
//
// Tests want the two channels to be distinguishable: with cells at $500/$1000
// against $100/$200 chips, an assertion that a team took $500 of board points
// and $200 of winnings would fail if the engine ever swapped the two, whereas
// identical values would let that bug pass silently. The shipped weighting is
// covered separately by the tests that construct settings explicitly.
//
// RepeatQuestions is ON here for the same reason. Most of these tests run
// several games off one small seeded bank in a single tenant, and they are
// about scoring, phases and projections -- not about the bank. Leaving the
// shipped no-repeats rule on would make half of them fail with a shortfall
// that has nothing to do with what they assert. The rule itself is covered by
// the tests in models_fresh_test.go, which set this false deliberately.
func defaultSettings() Settings {
	return Settings{
		BoardRows: 2, BoardColumns: 5,
		CellValues: []int{500, 1000}, TokenValues: []int{100, 200},
		FinalWager: true, AnswerSeconds: 60, RevealSeconds: 15, BetSeconds: 45,
		RepeatQuestions: true,
		// ONE round, explicitly, unlike the shipped default of two. Almost
		// every test here is about scoring, phases or projections and wants
		// the smallest night that exercises them; leaving this at zero would
		// normalise to the shipped two and silently double every board these
		// tests build. The round machinery has its own tests, which set it.
		BoardRounds: 1,
	}
}

// TestShippedDefaultsWeightBettingOverKnowing pins the product decision that
// the fixture above deliberately does not use.
func TestShippedDefaultsWeightBettingOverKnowing(t *testing.T) {
	d := DefaultSettings()
	if err := validateSettings(d); err != nil {
		t.Fatalf("the shipped defaults do not validate: %v", err)
	}
	cheapestCell, dearestChip := d.CellValues[0], d.TokenValues[len(d.TokenValues)-1]
	if cheapestCell > dearestChip {
		t.Fatalf("cheapest cell %d is worth more than the biggest chip %d — "+
			"the default is supposed to make betting at least as big as knowing",
			cheapestCell, dearestChip)
	}
	if d.BoardRows*d.BoardColumns != 10 {
		t.Fatalf("the default board is %dx%d; ten questions is the half-hour shape",
			d.BoardColumns, d.BoardRows)
	}
}

func topicSet() []string { return []string{"space", "sports", "film", "food", "history"} }
