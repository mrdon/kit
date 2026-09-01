package trivia

import "github.com/google/uuid"

// This file and slots.go are the rules of the game, and they are PURE: no
// database, no context, no SQL. That is what makes them testable in
// isolation, and it is why the one path in the whole app where a score can
// fall lives in a single clearly-named branch of one function rather than
// spread across the service layer.

// RoundBet is one chip as the engine sees it.
type RoundBet struct {
	TeamID   uuid.UUID
	Amount   int
	SlotPos  int
	TokenIdx int
}

// RoundInput is everything ScoreRound needs.
type RoundInput struct {
	// Correct is the question's true answer.
	Correct float64
	// CellPoints is what the board cell was worth. Zero in a final, where
	// board points come from the final round's own points value.
	CellPoints int
	Slots      []Slot
	Bets       []RoundBet
	// IsFinal switches the one branch where a wrong bet costs money.
	IsFinal bool
	// Banks is each team's score going in, used only in a final to clamp a
	// loss at zero. A team can reach exactly $0 and never go below.
	Banks map[uuid.UUID]int
}

// RoundResult is what the round did to the standings.
type RoundResult struct {
	// WinningPos is the position of the slot that took the round. Always a
	// valid position -- the pseudo-slot at 0 exists precisely so this is
	// never "nobody".
	WinningPos int
	// Deltas is per team. Absent means the team neither wrote the winner nor
	// had a chip on it, which costs nothing outside a final.
	Deltas map[uuid.UUID]ScoreDelta
}

// ScoreRound applies the six rules.
//
//  1. Closest without going over: the winner is the slot with the largest
//     value at or below the correct answer.
//  2. If no guess is at or below it, the "Smaller than all of these"
//     pseudo-slot wins.
//  3. The team(s) who wrote the winning answer take the board cell's value.
//     Ties take FULL value each -- splitting it would punish agreement, and
//     the dedupe in BuildSlots means agreement is common.
//  4. Each team's chips pay their face value on the winning slot.
//  5. A chip anywhere else pays nothing and costs nothing. During the board,
//     scores only ever go up.
//  6. In a final, and only in a final, a chip on the wrong answer costs its
//     amount.
func ScoreRound(in RoundInput) RoundResult {
	res := RoundResult{Deltas: map[uuid.UUID]ScoreDelta{}}
	winning := winningSlot(in.Slots, in.Correct)
	res.WinningPos = winning.Position

	// Board points to everyone who wrote the winning answer. When the
	// pseudo-slot wins nobody wrote it, so nobody takes the cell -- its team
	// list is empty and this loop does nothing.
	for _, teamID := range winning.TeamIDs {
		d := res.Deltas[teamID]
		d.BoardPoints += in.CellPoints
		res.Deltas[teamID] = d
	}

	for _, b := range in.Bets {
		d := res.Deltas[b.TeamID]
		switch {
		case b.SlotPos == winning.Position:
			// Amount x Odds, with Odds always 1 in v1. The multiply stays so
			// an optional Casino mode is a data change rather than a code
			// change; no balance is priced into it.
			d.BetDelta += b.Amount * slotOdds(in.Slots, b.SlotPos)
		case in.IsFinal:
			// The only path in the app where a score decreases.
			d.BetDelta -= b.Amount
		}
		res.Deltas[b.TeamID] = d
	}

	if in.IsFinal {
		clampToBank(res.Deltas, in.Banks)
	}
	return res
}

// winningSlot is rules 1 and 2. Slots are ascending with the pseudo-slot at
// position 0, so scanning forward and keeping the last slot at or below the
// answer lands on the largest one -- and falls back to the pseudo-slot when
// every guess overshot.
func winningSlot(slots []Slot, correct float64) Slot {
	winner := Slot{Position: 0, Label: PseudoSlotLabel, Odds: 1}
	for _, s := range slots {
		if s.IsPseudo() {
			winner = s
			continue
		}
		if *s.Value <= correct {
			winner = s
		}
	}
	return winner
}

func slotOdds(slots []Slot, pos int) int {
	for _, s := range slots {
		if s.Position == pos {
			if s.Odds < 1 {
				return 1
			}
			return s.Odds
		}
	}
	return 1
}

// clampToBank keeps a losing final wager from taking a team below zero.
//
// The stake was already clamped to the team's bank when it was locked, so
// this is belt and braces rather than the primary guard -- but "a team
// finished on minus four hundred dollars" is the kind of thing a room
// notices, and the cost of being sure is four lines.
func clampToBank(deltas map[uuid.UUID]ScoreDelta, banks map[uuid.UUID]int) {
	for teamID, d := range deltas {
		if total := banks[teamID] + d.Total(); total < 0 {
			d.BetDelta -= total // add back exactly the overshoot
			deltas[teamID] = d
		}
	}
}
