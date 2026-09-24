package trivia

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Loading the night for the award pool.
//
// Four game-wide reads rather than the per-round loaders, because the pool
// wants every round at once and running ListAnswers twelve times to build
// one screen is twelve round trips for a screen that could have had four.
//
// Nothing here is materialised. The podium is terminal and its inputs are
// frozen, so recomputing is free of the hazard WriteRoundScores exists to
// avoid: there is no later round these numbers could restate. If awards ever
// reach a recap or an export, that is the moment to give them a table.

// AwardRows assembles everything the award pool reads.
func AwardRows(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) (AwardInput, error) {
	var in AwardInput
	teams, err := ListTeams(ctx, q, tenantID, gameID)
	if err != nil {
		return in, err
	}
	for _, t := range teams {
		in.Teams = append(in.Teams, AwardTeam{
			ID: t.ID, Name: t.Name, EligibleFrom: t.EligibleFromOrdinal,
		})
	}
	rounds, err := ListRounds(ctx, q, tenantID, gameID)
	if err != nil {
		return in, err
	}
	answers, err := awardAnswers(ctx, q, tenantID, gameID)
	if err != nil {
		return in, err
	}
	slots, err := awardSlots(ctx, q, tenantID, gameID)
	if err != nil {
		return in, err
	}
	bets, err := awardBets(ctx, q, tenantID, gameID)
	if err != nil {
		return in, err
	}
	scores, err := ScoredRoundScores(ctx, q, tenantID, gameID)
	if err != nil {
		return in, err
	}
	deltas := map[uuid.UUID]map[uuid.UUID]ScoreDelta{}
	for _, s := range scores {
		if deltas[s.RoundID] == nil {
			deltas[s.RoundID] = map[uuid.UUID]ScoreDelta{}
		}
		deltas[s.RoundID][s.TeamID] = ScoreDelta{BoardPoints: s.BoardPoints, BetDelta: s.BetDelta}
	}

	for _, r := range rounds {
		// Only scored rounds. A round abandoned mid-question has no winning
		// card and no deltas, and every award over it would be counting a
		// question the room never finished.
		if r.ScoredAt == nil || r.WinningSlotID == nil {
			continue
		}
		in.Rounds = append(in.Rounds, AwardRound{
			Ordinal: r.Ordinal, IsFinal: r.IsFinal,
			Correct: r.AnswerValue, CorrectText: r.AnswerText,
			WinningSlotID: *r.WinningSlotID,
			Answers:       answers[r.ID], Bets: bets[r.ID], Slots: slots[r.ID],
			Deltas: deltas[r.ID],
		})
	}
	return in, nil
}

// awardAnswers reads every answer of the night, ordered so the first-in and
// last-in awards can read the race straight off the slice.
func awardAnswers(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) (map[uuid.UUID][]AwardAnswer, error) {
	rows, err := q.Query(ctx, `
		SELECT a.round_id, a.team_id, a.value, a.submitted_at
		  FROM app_trivia_answers a
		  JOIN app_trivia_rounds r ON r.id = a.round_id AND r.tenant_id = a.tenant_id
		 WHERE a.tenant_id = $1 AND r.game_id = $2
		 ORDER BY a.submitted_at, a.team_id`, tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing answers for awards: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]AwardAnswer{}
	for rows.Next() {
		var roundID uuid.UUID
		var a AwardAnswer
		if err := rows.Scan(&roundID, &a.TeamID, &a.Value, &a.SubmittedAt); err != nil {
			return nil, fmt.Errorf("scanning answer for awards: %w", err)
		}
		out[roundID] = append(out[roundID], a)
	}
	return out, rows.Err()
}

// awardSlots reads every card of the night with the tables that wrote it.
func awardSlots(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) (map[uuid.UUID][]AwardSlot, error) {
	rows, err := q.Query(ctx, `
		SELECT s.round_id, s.id, s.value,
		       COALESCE(array_agg(st.team_id) FILTER (WHERE st.team_id IS NOT NULL), '{}')
		  FROM app_trivia_slots s
		  JOIN app_trivia_rounds r ON r.id = s.round_id AND r.tenant_id = s.tenant_id
		  LEFT JOIN app_trivia_slot_teams st
		         ON st.slot_id = s.id AND st.tenant_id = s.tenant_id
		 WHERE s.tenant_id = $1 AND r.game_id = $2
		 GROUP BY s.round_id, s.id, s.value, s.position
		 ORDER BY s.round_id, s.position`, tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing slots for awards: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]AwardSlot{}
	for rows.Next() {
		var roundID uuid.UUID
		var s AwardSlot
		if err := rows.Scan(&roundID, &s.ID, &s.Value, &s.TeamIDs); err != nil {
			return nil, fmt.Errorf("scanning slot for awards: %w", err)
		}
		out[roundID] = append(out[roundID], s)
	}
	return out, rows.Err()
}

// awardBets reads every chip of the night.
func awardBets(ctx context.Context, q Querier, tenantID, gameID uuid.UUID) (map[uuid.UUID][]AwardBet, error) {
	rows, err := q.Query(ctx, `
		SELECT b.round_id, b.team_id, b.slot_id, b.amount
		  FROM app_trivia_bets b
		  JOIN app_trivia_rounds r ON r.id = b.round_id AND r.tenant_id = b.tenant_id
		 WHERE b.tenant_id = $1 AND r.game_id = $2
		 ORDER BY b.round_id, b.team_id, b.token_index`, tenantID, gameID)
	if err != nil {
		return nil, fmt.Errorf("listing bets for awards: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]AwardBet{}
	for rows.Next() {
		var roundID uuid.UUID
		var b AwardBet
		if err := rows.Scan(&roundID, &b.TeamID, &b.SlotID, &b.Amount); err != nil {
			return nil, fmt.Errorf("scanning bet for awards: %w", err)
		}
		out[roundID] = append(out[roundID], b)
	}
	return out, rows.Err()
}
