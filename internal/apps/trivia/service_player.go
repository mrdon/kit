package trivia

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrGameFull means the twenty-first team tried to join.
var ErrGameFull = errors.New("trivia: this game is full")

// ErrNameTaken means another table already has that name.
var ErrNameTaken = errors.New("trivia: that team name is taken")

// uniqueViolation is Postgres 23505. The unique indexes are the real
// concurrency guards in this app, so recognising the code and translating it
// to a friendly error is a first-class path, not an edge case.
const uniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	return pgErrCode(err) == uniqueViolation
}

// pgErrCode returns the SQLSTATE of a Postgres error, or "" for anything
// else. The unique and foreign-key indexes are the real concurrency and
// integrity guards in this app, so recognising their codes is a first-class
// path rather than an edge case.
func pgErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// Join adds a team and mints its identity token.
//
// The name check is the unique index, not a read-then-write: two phones
// typing "Bar Flies" at the same moment would both pass a check-first and one
// would then fail confusingly at insert.
func (s *Service) Join(ctx context.Context, tenantID, gameID uuid.UUID, name string) (*Team, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 40 {
		return nil, "", fmt.Errorf("%w: a team name is 1-40 characters", ErrBadRequest)
	}
	if err := s.SweepDue(ctx, tenantID, gameID); err != nil {
		return nil, "", err
	}
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, "", err
	}
	if game.Phase == PhaseAwards || game.Phase == PhasePodium {
		return nil, "", fmt.Errorf("%w: this game has finished", ErrClosed)
	}
	n, err := CountTeams(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, "", err
	}
	if n >= MaxTeams {
		return nil, "", ErrGameFull
	}

	eligibleFrom, err := s.joinEligibleFrom(ctx, tenantID, game)
	if err != nil {
		return nil, "", err
	}

	token := NewTeamToken()
	team, err := InsertTeam(ctx, s.pool, tenantID, gameID, name, HashToken(token), eligibleFrom)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, "", ErrNameTaken
		}
		return nil, "", fmt.Errorf("inserting team: %w", err)
	}
	if err := BumpVersion(ctx, s.pool, tenantID, gameID); err != nil {
		return nil, "", err
	}
	s.publish(ctx, tenantID, gameID)
	return team, token, nil
}

// joinEligibleFrom decides which question a newcomer is in from, and closes
// the door entirely once the final is under way.
//
// Joining late is deliberately open all night. Walk in on question seven,
// take a $0 seat, and the betting plus the final's wager still leave a real
// shot at the room -- which is most of what makes a 9pm arrival worth
// selling. A table arriving mid-question is not in THAT question's
// denominator though, so it becomes eligible from the next one; without that
// the TV's "12 of 20 answered" ticks backwards as latecomers land.
//
// The final is where it stops. A table that arrives after the final opens
// cannot answer it, cannot wager into it, and the next thing that happens is
// the podium -- so the seat is a phone that says "waiting" until the lights
// come up. Better to say so at the door than sell a ticket to nothing.
//
// The test is the current round being FINAL rather than the phase name, on
// purpose: the final re-enters the ordinary question phase and passes through
// reveal, betting and scoring on its way out, and a wager phase may land in
// front of it. One condition covers all of them, including phases that do not
// exist yet.
func (s *Service) joinEligibleFrom(ctx context.Context, tenantID uuid.UUID, game *Game) (int, error) {
	if game.CurrentRoundID == nil {
		return 1, nil
	}
	round, err := GetRound(ctx, s.pool, tenantID, *game.CurrentRoundID)
	if err != nil {
		return 0, err
	}
	if round.IsFinal {
		return 0, fmt.Errorf("%w: the final question is under way — this game is closing", ErrClosed)
	}
	return round.Ordinal + 1, nil
}

// assertEligible refuses an action from a table that joined after this round
// opened.
//
// Shared by the answer and the chip deliberately. A latecomer that could not
// answer but could still put money on the room's cards would be betting on a
// question it was excluded from -- and in this game the chips are the half of
// the round that actually pays, so that is the bigger hole of the two.
func (s *Service) assertEligible(ctx context.Context, tenantID, gameID, teamID uuid.UUID, round *Round) error {
	team, err := teamByID(ctx, s.pool, tenantID, gameID, teamID)
	if err != nil {
		return err
	}
	if team.EligibleFromOrdinal > round.Ordinal {
		return fmt.Errorf("%w: you joined during this question — you're in from the next one", ErrClosed)
	}
	return nil
}

