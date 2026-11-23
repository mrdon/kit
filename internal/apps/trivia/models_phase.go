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
// Two paths close a timed phase -- the sweeper, and the write guard
// (SweepDue) every mutating request runs first -- and they can race, as can
// the sweepers of two web processes. What makes that safe is that all of them
// issue exactly this conditional UPDATE: it fires only if the game is still in `from` and (when
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
		   SET phase = $4, phase_deadline = $5, paused_remaining_ms = NULL,
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
		   SET phase = $3, phase_deadline = $4, current_round_id = $5, paused_remaining_ms = NULL,
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
//
// A paused clock takes the time too: exactly one of the two columns is set,
// and NULL plus anything stays NULL, so the one statement extends whichever
// clock the phase actually has.
func ExtendDeadline(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, from Phase, extra time.Duration) (*Game, bool, error) {
	g, err := scanGame(pool.QueryRow(ctx, `
		UPDATE app_trivia_games
		   SET phase_deadline = phase_deadline + $4::interval,
		       paused_remaining_ms = paused_remaining_ms + $5,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2 AND phase = $3
		   AND (phase_deadline IS NOT NULL OR paused_remaining_ms IS NOT NULL)
		RETURNING `+gameColumns,
		tenantID, gameID, from, fmt.Sprintf("%d milliseconds", extra.Milliseconds()), int(extra.Milliseconds())))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("extending trivia deadline: %w", err)
	}
	return g, true, nil
}

// PausePhase stops a running clock where it is. The deadline goes to NULL,
// which is the whole mechanism: the sweeper, the lazy sweep and the early
// close all key on a deadline and simply stop seeing this game. Answers and
// chips still land, because those check the phase and never the clock -- the
// host pausing to explain something must not lock the room out.
//
// The remaining time is floored at the grace window rather than zero, so a
// pause pressed on the last tick resumes into a beat the room can see rather
// than a phase that closes as the button is released.
func PausePhase(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, from Phase) (*Game, bool, error) {
	g, err := scanGame(pool.QueryRow(ctx, fmt.Sprintf(`
		UPDATE app_trivia_games
		   SET paused_remaining_ms = GREATEST(%d,
		         (EXTRACT(EPOCH FROM (phase_deadline - now())) * 1000)::int),
		       phase_deadline = NULL,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2 AND phase = $3 AND phase_deadline IS NOT NULL
		RETURNING `+gameColumns, graceWindow.Milliseconds()),
		tenantID, gameID, from))
	return pauseResult(g, err, "pausing")
}

// ResumePhase re-arms a paused clock from what it had left.
func ResumePhase(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, from Phase) (*Game, bool, error) {
	g, err := scanGame(pool.QueryRow(ctx, `
		UPDATE app_trivia_games
		   SET phase_deadline = now() + paused_remaining_ms * interval '1 millisecond',
		       paused_remaining_ms = NULL,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2 AND phase = $3 AND paused_remaining_ms IS NOT NULL
		RETURNING `+gameColumns,
		tenantID, gameID, from))
	return pauseResult(g, err, "resuming")
}

// pauseResult maps "no row" to "nothing to do", the same convention as
// ExtendDeadline: a pause landing after the phase moved on is not an error.
func pauseResult(g *Game, err error, doing string) (*Game, bool, error) {
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%s trivia clock: %w", doing, err)
	}
	return g, true, nil
}

// ShortenDeadline pulls a live phase's deadline IN to now + grace. It is
// ExtendDeadline's mirror image and the only other thing in the app that
// moves a clock without moving a phase.
//
// The room is told the news by the clock itself: bumping state_version puts
// the new deadline on every phone and the TV in the same frame, so the
// countdown visibly drops to the grace instead of the phase simply vanishing
// out from under whoever acted last. The close that follows is the ORDINARY
// timer close -- the sweeper and the write guard take it as they always
// would -- so nothing new learned how to end a phase.
//
// Two guards, both load-bearing. `phase = $3` is the usual one: a shorten
// that lands just after the phase closed must do nothing rather than putting
// a stale clock on the next phase. `phase_deadline > now() + grace` makes the
// operation idempotent AND one-way: it never pushes a deadline out, so a
// table that lifts and re-places a chip during the grace re-triggers this and
// gets a no-op. THE GRACE IS NOT RE-ARMED BY MOVING WITHIN IT. That is
// deliberate -- an extendable grace is an unbounded phase, one table with a
// twitchy thumb holding the whole room -- and it is also why the grace is
// short enough to spend entirely on one change of mind.
func ShortenDeadline(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID, from Phase, grace time.Duration) (*Game, bool, error) {
	interval := fmt.Sprintf("%d milliseconds", grace.Milliseconds())
	g, err := scanGame(pool.QueryRow(ctx, `
		UPDATE app_trivia_games
		   SET phase_deadline = now() + $4::interval,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2 AND phase = $3
		   AND phase_deadline IS NOT NULL
		   AND phase_deadline > now() + $4::interval
		RETURNING `+gameColumns,
		tenantID, gameID, from, interval))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("shortening trivia deadline: %w", err)
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

// NextDeadlineIn reports how long until the earliest live deadline anywhere
// becomes sweepable (deadline + grace), measured on the database's clock so
// app/DB clock skew cannot make the sweeper wake early and find nothing. ok is
// false when no game has a running clock -- the normal state six days a week.
// Process-wide and not tenant-scoped, for the same reason as DueGames.
func NextDeadlineIn(ctx context.Context, pool *pgxpool.Pool) (time.Duration, bool, error) {
	var ms *int64
	err := pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT (EXTRACT(EPOCH FROM (min(phase_deadline) + interval '%d milliseconds' - now())) * 1000)::bigint
		  FROM app_trivia_games
		 WHERE phase_deadline IS NOT NULL`, graceWindow.Milliseconds())).Scan(&ms)
	if err != nil {
		return 0, false, fmt.Errorf("querying next trivia deadline: %w", err)
	}
	if ms == nil {
		return 0, false, nil
	}
	return time.Duration(*ms) * time.Millisecond, true, nil
}

// GameRef is the minimum the sweeper needs to act: which game, whose, and
// what phase it is leaving.
type GameRef struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Phase    Phase
}
