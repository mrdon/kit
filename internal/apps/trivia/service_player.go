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
	if game.Phase == PhasePodium {
		return nil, "", fmt.Errorf("%w: this game has finished", ErrClosed)
	}
	n, err := CountTeams(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, "", err
	}
	if n >= MaxTeams {
		return nil, "", ErrGameFull
	}

	// A team joining mid-question is not in that question's denominator, so
	// it becomes eligible from the NEXT round. Without this the TV's
	// "12 of 20 answered" ticks backwards as latecomers arrive.
	eligibleFrom := 1
	if game.CurrentRoundID != nil {
		round, err := GetRound(ctx, s.pool, tenantID, *game.CurrentRoundID)
		if err != nil {
			return nil, "", err
		}
		eligibleFrom = round.Ordinal + 1
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

// SubmitAnswer records a team's number, and in a final its stake too.
//
// Resubmitting until the deadline is allowed on purpose, and the phone says
// so: on a sixty-second clock, fat-finger anxiety costs more than a late
// edit does.
func (s *Service) SubmitAnswer(ctx context.Context, tenantID, gameID, teamID uuid.UUID, raw string, stake *int) error {
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
	team, err := teamByID(ctx, s.pool, tenantID, gameID, teamID)
	if err != nil {
		return err
	}
	if team.EligibleFromOrdinal > round.Ordinal {
		// A team that arrived mid-round watches this one out. Letting it
		// answer would put it in a denominator it was excluded from.
		return fmt.Errorf("%w: you joined during this question — you're in from the next one", ErrClosed)
	}

	clamped, err := s.clampStake(ctx, game, round, teamID, stake)
	if err != nil {
		return err
	}
	if err := UpsertAnswer(ctx, s.pool, tenantID, round.ID, teamID, value, strings.TrimSpace(raw), clamped); err != nil {
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

// clampStake bounds a final's wager to the team's own bank, SERVER-SIDE. The
// phone mirrors the clamp so the slider cannot express an impossible bet, but
// a hand-edited request has to be clamped rather than rejected: rejecting
// would let a team lose its final to a typo.
//
// A stake outside a final is ignored rather than an error -- there is nothing
// to stake during the board, and a phone that sends one is stale, not
// malicious.
func (s *Service) clampStake(ctx context.Context, game *Game, round *Round, teamID uuid.UUID, stake *int) (*int, error) {
	if !round.IsFinal || stake == nil || !game.FinalWager {
		return nil, nil //nolint:nilnil // "no stake" is the normal outside-a-final result, not an error
	}
	standings, err := Leaderboard(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return nil, err
	}
	bank := 0
	for _, st := range standings {
		if st.TeamID == teamID {
			bank = st.Total
		}
	}
	v := min(max(*stake, 0), bank)
	return &v, nil
}

// PlaceChip puts one token on one card, or lifts it off with a nil slot.
//
// A PUT of the desired placement rather than an append, so every retry over
// flaky bar wifi is idempotent. The two-different-answers rule is the unique
// index on (round, team, slot_id) -- enforced by the database rather than a
// handler check that two racing taps could both pass.
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
				return fmt.Errorf("%w: your two chips must go on different answers", ErrBadRequest)
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
// number. In a final it is the stake the team locked WITH ITS ANSWER, before
// it saw anybody else's, which is what makes the final a wager rather than a
// calculation.
func (s *Service) chipAmount(ctx context.Context, game *Game, round *Round, teamID uuid.UUID, tokenIndex, _ int) (int, error) {
	if round.IsFinal {
		answers, err := ListAnswers(ctx, s.pool, game.TenantID, round.ID)
		if err != nil {
			return 0, err
		}
		for _, a := range answers {
			if a.TeamID == teamID && a.Stake != nil {
				return *a.Stake, nil
			}
		}
		// A team that never answered the final never locked a stake, so it
		// has nothing to place. $0 is a legal bet, so this is a no-op rather
		// than an error.
		return 0, nil
	}
	if tokenIndex < 0 || tokenIndex >= len(game.TokenValues) {
		return 0, fmt.Errorf("%w: no such chip", ErrBadRequest)
	}
	return game.TokenValues[tokenIndex], nil
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
