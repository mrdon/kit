package trivia

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// minWait floors the sleep so a deadline the database has not quite reached
// yet cannot turn the loop into a spin.
const minWait = 250 * time.Millisecond

// retryWait is how long to back off after the next-deadline lookup fails.
const retryWait = 5 * time.Second

// SweepDue closes this game's phase if its deadline has passed, and
// publishes when it did.
//
// Ending countdowns is the sweeper's job, not this one. This is the WRITE
// GUARD: every path that changes a game (host action, join, answer, wager,
// bet) calls it first, so an answer arriving in the instant between a clock
// running out and the sweeper waking is judged against the phase the game is
// really in, never the expired one. Read paths do not call it -- the sweeper
// already has, to the millisecond.
//
// It publishes when it moves the game, because the caller may go on to reject
// its own write (ErrClosed) and publish nothing, and the room -- and the
// sweeper, which re-arms on publish -- must still hear the phase change.
func (s *Service) SweepDue(ctx context.Context, tenantID, gameID uuid.UUID) error {
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return err
	}
	if game.PhaseDeadline == nil || time.Now().UTC().Before(game.PhaseDeadline.Add(graceWindow)) {
		return nil
	}
	return s.closeExpired(ctx, game)
}

// closeExpired closes game's timed-out phase and publishes if the game moved.
// A zero-row guarded UPDATE means somebody beat us to it, and closePhase
// returns nil having done nothing -- so the check is "did the version
// actually move", not "did we win".
func (s *Service) closeExpired(ctx context.Context, game *Game) error {
	before := game.StateVersion
	if err := s.closePhase(ctx, game, game.Phase, true); err != nil {
		return err
	}
	after, err := GetGame(ctx, s.pool, game.TenantID, game.ID)
	if err != nil || after.StateVersion == before {
		return nil // nothing moved: another process or another path got there
	}
	s.publish(ctx, game.TenantID, game.ID)
	return nil
}

// StartSweeper runs the liveness loop.
//
// CLAUDE.md CARVE-OUT, considered rather than overlooked. The rule is
// "recurring work is declared with RegisterScheduledTask ... never a goroutine
// ticker", and it exists so tenant-scoped business work has run history,
// last_error and audit. This loop exists because a countdown people are
// watching on a wall needs to end within about a second, and cron's floor is
// a minute. It is the ONE thing that ends countdowns: there is no scheduled
// backstop (the process that would run it is this process), and reads do
// not sweep.
//
// It is NOT a ticker. It sleeps until the earliest running clock anywhere
// expires, and with no game live -- all week but an hour or so -- it sleeps
// indefinitely and issues no queries at all. Any publish (local or relayed
// from another process) wakes it to re-read the next deadline, because every
// write that sets, shortens, extends, pauses or clears a deadline publishes.
// It re-reads the deadline from the database on every wake, so it cannot
// drift from what is stored; and SweepDue publishes when a write path closes
// a phase first, so no deadline is ever set without a wake.
//
// What makes it safe is that this goroutine owns nothing. All game state is
// in Postgres; it holds no locks, and issues the same guarded conditional
// UPDATE every other path issues -- so two processes both waking is a no-op
// for the loser, and a crashed process leaves nothing to reconstruct.
func (s *Service) StartSweeper(ctx context.Context) {
	go s.runSweeper(ctx)
}

func (s *Service) runSweeper(ctx context.Context) {
	timer := time.NewTimer(0) // one sweep at boot heals anything that expired while down
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.broker.Changed():
		}
		s.sweepAll(ctx)
		s.rearm(ctx, timer)
	}
}

// rearm points the timer at the next deadline, or parks it when there is none.
// Go 1.23+ timers need no drain before Stop/Reset.
func (s *Service) rearm(ctx context.Context, timer *time.Timer) {
	wait, ok, err := NextDeadlineIn(ctx, s.pool)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			slog.Warn("trivia next-deadline lookup failed", "error", err)
		}
		timer.Reset(retryWait)
	case !ok:
		timer.Stop()
	default:
		timer.Reset(max(wait, minWait))
	}
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

