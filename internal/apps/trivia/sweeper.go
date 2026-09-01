package trivia

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// tickInterval is how often the process checks for an expired deadline. A
// countdown people are watching on a wall needs about a second; cron's floor
// is a minute.
const tickInterval = 500 * time.Millisecond

// SweepDue closes any phase of this game whose deadline has passed. THIS is
// the correctness guarantee, not the ticker: every read and write path calls
// it first, so phases advance correctly with no background machinery running
// at all, and a process restarted mid-round heals on the next request.
func (s *Service) SweepDue(ctx context.Context, tenantID, gameID uuid.UUID) error {
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return err
	}
	if game.PhaseDeadline == nil || time.Now().UTC().Before(game.PhaseDeadline.Add(graceWindow)) {
		return nil
	}
	return s.closePhase(ctx, game, game.Phase, true)
}

// StartSweeper runs the liveness loop.
//
// CLAUDE.md CARVE-OUT, considered rather than overlooked. The rule is
// "recurring work is declared with RegisterScheduledTask ... never a goroutine
// ticker", and it exists so tenant-scoped business work has run history,
// last_error and audit. The minute-granularity backstop DOES go in the
// scheduler, in schedule.go, exactly as the rule requires. This 500ms loop
// structurally cannot: cron is five-field, so one minute is its floor, and a
// per-tick jobs row would be pure noise in a table meant for auditable work.
//
// What makes the exception safe is that this goroutine owns nothing. All game
// state is in Postgres; it stores no state, holds no locks, and issues the
// same guarded conditional UPDATE every other path issues -- so two processes
// both ticking is a no-op for the loser, and a crashed process leaves nothing
// to reconstruct. Its failure mode costs latency, not correctness: without it
// the game is still correct, just frozen until somebody touches it, because
// SweepDue heals on the next request either way.
//
// One shared ticker for the process, not one timer per game: one bounded
// indexed query per tick regardless of how many games exist, no per-game
// lifecycle to leak, and nothing to rebuild after a restart.
func (s *Service) StartSweeper(ctx context.Context) {
	go func() {
		t := time.NewTicker(tickInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweepAll(ctx)
			}
		}
	}()
}

// sweepAll closes every expired phase anywhere and publishes the ones that
// actually moved.
func (s *Service) sweepAll(ctx context.Context) {
	due, err := DueGames(ctx, s.pool)
	if err != nil {
		slog.Warn("trivia sweep query failed", "error", err)
		return
	}
	for _, ref := range due {
		s.sweepOne(ctx, ref)
	}
}

// SweepTenant is the scheduled backstop's entry point, scoped to one tenant's
// job row.
func (s *Service) SweepTenant(ctx context.Context, tenantID uuid.UUID) error {
	due, err := DueGamesForTenant(ctx, s.pool, tenantID)
	if err != nil {
		return err
	}
	for _, ref := range due {
		s.sweepOne(ctx, ref)
	}
	return nil
}

// sweepOne closes one game's expired phase. A zero-row guarded UPDATE means
// somebody beat us to it, and closePhase returns nil having done nothing --
// so the check below is "did the version actually move", not "did we win".
func (s *Service) sweepOne(ctx context.Context, ref GameRef) {
	game, err := GetGame(ctx, s.pool, ref.TenantID, ref.ID)
	if err != nil {
		return
	}
	before := game.StateVersion
	if err := s.closePhase(ctx, game, game.Phase, true); err != nil {
		slog.Warn("trivia sweep failed to close phase",
			"game_id", ref.ID, "phase", game.Phase, "error", err)
		return
	}
	after, err := GetGame(ctx, s.pool, ref.TenantID, ref.ID)
	if err != nil || after.StateVersion == before {
		return // nothing moved: another process or another layer got there
	}
	s.publish(ctx, ref.TenantID, ref.ID)
}

// maybeCloseEarly ends a phase before its deadline when there is nothing left
// to wait for: everyone has answered, or every chip is down.
//
// Worth doing rather than always burning the clock -- a room of three teams
// should not sit through sixty seconds of silence -- and it is the same
// guarded transition, so racing the timer is harmless.
func (s *Service) maybeCloseEarly(ctx context.Context, game *Game) bool {
	if game.CurrentRoundID == nil {
		return false
	}
	ready, err := s.everyoneIn(ctx, game)
	if err != nil || !ready {
		return false
	}
	if err := s.closePhase(ctx, game, game.Phase, false); err != nil {
		slog.Warn("trivia early close failed", "game_id", game.ID, "error", err)
		return false
	}
	return true
}

// everyoneIn is the early-close test for the two phases that have one.
func (s *Service) everyoneIn(ctx context.Context, game *Game) (bool, error) {
	round, err := GetRound(ctx, s.pool, game.TenantID, *game.CurrentRoundID)
	if err != nil {
		return false, err
	}
	teams, err := ListTeams(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return false, err
	}
	eligible := make([]uuid.UUID, 0, len(teams))
	for _, t := range teams {
		if t.EligibleFromOrdinal <= round.Ordinal {
			eligible = append(eligible, t.ID)
		}
	}
	if len(eligible) == 0 {
		return false, nil
	}

	switch game.Phase {
	case PhaseQuestion:
		answers, err := ListAnswers(ctx, s.pool, game.TenantID, round.ID)
		if err != nil {
			return false, err
		}
		in := map[uuid.UUID]bool{}
		for _, a := range answers {
			in[a.TeamID] = true
		}
		for _, id := range eligible {
			if !in[id] {
				return false, nil
			}
		}
		return true, nil

	case PhaseBetting:
		bets, err := ListBets(ctx, s.pool, game.TenantID, round.ID)
		if err != nil {
			return false, err
		}
		// A round with one card has nowhere to spread two chips, so the
		// requirement is however many chips can legally be placed.
		slots, err := ListSlots(ctx, s.pool, game.TenantID, round.ID)
		if err != nil {
			return false, err
		}
		want := len(game.TokenValues)
		if round.IsFinal {
			want = 1
		}
		if len(slots) < want {
			want = len(slots)
		}
		count := map[uuid.UUID]int{}
		for _, b := range bets {
			count[b.TeamID]++
		}
		for _, id := range eligible {
			if count[id] < want {
				return false, nil
			}
		}
		return true, nil

	case PhaseSetup, PhaseLobby, PhaseBoard, PhaseReveal, PhaseScoring, PhasePodium:
		// No early close: reveal is a fixed beat the room watches, and the
		// rest are not waiting on anybody's input.
		return false, nil
	}
	return false, nil
}
