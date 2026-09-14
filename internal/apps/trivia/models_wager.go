package trivia

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Wager is what one table put up in the final, locked before the question was
// read out.
//
// It is keyed on the ROUND rather than on an answer, which is the whole point
// of migration 098: at the moment a wager is committed there is no answer to
// hang it on, and there may never be one. A table that wagers and then fails
// to type a number still has a chip to place, and that chip is still worth
// what it said.
type Wager struct {
	TeamID uuid.UUID
	Amount int
}

// UpsertWager records or replaces a team's blind bet.
//
// Replacing until the clock runs out is deliberate and the phone says so: on a
// thirty-second clock a table that mis-drags a slider and cannot fix it has
// lost its night to the UI rather than to the question.
func UpsertWager(ctx context.Context, pool *pgxpool.Pool, tenantID, roundID, teamID uuid.UUID, amount int) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_trivia_wagers (tenant_id, round_id, team_id, amount)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (tenant_id, round_id, team_id) DO UPDATE
		   SET amount = EXCLUDED.amount, locked_at = now()`,
		tenantID, roundID, teamID, amount)
	if err != nil {
		return fmt.Errorf("upserting wager: %w", err)
	}
	return nil
}

// ListWagers returns a round's locked wagers.
//
// A team with no row here never locked one. That is a $0 bet rather than an
// error -- see chipAmount -- so callers look this up as a map and treat a miss
// as zero rather than refusing the chip.
func ListWagers(ctx context.Context, q Querier, tenantID, roundID uuid.UUID) ([]Wager, error) {
	rows, err := q.Query(ctx,
		`SELECT team_id, amount FROM app_trivia_wagers
		  WHERE tenant_id = $1 AND round_id = $2 ORDER BY locked_at, team_id`,
		tenantID, roundID)
	if err != nil {
		return nil, fmt.Errorf("listing wagers: %w", err)
	}
	defer rows.Close()
	var out []Wager
	for rows.Next() {
		var w Wager
		if err := rows.Scan(&w.TeamID, &w.Amount); err != nil {
			return nil, fmt.Errorf("scanning wager: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
