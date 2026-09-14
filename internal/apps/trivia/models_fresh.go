package trivia

import "github.com/google/uuid"

// Freshness is the "don't ask it twice" rule, in the shape every bank query
// needs it.
//
// A question is STALE once some round has asked it -- a round row, not a
// board placement, because a cell the host never opened was never asked and
// the room would rightly say so. Rounds cascade with their game, so deleting
// a night gives its questions back and there is no reset to remember.
//
// The match is on prompt_key rather than question id: the same question
// uploaded in two packs is two rows saying one thing, and asking it again
// because it came from the other pack is exactly the bug this prevents.
type Freshness struct {
	// GameID is the game being drawn for. Its own rounds never count as
	// asked -- a board rebuilt mid-night must not refuse to re-place the
	// questions this very game already used. The zero value means "every
	// round in the workspace counts", which is what a workspace-wide count
	// (the question-bank page) wants.
	GameID uuid.UUID
	// AllowRepeats is the game's repeat_questions setting. On, every query
	// below collapses back to the behaviour the app shipped with: the whole
	// bank is available and last_used_at alone decides the order.
	AllowRepeats bool
}

// freshnessOf reads the rule off a game, which is where it is configured.
func freshnessOf(g *Game) Freshness {
	return Freshness{GameID: g.ID, AllowRepeats: g.RepeatQuestions}
}

// freshSQL is the predicate "this bank row has not been asked", for a query
// that has the questions table under the alias `q`.
//
// repeatParam and gameParam are placeholder spellings ("$4", "$2") rather
// than values, because each caller numbers its own parameters. Passing the
// repeat flag as a parameter rather than branching on the Go side keeps one
// query text per function, so the plan cache sees one statement and a reader
// sees the whole rule in one place.
//
// game_id <> a nil uuid is true for every row, so a zero GameID needs no
// special case: nothing is excluded and the count is workspace-wide.
func freshSQL(repeatParam, gameParam string) string {
	return `(` + repeatParam + `::bool OR NOT EXISTS (
		    SELECT 1 FROM app_trivia_rounds r
		     WHERE r.tenant_id = q.tenant_id
		       AND r.prompt_key = q.prompt_key
		       AND r.game_id <> ` + gameParam + `::uuid))`
}
