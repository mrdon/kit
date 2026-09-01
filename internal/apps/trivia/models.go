package trivia

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is the package's "no such row" sentinel, so handlers can 404
// without matching on pgx.ErrNoRows all over the place.
var ErrNotFound = errors.New("trivia: not found")

// Phase names where a game is in its lifecycle. There is deliberately no
// separate phase for the final: it re-enters PhaseQuestion with the round
// marked final, so every screen, timer and transition already handles it.
type Phase string

// The eight phases. See the state machine in service.go.
const (
	PhaseSetup    Phase = "setup"
	PhaseLobby    Phase = "lobby"
	PhaseBoard    Phase = "board"
	PhaseQuestion Phase = "question"
	PhaseReveal   Phase = "reveal"
	PhaseBetting  Phase = "betting"
	PhaseScoring  Phase = "scoring"
	PhasePodium   Phase = "podium"
)

// MaxTeams caps a game. Twenty tables is already a big room, and the TV
// layout (a per-team bar on the question screen, team pills in the lobby) is
// designed against that number rather than an unbounded one.
const MaxTeams = 20

// Question is one row of the workspace bank.
type Question struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Prompt      string
	PromptKey   string
	AnswerValue float64
	AnswerText  string
	LastUsedAt  *time.Time
	Topics      []Topic
}

// Topic is one category spelling attached to a question.
type Topic struct {
	Key   string
	Label string
}

