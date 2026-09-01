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

// graceWindow is how long past a deadline a submission still lands, and how
// long the closing UPDATE waits before it fires.
//
// A phone on bar wifi tapping submit at T-0.2s deserves to count, and the TV
// holding a beat at zero before the cards flip reads as drama rather than
// lag. The countdown shows zero at the deadline; this is slack behind it, not
// time anyone sees.
const graceWindow = 1500 * time.Millisecond

// AdvancePhase is the one guarded transition every path in the app issues.
//
// Three layers close a timed phase -- the lazy sweep on every request, the
// process ticker, and the scheduled backstop -- and they run concurrently by
// design. What makes that safe is that all three issue exactly this
// conditional UPDATE: it fires only if the game is still in `from` and (when
// requireDeadline) the deadline plus the grace window has actually passed.
// Zero rows updated means somebody already advanced, so the caller does
// nothing and publishes nothing.
//
// deadline is the new phase's absolute expiry, or nil for a phase that waits
// on a human (the board between questions, the podium). It is computed
// server-side from the game's own settings and never accepted from a client.
func AdvancePhase(ctx context.Context, db Querier, tenantID, gameID uuid.UUID, from, to Phase, deadline *time.Time, requireDeadline bool) (*Game, bool, error) {
	q := `
		UPDATE app_trivia_games
		   SET phase = $4, phase_deadline = $5,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2 AND phase = $3`
	args := []any{tenantID, gameID, from, to, deadline}
	if requireDeadline {
		q += fmt.Sprintf(` AND phase_deadline IS NOT NULL
		   AND phase_deadline + interval '%d milliseconds' <= now()`, graceWindow.Milliseconds())
	}
	q += ` RETURNING ` + gameColumns

	g, err := scanGame(db.QueryRow(ctx, q, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("advancing trivia phase %s->%s: %w", from, to, err)
	}
	return g, true, nil
}

// SetPhaseUnconditional moves a game regardless of where it was. Reserved for
// the host's "end game" -- legal from any phase -- and for the setup-time
// moves that no timer competes over. Everything driven by a clock goes
// through AdvancePhase so it can lose the race harmlessly.
func SetPhaseUnconditional(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, to Phase, deadline *time.Time, roundID *uuid.UUID) (*Game, error) {
	g, err := scanGame(pool.QueryRow(ctx, `
		UPDATE app_trivia_games
		   SET phase = $3, phase_deadline = $4, current_round_id = $5,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2
		RETURNING `+gameColumns,
		tenantID, gameID, to, deadline, roundID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("setting trivia phase: %w", err)
	}
	return g, nil
}

// ExtendDeadline is the host's +15s. It only ever pushes the deadline out and
// only while the game is in the phase the host thinks it is, so a click that
// lands just after a phase closed does nothing rather than reopening it.
func ExtendDeadline(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, from Phase, extra time.Duration) (*Game, bool, error) {
	g, err := scanGame(pool.QueryRow(ctx, `
		UPDATE app_trivia_games
		   SET phase_deadline = phase_deadline + $4::interval,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2 AND phase = $3 AND phase_deadline IS NOT NULL
		RETURNING `+gameColumns,
		tenantID, gameID, from, fmt.Sprintf("%d milliseconds", extra.Milliseconds())))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("extending trivia deadline: %w", err)
	}
	return g, true, nil
}

// BumpVersion records that something under a game changed without moving the
// phase -- an answer landing, a chip moving, a team joining. It is what turns
// those writes into a frame on every stream.
func BumpVersion(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_trivia_games SET state_version = state_version + 1, updated_at = now()
		  WHERE tenant_id = $1 AND id = $2`, tenantID, gameID)
	if err != nil {
		return fmt.Errorf("bumping trivia state version: %w", err)
	}
	return nil
}

// DueGames returns every game whose deadline has passed, across all tenants.
//
// This is the one read in the app that is not tenant-scoped, deliberately:
// the sweeper is process-wide infrastructure asking "has any clock anywhere
// run out", and every write it issues in response is tenant-filtered through
// AdvancePhase. The partial index on phase_deadline makes a tick with no live
// game an index scan over an empty set.
func DueGames(ctx context.Context, pool *pgxpool.Pool) ([]GameRef, error) {
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT id, tenant_id, phase FROM app_trivia_games
		 WHERE phase_deadline IS NOT NULL
		   AND phase_deadline + interval '%d milliseconds' <= now()
		 LIMIT 200`, graceWindow.Milliseconds()))
	if err != nil {
		return nil, fmt.Errorf("querying due trivia games: %w", err)
	}
	defer rows.Close()
	var out []GameRef
	for rows.Next() {
		var g GameRef
		if err := rows.Scan(&g.ID, &g.TenantID, &g.Phase); err != nil {
			return nil, fmt.Errorf("scanning due game: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GameRef is the minimum the sweeper needs to act: which game, whose, and
// what phase it is leaving.
type GameRef struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Phase    Phase
}

// DueGamesForTenant is the scheduled backstop's version, scoped to the tenant
// whose job row is running.
func DueGamesForTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]GameRef, error) {
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT id, tenant_id, phase FROM app_trivia_games
		 WHERE tenant_id = $1 AND phase_deadline IS NOT NULL
		   AND phase_deadline + interval '%d milliseconds' <= now()`, graceWindow.Milliseconds()),
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying due trivia games for tenant: %w", err)
	}
	defer rows.Close()
	var out []GameRef
	for rows.Next() {
		var g GameRef
		if err := rows.Scan(&g.ID, &g.TenantID, &g.Phase); err != nil {
			return nil, fmt.Errorf("scanning due game: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