// SubmitAnswer records a team's number.
//
// There is no stake here any more. In a final the wager was committed a phase
// earlier, against the category alone -- see SetWager -- so by the time this
// runs the final is an ordinary question and this function has no idea it is
// one.
//
// Resubmitting until the deadline is allowed on purpose, and the phone says
// so: on a sixty-second clock, fat-finger anxiety costs more than a late
// edit does.
func (s *Service) SubmitAnswer(ctx context.Context, tenantID, gameID, teamID uuid.UUID, raw string) error {
	if err := s.SweepDue(ctx, tenantID, gameID); err != nil {
		return err
	}
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return err
	}
	if game.Phase != PhaseQuestion || game.CurrentRoundID == nil {
		return ErrClosed
	}
	value, ok := ParseAnswer(raw)
	if !ok {
		return fmt.Errorf("%w: %q is not a number", ErrBadRequest, raw)
	}
	round, err := GetRound(ctx, s.pool, tenantID, *game.CurrentRoundID)
	if err != nil {
		return err
	}
	// A team that arrived mid-round watches this one out. Letting it answer
	// would put it in a denominator it was excluded from.
	if err := s.assertEligible(ctx, tenantID, gameID, teamID, round); err != nil {
		return err
	}

	if err := UpsertAnswer(ctx, s.pool, tenantID, round.ID, teamID, value, strings.TrimSpace(raw)); err != nil {
		return err
	}
	if err := BumpVersion(ctx, s.pool, tenantID, gameID); err != nil {
		return err
	}
	// Everybody's in: close early rather than making a room of three sit
	// through the rest of the clock.
	if reloaded, err := GetGame(ctx, s.pool, tenantID, gameID); err == nil {
		s.maybeCloseEarly(ctx, reloaded)
	}
	s.publish(ctx, tenantID, gameID)
	return nil
}

// SetWager locks a table's blind bet on the final, before the question exists
// on any public surface.
//
// Legal ONLY during PhaseWager. That is the whole mechanic: once the prompt is
// on the wall the amount is frozen, because a wager a table can revise after
// reading the question is not a wager, it is a calculation. The phase machinery
// enforces it rather than a flag -- ErrClosed here is the same refusal a late
// answer gets.
func (s *Service) SetWager(ctx context.Context, tenantID, gameID, teamID uuid.UUID, amount int) error {
	if err := s.SweepDue(ctx, tenantID, gameID); err != nil {
		return err
	}
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return err
	}
	if game.Phase != PhaseWager || game.CurrentRoundID == nil {
		return ErrClosed
	}
	round, err := GetRound(ctx, s.pool, tenantID, *game.CurrentRoundID)
	if err != nil {
		return err
	}
	team, err := teamByID(ctx, s.pool, tenantID, gameID, teamID)
	if err != nil {
		return err
	}
	if team.EligibleFromOrdinal > round.Ordinal {
		// A table that walked in during the final is not in its denominator,
		// so it has no wager to make either. Letting it bet would put money on
		// a question it was excluded from.
		return fmt.Errorf("%w: you joined during the final — this one is not yours", ErrClosed)
	}

	clamped, err := s.clampWager(ctx, game, teamID, amount)
	if err != nil {
		return err
	}
	if err := UpsertWager(ctx, s.pool, tenantID, round.ID, teamID, clamped); err != nil {
		return err
	}
	if err := BumpVersion(ctx, s.pool, tenantID, gameID); err != nil {
		return err
	}
	// Everybody is in: read the question rather than watching a room that has
	// already decided sit out the rest of the clock.
	if reloaded, err := GetGame(ctx, s.pool, tenantID, gameID); err == nil {
		s.maybeCloseEarly(ctx, reloaded)
	}
	s.publish(ctx, tenantID, gameID)
	return nil
}

// clampWager bounds a wager to the team's own bank, SERVER-SIDE. The phone
// mirrors the clamp so the slider cannot express an impossible bet, but a
// hand-edited request has to be CLAMPED rather than rejected: rejecting would
// let a table lose its final to a typo.
func (s *Service) clampWager(ctx context.Context, game *Game, teamID uuid.UUID, amount int) (int, error) {
	standings, err := Leaderboard(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return 0, err
	}
	bank := 0
	for _, st := range standings {
		if st.TeamID == teamID {
			bank = st.Total
		}
	}
	return min(max(amount, 0), bank), nil
}

// PlaceChip puts one token on one card, or lifts it off with a nil slot.
//
// A PUT of the desired placement rather than an append, so every retry over
// flaky bar wifi is idempotent. Both chips may sit on the SAME card -- see
// migration 097 for why the forced spread went away. The one unique index
// left is (round, team, token_index), which makes moving a chip an UPDATE so
// a double-tap cannot double a team's money.
func (s *Service) PlaceChip(ctx context.Context, tenantID, gameID, teamID uuid.UUID, tokenIndex int, slotID *uuid.UUID, amount int) error {
	if err := s.SweepDue(ctx, tenantID, gameID); err != nil {
		return err
	}
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return err
	}
	if game.Phase != PhaseBetting || game.CurrentRoundID == nil {
		return ErrClosed
	}
	round, err := GetRound(ctx, s.pool, tenantID, *game.CurrentRoundID)
	if err != nil {
		return err
	}
	// The same gate the answer phase applies. This one was missing: a table
	// that joined mid-question was correctly barred from answering and then
	// walked straight into the betting on that very question.
	if err := s.assertEligible(ctx, tenantID, gameID, teamID, round); err != nil {
		return err
	}
	if slotID == nil {
		if err := ClearBet(ctx, s.pool, tenantID, round.ID, teamID, tokenIndex); err != nil {
			return err
		}
	} else {
		amount, err = s.chipAmount(ctx, game, round, teamID, tokenIndex, amount)
		if err != nil {
			return err
		}
		if err := s.assertSlotInRound(ctx, tenantID, round.ID, *slotID); err != nil {
			return err
		}
		if err := PlaceBet(ctx, s.pool, tenantID, round.ID, teamID, tokenIndex, amount, *slotID); err != nil {
			if isUniqueViolation(err) {
				// The token index is the only unique key left, and PlaceBet
				// upserts on it -- so a violation here is no longer a rule
				// being enforced, it is two requests for the SAME chip
				// racing each other. There is nothing a player can do about
				// that by tapping somewhere else, so report what it was.
				return fmt.Errorf("placing chip %d: two placements raced: %w", tokenIndex, err)
			}
			return fmt.Errorf("placing bet: %w", err)
		}
	}
	if err := BumpVersion(ctx, s.pool, tenantID, gameID); err != nil {
		return err
	}
	if reloaded, err := GetGame(ctx, s.pool, tenantID, gameID); err == nil {
		s.maybeCloseEarly(ctx, reloaded)
	}
	s.publish(ctx, tenantID, gameID)
	return nil
}

