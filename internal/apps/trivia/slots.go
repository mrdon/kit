package trivia

import (
	"sort"

	"github.com/google/uuid"
)

// PseudoSlotLabel is the card that stands for "the correct answer is below
// every guess in the room". It always exists, at position 0, which is what
// keeps the winner from ever being nothing.
const PseudoSlotLabel = "Smaller than all of these"

// TeamAnswer is one team's guess, as the pure layer sees it. No database, no
// context -- that is what makes the rules testable in isolation.
type TeamAnswer struct {
	TeamID uuid.UUID
	Value  float64
	Raw    string
}

// Slot is a revealed card: a distinct value and every team that wrote it.
type Slot struct {
	Position int
	// Value is nil on the pseudo-slot at position 0.
	Value   *float64
	Label   string
	TeamIDs []uuid.UUID
	Odds    int
}

// IsPseudo reports the "smaller than all of these" card.
func (s Slot) IsPseudo() bool { return s.Value == nil }

// BuildSlots turns the room's answers into the cards it will bet on:
// deduplicated on value, sorted ascending, with the pseudo-slot prepended.
//
// Sorting is not decoration -- it is what makes the betting spatial, so a
// table scanning the TV can reason about "somewhere in the middle" rather
// than reading twenty numbers.
//
// The dedupe is what removes tie-breaking from the game entirely. Two tables
// that both write 1969 share one card and both appear in its team list, so
// "who wins a tie" is never a case the scoring engine has to reason about,
// and identical guesses collapsing means a popular round number spreads board
// points across several tables at twenty teams.
func BuildSlots(answers []TeamAnswer) []Slot {
	byValue := map[float64][]uuid.UUID{}
	labelFor := map[float64]string{}
	var values []float64
	for _, a := range answers {
		if _, seen := byValue[a.Value]; !seen {
			values = append(values, a.Value)
			labelFor[a.Value] = a.Raw
		}
		byValue[a.Value] = append(byValue[a.Value], a.TeamID)
	}
	sort.Float64s(values)

	slots := []Slot{{Position: 0, Label: PseudoSlotLabel, Odds: 1}}
	for i, v := range values {
		value := v
		label := labelFor[v]
		if label == "" {
			label = FormatValue(v)
		}
		slots = append(slots, Slot{
			Position: i + 1,
			Value:    &value,
			Label:    label,
			TeamIDs:  byValue[v],
			Odds:     1,
		})
	}
	return slots
}