// sweepOne closes one due game's expired phase.
func (s *Service) sweepOne(ctx context.Context, ref GameRef) {
	game, err := GetGame(ctx, s.pool, ref.TenantID, ref.ID)
	if err != nil {
		return
	}
	if err := s.closeExpired(ctx, game); err != nil {
		slog.Warn("trivia sweep failed to close phase",
			"game_id", ref.ID, "phase", game.Phase, "error", err)
	}
}

// maybeCloseEarly cuts a phase short when there is nothing left to wait for:
// everyone has answered, everyone has wagered, or every chip is down.
//
// Worth doing rather than always burning the clock -- a room of three teams
// should not sit through sixty seconds of silence -- but "cut short" is a
// SHORTENED DEADLINE, not an immediate close, and that distinction is the
// whole point of the grace setting.
//
// Closing instantly punishes precisely one table per round: the one that
// acted last. Its chip ends betting the moment it lands, so it alone never
// gets to look at the wall with its own money on it, and alone cannot change
// its mind -- the beat every other table in the room had for free. Pulling
// the deadline in to now + grace gives it that beat, in public, on the same
// countdown everyone is already watching, and then lets the phase end the way
// every other phase ends: by the timer.
//
// grace of 0 keeps the old behaviour exactly, for a host who wants the night
// to move.
func (s *Service) maybeCloseEarly(ctx context.Context, game *Game) bool {
	if game.CurrentRoundID == nil {
		return false
	}
	ready, err := s.everyoneIn(ctx, game)
	if err != nil || !ready {
		return false
	}
	if game.GraceSeconds <= 0 {
		if err := s.closePhase(ctx, game, game.Phase, false); err != nil {
			slog.Warn("trivia early close failed", "game_id", game.ID, "error", err)
			return false
		}
		return true
	}
	grace := time.Duration(game.GraceSeconds) * time.Second
	_, moved, err := ShortenDeadline(ctx, s.pool, game.TenantID, game.ID, game.Phase, grace)
	if err != nil {
		slog.Warn("trivia grace shorten failed", "game_id", game.ID, "error", err)
		return false
	}
	// moved == false is the ordinary case on every call after the first: the
	// deadline is already inside the grace, so there is nothing to pull in
	// and nothing to re-arm.
	return moved
}

// everyoneIn is the early-close test for the two phases that have one.
func (s *Service) everyoneIn(ctx context.Context, game *Game) (bool, error) {
	round, err := GetRound(ctx, s.pool, game.TenantID, *game.CurrentRoundID)
	if err != nil {
		return false, err
	}
	// Practice never closes itself. The host is talking the room through
	// each step, and the phase ending because the last table tapped submit
	// cuts them off mid-sentence; the button moves practice on.
	if round.IsPractice {
		return false, nil
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
	case PhaseWager:
		// Everybody has put their money up, with nobody having seen the
		// question. Holding the room on a countdown at that point is twenty
		// seconds of silence before the one moment of the night everybody
		// came for.
		wagers, err := ListWagers(ctx, s.pool, game.TenantID, round.ID)
		if err != nil {
			return false, err
		}
		in := map[uuid.UUID]bool{}
		for _, w := range wagers {
			in[w.TeamID] = true
		}
		for _, id := range eligible {
			if !in[id] {
				return false, nil
			}
		}
		return true, nil

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
		// Every chip can always go down now that stacking is allowed -- even a
		// round showing only the pseudo-slot takes both -- so the requirement
		// is simply the chips the team was dealt.
		want := ChipsPerTable(game.TokenValues, round.IsFinal)
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

	case PhaseSetup, PhaseLobby, PhaseBoard, PhaseIntermission, PhaseReveal, PhaseScoring, PhasePodium:
		// No early close: reveal is a fixed beat the room watches, and the
		// rest are not waiting on anybody's input. The break least of all --
		// it ends when the host says the room is back, not when a count of
		// somebodies reaches a total.
		return false, nil
	}
	return false, nil
}