// chipAmount decides what a chip is worth, server-side. During the board it
// is the game's token value for that index -- a client cannot name its own
// number. In a final it is the wager the team locked BEFORE IT SAW THE
// QUESTION, which is what makes the final a wager rather than a calculation.
func (s *Service) chipAmount(ctx context.Context, game *Game, round *Round, teamID uuid.UUID, tokenIndex, _ int) (int, error) {
	if round.IsFinal {
		wagers, err := ListWagers(ctx, s.pool, game.TenantID, round.ID)
		if err != nil {
			return 0, err
		}
		for _, w := range wagers {
			if w.TeamID == teamID {
				return w.Amount, nil
			}
		}
		// A table that sat out the wager phase has nothing to place. $0 is a
		// legal bet, so this is a no-op rather than an error -- and its chip
		// still lands on a card, which is how the room sees it played along.
		return 0, nil
	}
	if tokenIndex < 0 || tokenIndex >= len(game.TokenValues) {
		return 0, fmt.Errorf("%w: no such chip", ErrBadRequest)
	}
	// Scaled by THIS ROUND's board, exactly as the phone was shown it, and
	// derived here rather than trusted from the client so a stale phone
	// cannot bet round one's $100 into round two. ScaleRoundOf reads the
	// round's own cell; asking the board instead over-paid every round's last
	// question, which is the one being bet on at the moment the board empties.
	cells, err := ListBoardCells(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return 0, err
	}
	return game.TokenValues[tokenIndex] * boardMultiplier(ScaleRoundOf(round, cells)), nil
}

// assertSlotInRound stops a chip landing on a card from a different round --
// which a stale phone that missed a transition would otherwise try.
func (s *Service) assertSlotInRound(ctx context.Context, tenantID, roundID, slotID uuid.UUID) error {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM app_trivia_slots WHERE tenant_id = $1 AND round_id = $2 AND id = $3)`,
		tenantID, roundID, slotID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking slot: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func teamByID(ctx context.Context, q Querier, tenantID, gameID, teamID uuid.UUID) (*Team, error) {
	teams, err := ListTeams(ctx, q, tenantID, gameID)
	if err != nil {
		return nil, err
	}
	for i := range teams {
		if teams[i].ID == teamID {
			return &teams[i], nil
		}
	}
	return nil, ErrNotFound
}

// IssueReclaim replaces a team's identity with one derived from a four-digit
// code the host reads out, and returns nothing but that. The phone posts the
// code back and gets a cookie.
//
// The code is short because it is spoken across a bar, and short is safe here
// only because the trust boundary is a person standing in the room who can
// see who is asking -- not because four digits are hard to guess.
func (s *Service) IssueReclaim(ctx context.Context, tenantID, gameID, teamID uuid.UUID, code string) error {
	return SetTeamToken(ctx, s.pool, tenantID, gameID, teamID, HashToken(reclaimSecret(gameID, teamID, code)))
}

// RedeemReclaim exchanges a host-issued code for a fresh identity token.
func (s *Service) RedeemReclaim(ctx context.Context, tenantID, gameID, teamID uuid.UUID, code string) (string, error) {
	team, err := FindTeamByToken(ctx, s.pool, tenantID, gameID, teamID,
		HashToken(reclaimSecret(gameID, teamID, code)))
	if err != nil {
		return "", err
	}
	// Burn the code on use: a four-digit secret read out loud must not stay
	// live for the rest of the night.
	token := NewTeamToken()
	if err := SetTeamToken(ctx, s.pool, tenantID, gameID, team.ID, HashToken(token)); err != nil {
		return "", err
	}
	return token, nil
}

// reclaimSecret binds a spoken code to one team in one game, so the same four
// digits issued for another table are not interchangeable.
func reclaimSecret(gameID, teamID uuid.UUID, code string) string {
	return "reclaim:" + gameID.String() + ":" + teamID.String() + ":" + code
}
