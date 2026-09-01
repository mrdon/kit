package trivia

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the read surface shared by *pgxpool.Pool and pgx.Tx, so the same
// loader can run inside the transaction that just wrote or outside it on the
// committed row. Snapshot assembly needs both.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Round is one question in play. CellID is nil for a final, whose question is
// drawn from the bank rather than the board.
type Round struct {
	ID            uuid.UUID
	GameID        uuid.UUID
	CellID        *uuid.UUID
	QuestionID    uuid.UUID
	IsFinal       bool
	Ordinal       int
	Points        int
	WinningSlotID *uuid.UUID
	StartedAt     time.Time
	ScoredAt      *time.Time
}

const roundColumns = `id, game_id, cell_id, question_id, is_final, ordinal, points,
	winning_slot_id, started_at, scored_at`

func scanRound(row pgx.Row) (*Round, error) {
	var r Round
	err := row.Scan(&r.ID, &r.GameID, &r.CellID, &r.QuestionID, &r.IsFinal, &r.Ordinal,
		&r.Points, &r.WinningSlotID, &r.StartedAt, &r.ScoredAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetRound loads one round.
func GetRound(ctx context.Context, q Querier, tenantID, id uuid.UUID) (*Round, error) {
	r, err := scanRound(q.QueryRow(ctx,
		`SELECT `+roundColumns+` FROM app_trivia_rounds WHERE tenant_id = $1 AND id = $2`,
		tenantID, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying round: %w", err)
	}
	return r, nil
}

// ListRounds returns a game's rounds in play order, for the results recap.
func ListRounds(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) ([]Round, error) {
	rows, err := q.Query(ctx,
		`SELECT `+roundColumns+` FROM app_trivia_rounds
		  WHERE tenant_id = $1 AND game_id = $2 ORDER BY ordinal`, tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing rounds: %w", err)
	}
	defer rows.Close()
	var out []Round
	for rows.Next() {
		r, err := scanRound(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning round: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// Answer is what one team typed. Stake is set only in a final, where it is
// committed with the answer -- before the team has seen anyone else's number.
type Answer struct {
	TeamID      uuid.UUID
	Value       float64
	Raw         string
	Stake       *int
	SubmittedAt time.Time
}

// UpsertAnswer records or replaces a team's answer. Resubmitting until the
// deadline is allowed on purpose -- and said so on the phone -- because
// fat-finger anxiety on a 60-second clock is worse than a late edit.
func UpsertAnswer(ctx context.Context, pool *pgxpool.Pool, tenantID, roundID, teamID uuid.UUID, value float64, raw string, stake *int) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_trivia_answers (tenant_id, round_id, team_id, value, raw, stake)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id, round_id, team_id) DO UPDATE
		   SET value = EXCLUDED.value, raw = EXCLUDED.raw,
		       stake = EXCLUDED.stake, submitted_at = now()`,
		tenantID, roundID, teamID, value, raw, stake)
	if err != nil {
		return fmt.Errorf("upserting answer: %w", err)
	}
	return nil
}

// ListAnswers returns a round's answers in submission order.
func ListAnswers(ctx context.Context, q Querier, tenantID, roundID uuid.UUID) ([]Answer, error) {
	rows, err := q.Query(ctx,
		`SELECT team_id, value, raw, stake, submitted_at FROM app_trivia_answers
		  WHERE tenant_id = $1 AND round_id = $2 ORDER BY submitted_at, team_id`,
		tenantID, roundID)
	if err != nil {
		return nil, fmt.Errorf("listing answers: %w", err)
	}
	defer rows.Close()
	var out []Answer
	for rows.Next() {
		var a Answer
		if err := rows.Scan(&a.TeamID, &a.Value, &a.Raw, &a.Stake, &a.SubmittedAt); err != nil {
			return nil, fmt.Errorf("scanning answer: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SlotRow is a revealed card as stored. Position 0 is always the "Smaller
// than all of these" pseudo-slot and has a nil Value.
type SlotRow struct {
	ID       uuid.UUID
	Position int
	Value    *float64
	Label    string
	Odds     int
	TeamIDs  []uuid.UUID
}

// WriteSlots persists a built reveal inside the caller's transaction. Slots
// are written once, when the answer phase closes, and never edited: the cards
// on the TV and the cards the phones bet on have to be the same objects.
func WriteSlots(ctx context.Context, tx pgx.Tx, tenantID, roundID uuid.UUID, slots []SlotRow) error {
	for _, s := range slots {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app_trivia_slots (tenant_id, round_id, position, value, label, odds)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			tenantID, roundID, s.Position, s.Value, s.Label, s.Odds).Scan(&id); err != nil {
			return fmt.Errorf("inserting slot: %w", err)
		}
		for _, teamID := range s.TeamIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO app_trivia_slot_teams (tenant_id, slot_id, team_id)
				VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, tenantID, id, teamID); err != nil {
				return fmt.Errorf("inserting slot team: %w", err)
			}
		}
	}
	return nil
}

// ListSlots returns a round's cards ascending, each with the teams who wrote
// it. Team names ride on the cards from reveal onward: knowing who is usually
// right is half the fun of deciding where to put your chips.
func ListSlots(ctx context.Context, q Querier, tenantID, roundID uuid.UUID) ([]SlotRow, error) {
	rows, err := q.Query(ctx, `
		SELECT s.id, s.position, s.value, s.label, s.odds,
		       COALESCE(array_agg(st.team_id) FILTER (WHERE st.team_id IS NOT NULL), '{}')
		  FROM app_trivia_slots s
		  LEFT JOIN app_trivia_slot_teams st ON st.slot_id = s.id AND st.tenant_id = s.tenant_id
		 WHERE s.tenant_id = $1 AND s.round_id = $2
		 GROUP BY s.id, s.position, s.value, s.label, s.odds
		 ORDER BY s.position`, tenantID, roundID)
	if err != nil {
		return nil, fmt.Errorf("listing slots: %w", err)
	}
	defer rows.Close()
	var out []SlotRow
	for rows.Next() {
		var s SlotRow
		if err := rows.Scan(&s.ID, &s.Position, &s.Value, &s.Label, &s.Odds, &s.TeamIDs); err != nil {
			return nil, fmt.Errorf("scanning slot: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Bet is one chip on one card.
type Bet struct {
	TeamID     uuid.UUID
	TokenIndex int
	Amount     int
	SlotID     uuid.UUID
}

// PlaceBet moves a chip. A PUT of the desired placement rather than an
// append, so every retry over flaky bar wifi is idempotent: the
// (round, team, token_index) unique index turns a second tap into an UPDATE
// and a double-tap cannot double a team's money.
//
// The (round, team, slot_id) index is the two-different-answers rule. It is
// enforced here, in the database, rather than by a handler check that two
// racing requests could both pass -- and the phone mirrors it in the UI so
// the rule is obvious rather than discovered by rejection.
func PlaceBet(ctx context.Context, pool *pgxpool.Pool, tenantID, roundID, teamID uuid.UUID, tokenIndex, amount int, slotID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_trivia_bets (tenant_id, round_id, team_id, token_index, amount, slot_id)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id, round_id, team_id, token_index) DO UPDATE
		   SET slot_id = EXCLUDED.slot_id, amount = EXCLUDED.amount, placed_at = now()`,
		tenantID, roundID, teamID, tokenIndex, amount, slotID)
	if err != nil {
		return err
	}
	return nil
}

// ClearBet lifts a chip back off the board.
func ClearBet(ctx context.Context, pool *pgxpool.Pool, tenantID, roundID, teamID uuid.UUID, tokenIndex int) error {
	_, err := pool.Exec(ctx,
		`DELETE FROM app_trivia_bets
		  WHERE tenant_id = $1 AND round_id = $2 AND team_id = $3 AND token_index = $4`,
		tenantID, roundID, teamID, tokenIndex)
	if err != nil {
		return fmt.Errorf("clearing bet: %w", err)
	}
	return nil
}

// ListBets returns a round's chips.
func ListBets(ctx context.Context, q Querier, tenantID, roundID uuid.UUID) ([]Bet, error) {
	rows, err := q.Query(ctx,
		`SELECT team_id, token_index, amount, slot_id FROM app_trivia_bets
		  WHERE tenant_id = $1 AND round_id = $2 ORDER BY team_id, token_index`,
		tenantID, roundID)
	if err != nil {
		return nil, fmt.Errorf("listing bets: %w", err)
	}
	defer rows.Close()
	var out []Bet
	for rows.Next() {
		var b Bet
		if err := rows.Scan(&b.TeamID, &b.TokenIndex, &b.Amount, &b.SlotID); err != nil {
			return nil, fmt.Errorf("scanning bet: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RoundScore is one team's materialised delta for one round.
type RoundScore struct {
	RoundID     uuid.UUID
	TeamID      uuid.UUID
	BoardPoints int
	BetDelta    int
}

// WriteRoundScores persists a scored round inside the caller's transaction.
// The leaderboard then sums this table rather than replaying the engine, so
// the three surfaces cannot disagree about a total and a later bug fix cannot
// silently restate a game that already happened.
func WriteRoundScores(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scores []RoundScore) error {
	for _, s := range scores {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_trivia_round_scores (tenant_id, round_id, team_id, board_points, bet_delta)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (round_id, team_id) DO UPDATE
			   SET board_points = EXCLUDED.board_points, bet_delta = EXCLUDED.bet_delta`,
			tenantID, s.RoundID, s.TeamID, s.BoardPoints, s.BetDelta); err != nil {
			return fmt.Errorf("writing round score: %w", err)
		}
	}
	return nil
}

// Standing is one row of the leaderboard.
type Standing struct {
	TeamID uuid.UUID
	Total  int
}

// Leaderboard sums every scored round for a game. Teams with no scored round
// yet appear at zero rather than being absent -- a table that has answered
// nothing is still in the room and still on the TV.
func Leaderboard(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) ([]Standing, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, COALESCE(SUM(rs.board_points + rs.bet_delta), 0)::int
		  FROM app_trivia_teams t
		  LEFT JOIN app_trivia_round_scores rs
		         ON rs.team_id = t.id AND rs.tenant_id = t.tenant_id
		 WHERE t.tenant_id = $1 AND t.game_id = $2
		 GROUP BY t.id`, tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("querying leaderboard: %w", err)
	}
	defer rows.Close()
	var out []Standing
	for rows.Next() {
		var s Standing
		if err := rows.Scan(&s.TeamID, &s.Total); err != nil {
			return nil, fmt.Errorf("scanning standing: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ScoredRoundScores returns every stored delta for a game, for the recap.
func ScoredRoundScores(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) ([]RoundScore, error) {
	rows, err := q.Query(ctx, `
		SELECT rs.round_id, rs.team_id, rs.board_points, rs.bet_delta
		  FROM app_trivia_round_scores rs
		  JOIN app_trivia_rounds r ON r.id = rs.round_id AND r.tenant_id = rs.tenant_id
		 WHERE rs.tenant_id = $1 AND r.game_id = $2
		 ORDER BY r.ordinal`, tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing round scores: %w", err)
	}
	defer rows.Close()
	var out []RoundScore
	for rows.Next() {
		var s RoundScore
		if err := rows.Scan(&s.RoundID, &s.TeamID, &s.BoardPoints, &s.BetDelta); err != nil {
			return nil, fmt.Errorf("scanning round score: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
