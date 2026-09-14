package trivia

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Who picks the next category.
//
// The host used to pick the first one, which the room reads as the quiz
// picking its own favourite — and after that the console derived "X picks
// next" from whoever wrote the winning card. That derivation has no answer
// for the three cases a bar produces every single night: two tables wrote the
// same winning number, nobody wrote it at all, and the first question, where
// there is no previous round.
//
// So the rule lives here, once, as a pure function over candidates, and the
// answer is stored on the game row. The TV, the phones and the console all
// read the same field; none of them re-derives it, and none of them can
// disagree about it.

// PickerReason says WHY a table holds the pick, so each surface can phrase it
// without knowing the rule. Stored on the game row alongside the team id.
type PickerReason string

// The three ways a table comes to hold the pick.
const (
	// PickerDrawn is the wheel at the start of the night.
	PickerDrawn PickerReason = "drawn"
	// PickerWroteWinner is the ordinary case: you wrote the winning card.
	PickerWroteWinner PickerReason = "wrote_winner"
	// PickerLowest is the consolation: nobody wrote the winner, or several
	// tables did and this is the one furthest behind.
	PickerLowest PickerReason = "lowest"
)

// PickerCandidate is one table as the rule sees it. Deliberately not *Team:
// the rule needs the score AFTER the round being scored, which no row in the
// database holds at the moment it has to be applied.
type PickerCandidate struct {
	TeamID uuid.UUID
	// Score is this table's total once the round in hand is counted.
	Score int
	// JoinedAt breaks a tie on score. Earliest joined wins, because it is
	// the one ordering of the room that everybody in it already agrees on.
	JoinedAt time.Time
	// EligibleFromOrdinal is the first question this table may play. A table
	// that joined mid-round is fine — it is eligible from the next ordinal,
	// which is exactly the question it would be picking.
	EligibleFromOrdinal int
	// WroteWinner is whether this table's answer became the winning card.
	WroteWinner bool
}

// ChoosePicker applies the rule for the question numbered nextOrdinal.
//
//  1. Among the tables that wrote the winning card, the one with the LOWEST
//     score after this round. Sharing the card is common — BuildSlots dedupes
//     equal answers onto one — and handing the pick to the table already in
//     front compounds a lead with a choice; handing it to the one behind is
//     the small correction the room reads as fair.
//  2. If nobody wrote it (the pseudo-slot took the round), the lowest-scoring
//     table in the room.
//  3. Ties on score go to whoever joined first.
//
// A table that will not be eligible for nextOrdinal cannot pick it, and ok is
// false for an empty room — a game with nobody in it has no picker, and that
// is a state the surfaces render rather than an error.
func ChoosePicker(cands []PickerCandidate, nextOrdinal int) (uuid.UUID, PickerReason, bool) {
	eligible := make([]PickerCandidate, 0, len(cands))
	for _, c := range cands {
		if c.EligibleFromOrdinal <= nextOrdinal {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		return uuid.Nil, "", false
	}

	winners := make([]PickerCandidate, 0, len(eligible))
	for _, c := range eligible {
		if c.WroteWinner {
			winners = append(winners, c)
		}
	}
	if len(winners) == 1 {
		return winners[0].TeamID, PickerWroteWinner, true
	}
	if len(winners) > 1 {
		// Shared the card: the one furthest behind takes the pick. Still
		// "wrote_winner" — the reason the room cares about is that their
		// answer won, and the tie-break is the fine print.
		return lowest(winners).TeamID, PickerWroteWinner, true
	}
	return lowest(eligible).TeamID, PickerLowest, true
}

// lowest is the tie-break, written once: lowest score, then earliest joined,
// then the id, so the answer is total and the same on every replay.
func lowest(cands []PickerCandidate) PickerCandidate {
	best := cands[0]
	for _, c := range cands[1:] {
		switch {
		case c.Score != best.Score:
			if c.Score < best.Score {
				best = c
			}
		case !c.JoinedAt.Equal(best.JoinedAt):
			if c.JoinedAt.Before(best.JoinedAt) {
				best = c
			}
		default:
			if c.TeamID.String() < best.TeamID.String() {
				best = c
			}
		}
	}
	return best
}

// DrawPicker picks the first category's picker out of the hat.
//
// Random rather than "the host picks", because the host picking the first
// category is the one moment of the night where the quiz looks like it is
// playing itself — and because a draw is something the room can WATCH. The
// server decides; the wheel on the TV is an animation of a decision already
// made, which is the only way the wheel and the board can agree.
//
// math/rand/v2 rather than crypto/rand: nothing is at stake here that a
// determined table could profit from predicting, and the failure mode of the
// crypto reader (an error to handle at the exact moment a game starts) is
// worse than the one it protects against.
func DrawPicker(teams []Team) (uuid.UUID, bool) {
	if len(teams) == 0 {
		return uuid.Nil, false
	}
	return teams[rand.IntN(len(teams))].ID, true //nolint:gosec // see above
}

// Execer is the write half of Querier: the pool and a transaction both
// satisfy it, so SetPicker can be called from the start action (pool) and
// from inside the scoring transaction (tx) without two spellings.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// SetPicker writes the picker onto the game row. It does NOT bump the state
// version: every caller is already inside a transaction that bumps it for its
// own reasons, and a second bump would put a duplicate frame on every stream.
func SetPicker(ctx context.Context, db Execer, tenantID, gameID uuid.UUID, teamID *uuid.UUID, reason PickerReason) error {
	_, err := db.Exec(ctx, `
		UPDATE app_trivia_games SET picker_team_id = $3, picker_reason = $4
		 WHERE tenant_id = $1 AND id = $2`,
		tenantID, gameID, teamID, string(reason))
	if err != nil {
		return fmt.Errorf("setting trivia picker: %w", err)
	}
	return nil
}

// pickerAfterRound applies the rule inside the transaction that scored the
// round, which is the only place the post-round scores exist.
//
// banks is each team's total going IN to this round, deltas what the round
// did to them, and winnerIDs the teams whose answer became the winning card
// (empty when the pseudo-slot took it).
func (s *Service) pickerAfterRound(ctx context.Context, tx pgx.Tx, game *Game, nextOrdinal int,
	banks map[uuid.UUID]int, deltas map[uuid.UUID]ScoreDelta, winnerIDs []uuid.UUID) error {
	teams, err := ListTeams(ctx, tx, game.TenantID, game.ID)
	if err != nil {
		return err
	}
	wrote := map[uuid.UUID]bool{}
	for _, id := range winnerIDs {
		wrote[id] = true
	}
	cands := make([]PickerCandidate, 0, len(teams))
	for _, t := range teams {
		cands = append(cands, PickerCandidate{
			TeamID:              t.ID,
			Score:               banks[t.ID] + deltas[t.ID].Total(),
			JoinedAt:            t.JoinedAt,
			EligibleFromOrdinal: t.EligibleFromOrdinal,
			WroteWinner:         wrote[t.ID],
		})
	}
	teamID, reason, ok := ChoosePicker(cands, nextOrdinal)
	if !ok {
		// Nobody eligible. Clear it rather than leaving a stale name on the
		// wall for a table that is no longer in the game.
		return SetPicker(ctx, tx, game.TenantID, game.ID, nil, "")
	}
	return SetPicker(ctx, tx, game.TenantID, game.ID, &teamID, reason)
}
