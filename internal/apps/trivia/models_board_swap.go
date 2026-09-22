package trivia

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reading the board back and changing ONE question on it.
//
// A rebuild was the only tool the host had, and it is the wrong one for "I
// don't like that one": it throws away the nine cells they were happy with to
// fix the tenth. Swapping a single cell moves nothing the room can see -- the
// column and the points stay exactly where they were -- so it is safe to
// press as many times as it takes.

// GetBoardCell loads one tile.
func GetBoardCell(ctx context.Context, q Querier, tenantID, gameID, cellID uuid.UUID) (*BoardCell, error) {
	var c BoardCell
	err := q.QueryRow(ctx, `
		SELECT id, game_id, round_index, col_index, row_index, topic, points, question_id, played_at
		  FROM app_trivia_board_cells
		 WHERE tenant_id = $1 AND game_id = $2 AND id = $3`, tenantID, gameID, cellID).
		Scan(&c.ID, &c.GameID, &c.RoundIndex, &c.ColIndex, &c.RowIndex,
			&c.Topic, &c.Points, &c.QuestionID, &c.PlayedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying trivia board cell: %w", err)
	}
	return &c, nil
}

// QuestionsByID loads the bank rows behind a board in one query, keyed by id.
//
// Topics are deliberately NOT attached: the caller already knows which column
// each question is sitting in, and a board of ten cells does not need a
// second query to be told what it can see.
func QuestionsByID(ctx context.Context, q Querier, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Question, error) {
	out := map[uuid.UUID]Question{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx,
		`SELECT `+questionColumns+` FROM app_trivia_questions WHERE tenant_id = $1 AND id = ANY($2)`,
		tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("querying trivia questions by id: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning trivia question: %w", err)
		}
		out[q.ID] = *q
	}
	return out, rows.Err()
}

// swapCandidateSQL is the predicate "this bank row could take a cell in this
// game", for a query with the questions table aliased `q`. The count and the
// pick below share it so a Swap button can never be offered for a cell the
// pick would then refuse.
//
// THE EXCLUSIONS ARE ON prompt_key, NOT ON question id. The same question
// uploaded in two sets is two rows saying one thing, and swapping in the
// other copy would put the same question on the board twice -- which the
// board's own unique index, being on question_id, would happily allow and
// every person in the room would notice.
//
// The own-game rounds clause is not covered by freshSQL, which deliberately
// ignores this game's rounds so a mid-night rebuild can re-place what this
// game has already used. A swap wants the opposite: even with repeats on, one
// night does not ask the same question twice.
func swapCandidateSQL(gameParam, datasetsParam, repeatParam string) string {
	return `(` + datasetsParam + `::uuid[] IS NULL OR cardinality(` + datasetsParam + `::uuid[]) = 0
		      OR q.dataset_id = ANY(` + datasetsParam + `::uuid[]))
		AND ` + freshSQL(repeatParam, gameParam) + `
		AND NOT EXISTS (SELECT 1 FROM app_trivia_board_cells c
		                  JOIN app_trivia_questions bq
		                    ON bq.id = c.question_id AND bq.tenant_id = c.tenant_id
		                 WHERE c.tenant_id = q.tenant_id AND c.game_id = ` + gameParam + `::uuid
		                   AND bq.prompt_key = q.prompt_key)
		AND NOT EXISTS (SELECT 1 FROM app_trivia_rounds r
		                 WHERE r.tenant_id = q.tenant_id AND r.game_id = ` + gameParam + `::uuid
		                   AND r.prompt_key = q.prompt_key)`
}

// SwapSpares counts, per topic key, what a swap in that column could reach
// for. The console needs it to grey out a button rather than offer one that
// answers 422, and the host needs the number itself: "3 spare" and "none
// left" are different situations with different fixes.
func SwapSpares(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID,
	topicKeys []string, datasetIDs []uuid.UUID, fresh Freshness) (map[string]int, error) {
	out := map[string]int{}
	if len(topicKeys) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT t.topic_key, count(DISTINCT q.prompt_key)::int
		  FROM app_trivia_questions q
		  JOIN app_trivia_question_topics t
		    ON t.question_id = q.id AND t.tenant_id = q.tenant_id
		 WHERE q.tenant_id = $1
		   AND t.topic_key = ANY($2)
		   AND `+swapCandidateSQL("$3", "$4", "$5")+`
		 GROUP BY t.topic_key`,
		tenantID, topicKeys, gameID, datasetIDs, fresh.AllowRepeats)
	if err != nil {
		return nil, fmt.Errorf("counting trivia swap candidates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, fmt.Errorf("scanning trivia swap candidate count: %w", err)
		}
		out[key] = n
	}
	return out, rows.Err()
}

// NextSwapQuestion picks the replacement for one cell: same category,
// least-recently-heard first. ErrNotFound means the category has nothing left
// this board is not already using, which is a sentence the host can act on
// rather than a failure.
//
// Least-recently-heard is also what makes the button ROTATE rather than
// toggle: the question it swaps in is stamped used on the way, so pressing it
// again reaches past it to the next one.
func NextSwapQuestion(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID,
	topicKey string, datasetIDs []uuid.UUID, fresh Freshness) (*Question, error) {
	q, err := scanQuestion(pool.QueryRow(ctx, `
		SELECT `+questionColumns+`
		  FROM app_trivia_questions q
		 WHERE q.tenant_id = $1
		   AND EXISTS (SELECT 1 FROM app_trivia_question_topics t
		                WHERE t.question_id = q.id AND t.tenant_id = q.tenant_id
		                  AND t.topic_key = $2)
		   AND `+swapCandidateSQL("$3", "$4", "$5")+`
		 ORDER BY q.last_used_at ASC NULLS FIRST, q.id
		 LIMIT 1`,
		tenantID, topicKey, gameID, datasetIDs, fresh.AllowRepeats))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying a trivia swap candidate: %w", err)
	}
	return q, nil
}

// SwapCellQuestion puts a different question behind one tile.
//
// played_at IS NULL is in the WHERE rather than in a read-then-write: the
// host's tab may have been open since before the room opened that cell, and
// rewriting the question under a round already in play would restate a
// question that was read out loud.
func SwapCellQuestion(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID, cellID, questionID uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning trivia cell swap: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE app_trivia_board_cells SET question_id = $4
		 WHERE tenant_id = $1 AND game_id = $2 AND id = $3 AND played_at IS NULL`,
		tenantID, gameID, cellID, questionID)
	if err != nil {
		return fmt.Errorf("swapping trivia cell question: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := MarkQuestionsUsed(ctx, tx, tenantID, []uuid.UUID{questionID}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE app_trivia_games SET state_version = state_version + 1, updated_at = now()
		  WHERE tenant_id = $1 AND id = $2`, tenantID, gameID); err != nil {
		return fmt.Errorf("bumping state version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing trivia cell swap: %w", err)
	}
	return nil
}
