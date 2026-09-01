package trivia

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// joinCodeAlphabet is base32 with the characters that get misread stripped
// out: no i or l (they look like 1), no o (looks like 0), no u (heard as
// "you" when a code is read across a room).
const joinCodeAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// joinCodeLength is five characters, about a million codes. The unique index
// plus a retry is the guarantee; this is the size that keeps the URL short
// enough to matter for the QR.
const joinCodeLength = 5

// NewJoinCode draws a code. crypto/rand for the same reason the game name
// uses it: a process restarting twice during a deploy must not hand two games
// the same seed.
func NewJoinCode() string {
	var b strings.Builder
	for range joinCodeLength {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(joinCodeAlphabet))))
		if err != nil {
			return ""
		}
		b.WriteByte(joinCodeAlphabet[n.Int64()])
	}
	return b.String()
}

// IsValidJoinCode guards the root-level route before it reaches a query.
func IsValidJoinCode(s string) bool {
	if len(s) < 4 || len(s) > 12 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(joinCodeAlphabet, r) {
			return false
		}
	}
	return true
}

// GameByJoinCode resolves a short code to its game and the workspace slug it
// belongs to.
//
// This is the ONE query in the app that is not tenant-scoped, and it has to
// be: the code exists precisely so the URL does not carry a workspace. It is
// safe because the code IS the identifier — it selects exactly one row, and
// the page it leads to is public by design. Everything downstream is scoped
// by the tenant this returns.
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
		&game.AnswerSeconds, &game.RevealSeconds, &game.BetSeconds,
		&game.CurrentRoundID, &game.PhaseDeadline, &game.StateVersion,
		&game.CreatedBy, &game.CreatedAt, &game.UpdatedAt, &game.JoinCode, &slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("resolving trivia join code: %w", err)
	}
	return &game, slug, nil
}

// AssignJoinCode gives a game a code, retrying on the unique index. Ten draws
// from a million is not a loop that runs twice in practice.
func AssignJoinCode(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID) (string, error) {
	for range 10 {
		code := NewJoinCode()
		if code == "" {
			return "", errors.New("trivia: could not generate a join code")
		}
		_, err := pool.Exec(ctx,
			`UPDATE app_trivia_games SET join_code = $3 WHERE tenant_id = $1 AND id = $2`,
			tenantID, gameID, code)
		if err == nil {
			return code, nil
		}
		if !isUniqueViolation(err) {
			return "", fmt.Errorf("assigning trivia join code: %w", err)
		}
	}
	return "", errors.New("trivia: could not find an unused join code")
}

// ShortJoinURL is what the QR encodes and what the TV prints. It is
// deliberately the SHORTEST correct thing: no workspace, no game name.
func ShortJoinURL(baseURL, code string) string {
	return strings.TrimRight(baseURL, "/") + "/j/t/" + code
}