// FoldKey folds free text to a comparison key: lowercased, with runs of
// whitespace and punctuation collapsed to single spaces and the ends
// trimmed. Used for question dedupe, topic grouping and team names alike, so
// "Sports " and "sports" are one thing everywhere in the app.
func FoldKey(s string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSpace = false
		case r > 127:
			// Keep non-ASCII letters as themselves; folding them properly is
			// a job for x/text and the payoff here is nil.
			b.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

const questionColumns = `id, tenant_id, prompt, prompt_key, answer_value, answer_text, last_used_at`

func scanQuestion(row pgx.Row) (*Question, error) {
	var q Question
	err := row.Scan(&q.ID, &q.TenantID, &q.Prompt, &q.PromptKey,
		&q.AnswerValue, &q.AnswerText, &q.LastUsedAt)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// CountQuestions is the bank size, for the Apps settings usage line.
func CountQuestions(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM app_trivia_questions WHERE tenant_id = $1`, tenantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting trivia questions: %w", err)
	}
	return n, nil
}

// UpsertQuestion writes one question into a DATASET and replaces its topic
// set, keyed on prompt_key within that dataset so re-uploading a corrected
// CSV updates in place. Returns the question id and whether the row already
// existed, which is what the import report counts as "updated".
//
// Uniqueness is per dataset, not per workspace: a general pack and a sports
// pack may both ask how many holes are on a golf course, and forbidding that
// would make the second upload silently lossy. The board builder dedupes on
// the question text so a game drawing on both still only asks it once.
func UpsertQuestion(ctx context.Context, tx pgx.Tx, tenantID, datasetID uuid.UUID, q Question) (uuid.UUID, bool, error) {
	var id uuid.UUID
	var inserted bool
	err := tx.QueryRow(ctx, `
		INSERT INTO app_trivia_questions (tenant_id, dataset_id, prompt, prompt_key, answer_value, answer_text)
		VALUES ($1, $6, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, dataset_id, prompt_key) DO UPDATE
		   SET prompt = EXCLUDED.prompt,
		       answer_value = EXCLUDED.answer_value,
		       answer_text = EXCLUDED.answer_text,
		       updated_at = now()
		RETURNING id, (xmax = 0)`,
		tenantID, q.Prompt, q.PromptKey, q.AnswerValue, q.AnswerText, datasetID).Scan(&id, &inserted)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("upserting trivia question: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM app_trivia_question_topics WHERE tenant_id = $1 AND question_id = $2`,
		tenantID, id); err != nil {
		return uuid.Nil, false, fmt.Errorf("clearing question topics: %w", err)
	}
	for _, t := range q.Topics {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_trivia_question_topics (tenant_id, question_id, topic_key, topic)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (question_id, topic_key) DO NOTHING`,
			tenantID, id, t.Key, t.Label); err != nil {
			return uuid.Nil, false, fmt.Errorf("inserting question topic: %w", err)
		}
	}
	return id, inserted, nil
}

// ExistingPromptKeys returns the subset of keys already in the bank, so an
// import can report duplicates without a round trip per row.
func ExistingPromptKeys(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx,
		`SELECT prompt_key FROM app_trivia_questions WHERE tenant_id = $1 AND prompt_key = ANY($2)`,
		tenantID, keys)
	if err != nil {
		return nil, fmt.Errorf("querying existing prompt keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scanning prompt key: %w", err)
		}
		out[k] = true
	}
	return out, rows.Err()
}

// TopicCount is one bar of the setup page's histogram: how many questions a
// topic has, and how many of those the room has never been asked.
type TopicCount struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Total  int    `json:"total"`
	Unused int    `json:"unused"`
}

// TopicHistogram is what the setup page needs to offer column choices, and
// the reason the import response carries it: a host who uploads a sheet and
// is told only "38 imported" has no idea what they got.
//
// datasetIDs narrows it to a game's selection. An EMPTY slice means every
// dataset — the same rule GameDatasetIDs documents — so a game that has never
// opened the picker still sees the full set of topics it can actually draw
// from. Counting the whole workspace regardless would offer the host a column
// their game cannot fill.
//
// The counts are DISTINCT on the question text rather than on the row,
// because two selected datasets may hold the same question and the board will
// only ever ask it once.
func TopicHistogram(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, datasetIDs []uuid.UUID) ([]TopicCount, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.topic_key, min(t.topic),
		       count(DISTINCT q.prompt_key)::int,
		       count(DISTINCT q.prompt_key) FILTER (WHERE q.last_used_at IS NULL)::int
		  FROM app_trivia_question_topics t
		  JOIN app_trivia_questions q ON q.id = t.question_id AND q.tenant_id = t.tenant_id
		 WHERE t.tenant_id = $1
		   AND ($2::uuid[] IS NULL OR cardinality($2::uuid[]) = 0 OR q.dataset_id = ANY($2::uuid[]))
		 GROUP BY t.topic_key
		 ORDER BY count(DISTINCT q.prompt_key) DESC, t.topic_key`, tenantID, datasetIDs)
	if err != nil {
		return nil, fmt.Errorf("querying topic histogram: %w", err)
	}
	defer rows.Close()
	var out []TopicCount
	for rows.Next() {
		var tc TopicCount
		if err := rows.Scan(&tc.Key, &tc.Label, &tc.Total, &tc.Unused); err != nil {
			return nil, fmt.Errorf("scanning topic count: %w", err)
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// QuestionsForTopics loads every question carrying any of the given topics,
// least-recently-used first so a weekly quiz doesn't repeat itself. Each
// question comes back with its full topic set, because the board builder has
// to know that one question can fill either of two columns.
// datasetIDs narrows to a game's selection; empty means every dataset.
//
// DISTINCT ON (prompt_key) is what stops two selected datasets that share a
// question from putting it on the board twice. The board's own unique index
// is on question_id, which would not catch it — those are two different rows
// saying the same thing, and the room would notice.
func QuestionsForTopics(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, topicKeys []string, datasetIDs []uuid.UUID) ([]Question, error) {
	rows, err := pool.Query(ctx, `
		SELECT * FROM (
		  SELECT DISTINCT ON (q.prompt_key) `+questionColumns+`
		    FROM app_trivia_questions q
		   WHERE q.tenant_id = $1
		     AND ($3::uuid[] IS NULL OR cardinality($3::uuid[]) = 0 OR q.dataset_id = ANY($3::uuid[]))
		     AND EXISTS (SELECT 1 FROM app_trivia_question_topics t
		                  WHERE t.question_id = q.id AND t.topic_key = ANY($2))
		   ORDER BY q.prompt_key, q.last_used_at ASC NULLS FIRST, q.id
		) d
		 ORDER BY d.last_used_at ASC NULLS FIRST, d.id`, tenantID, topicKeys, datasetIDs)
	if err != nil {
		return nil, fmt.Errorf("querying questions for topics: %w", err)
	}
	defer rows.Close()
	var out []Question
	byID := map[uuid.UUID]int{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning question: %w", err)
		}
		byID[q.ID] = len(out)
		out = append(out, *q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := attachTopics(ctx, pool, tenantID, out, byID); err != nil {
		return nil, err
	}
	return out, nil
}

// attachTopics fills in each question's topic set in one extra query rather
// than N, and rather than a join that would multiply the question rows.
func attachTopics(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, qs []Question, byID map[uuid.UUID]int) error {
	if len(qs) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(qs))
	for _, q := range qs {
		ids = append(ids, q.ID)
	}
	rows, err := pool.Query(ctx, `
		SELECT question_id, topic_key, topic
		  FROM app_trivia_question_topics
		 WHERE tenant_id = $1 AND question_id = ANY($2)
		 ORDER BY topic_key`, tenantID, ids)
	if err != nil {
		return fmt.Errorf("querying question topics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var qid uuid.UUID
		var t Topic
		if err := rows.Scan(&qid, &t.Key, &t.Label); err != nil {
			return fmt.Errorf("scanning question topic: %w", err)
		}
		if i, ok := byID[qid]; ok {
			qs[i].Topics = append(qs[i].Topics, t)
		}
	}
	return rows.Err()
}

// GetQuestion loads one bank row with its topics.
func GetQuestion(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Question, error) {
	q, err := scanQuestion(pool.QueryRow(ctx,
		`SELECT `+questionColumns+` FROM app_trivia_questions WHERE tenant_id = $1 AND id = $2`,
		tenantID, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying trivia question: %w", err)
	}
	list := []Question{*q}
	if err := attachTopics(ctx, pool, tenantID, list, map[uuid.UUID]int{q.ID: 0}); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// LeastUsedQuestion picks the bank question the room has heard least
// recently that isn't already on this game's board. It's how the host gets a
// final question without going shopping, and how the Auto button fills gaps.
func LeastUsedQuestion(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, datasetIDs []uuid.UUID) (*Question, error) {
	q, err := scanQuestion(pool.QueryRow(ctx, `
		SELECT `+questionColumns+`
		  FROM app_trivia_questions q
		 WHERE q.tenant_id = $1
		   AND ($3::uuid[] IS NULL OR cardinality($3::uuid[]) = 0 OR q.dataset_id = ANY($3::uuid[]))
		   AND NOT EXISTS (SELECT 1 FROM app_trivia_board_cells c
		                    WHERE c.game_id = $2 AND c.question_id = q.id)
		   AND NOT EXISTS (SELECT 1 FROM app_trivia_rounds r
		                    WHERE r.game_id = $2 AND r.question_id = q.id)
		 ORDER BY q.last_used_at ASC NULLS FIRST, q.id
		 LIMIT 1`, tenantID, gameID, datasetIDs))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying least-used question: %w", err)
	}
	return q, nil
}

// MarkQuestionsUsed stamps last_used_at so the next board prefers what the
// room has not heard. Called when a board is built, not when a cell is
// played: a question that made it onto tonight's board has been spent even if
// the night ends early.
func MarkQuestionsUsed(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx,
		`UPDATE app_trivia_questions SET last_used_at = now() WHERE tenant_id = $1 AND id = ANY($2)`,
		tenantID, ids)
	if err != nil {
		return fmt.Errorf("marking questions used: %w", err)
	}
	return nil
}

// DeleteQuestion removes a bank row. Board cells reference questions with ON
// DELETE RESTRICT, so a question that is on some game's board cannot be
// deleted out from under it -- the caller surfaces that as a conflict rather
// than orphaning a live board.
func DeleteQuestion(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	tag, err := pool.Exec(ctx,
		`DELETE FROM app_trivia_questions WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("deleting trivia question: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
