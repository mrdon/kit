package trivia

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/livegame"
)

// shortJoinRoute is trivia's segment of the root-level short join link,
// /j/t/{code}. The codes themselves are livegame's.
const shortJoinRoute = "t"

// GameByJoinCode resolves a short code to its game and the workspace slug it
// belongs to.
//
// This is the ONE query in the app that is not tenant-scoped, and it has to
// be: the code exists precisely so the URL does not carry a workspace. It is
// safe because the code IS the identifier — it selects exactly one row, and
// the page it leads to is public by design. Everything downstream is scoped
// by the tenant this returns.
//
// The scan below is hand-rolled rather than scanGame's, because of the extra
// slug column — so IT HAS TO TRACK gameColumnsQualified. Add a column there
// and forget this and the slug lands in the new field, the scan errors, and
// every QR in the room 404s. (Ask how that is known.)
func GameByJoinCode(ctx context.Context, pool *pgxpool.Pool, code string) (*Game, string, error) {
	var slug string
	row := pool.QueryRow(ctx, `
		SELECT `+gameColumnsQualified+`, t.slug
		  FROM app_trivia_games g
		  JOIN tenants t ON t.id = g.tenant_id
		 WHERE g.join_code = $1`, code)

	var game Game
	err := row.Scan(&game.ID, &game.TenantID, &game.Name, &game.Title, &game.Phase,
		&game.BoardRows, &game.BoardColumns, &game.CellValues, &game.TokenValues, &game.FinalWager,
		&game.AnswerSeconds, &game.RevealSeconds, &game.BetSeconds, &game.WagerSeconds,
		&game.GraceSeconds, &game.CurrentRoundID, &game.PhaseDeadline, &game.StateVersion,
		&game.CreatedBy, &game.CreatedAt, &game.UpdatedAt, &game.JoinCode,
		&game.PickerTeamID, &game.PickerReason, &game.RepeatQuestions, &game.BoardRounds,
		&game.BreakBeforeFinal, &game.PausedRemainingMs, &slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("resolving trivia join code: %w", err)
	}
	return &game, slug, nil
}

// AssignJoinCode gives a game a code, retrying on the unique index.
func AssignJoinCode(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID) (string, error) {
	code, err := livegame.AssignJoinCode(func(code string) error {
		_, err := pool.Exec(ctx,
			`UPDATE app_trivia_games SET join_code = $3 WHERE tenant_id = $1 AND id = $2`,
			tenantID, gameID, code)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("trivia: %w", err)
	}
	return code, nil
}

// ShortJoinURL is what the QR encodes and what the TV prints. It is
// deliberately the SHORTEST correct thing: no workspace, no game name.
func ShortJoinURL(baseURL, code string) string {
	return livegame.ShortJoinURL(baseURL, shortJoinRoute, code)
}
