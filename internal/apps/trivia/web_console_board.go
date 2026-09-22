package trivia

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
)

// The host's own view of the board: what each tile will actually ask, and the
// one button that changes it.
//
// This is deliberately NOT on the wire frames. HostFrame goes out over SSE to
// the console many times a night and shares its cell type with the TV and the
// phones; hanging the prompts off that would put every question of the night
// into a projection whose whole job is withholding. The setup page asks for
// them once, by request, on the page where the host is building the thing.

// cellJSON is one tile plus the question behind it.
type cellJSON struct {
	ID     string `json:"id"`
	Col    int    `json:"col"`
	Row    int    `json:"row"`
	Topic  string `json:"topic"`
	Points int    `json:"points"`
	Played bool   `json:"played"`
	Prompt string `json:"prompt"`
	// Answer is the correct answer as the sheet spelled it, so the host can
	// see that a cell is going to ask something unanswerable BEFORE the room
	// does. They read it out at reveal anyway; there is nothing to protect
	// here that the live page does not already show.
	Answer string `json:"answer"`
	// Spares is how many other questions this column could swap in. Zero
	// means the button would fail, so the console greys it out and says why
	// rather than offering it.
	Spares int `json:"spares"`
}

// boardCells assembles the setup page's board: the cells, their questions and
// what each column has left to swap in. Three queries for the whole grid, not
// three per tile.
func (a *App) boardCells(r *http.Request, tenantID uuid.UUID, game *Game) ([]cellJSON, error) {
	cells, err := ListBoardCells(r.Context(), a.pool, tenantID, game.ID)
	if err != nil {
		return nil, err
	}
	if len(cells) == 0 {
		return []cellJSON{}, nil
	}
	ids := make([]uuid.UUID, 0, len(cells))
	keys := make([]string, 0, len(cells))
	seenKey := map[string]bool{}
	for _, c := range cells {
		ids = append(ids, c.QuestionID)
		if k := cellTopicKey(c); !seenKey[k] {
			seenKey[k] = true
			keys = append(keys, k)
		}
	}
	questions, err := QuestionsByID(r.Context(), a.pool, tenantID, ids)
	if err != nil {
		return nil, err
	}
	datasetIDs, err := GameDatasetIDs(r.Context(), a.pool, tenantID, game.ID)
	if err != nil {
		return nil, err
	}
	spares, err := SwapSpares(r.Context(), a.pool, tenantID, game.ID, keys, datasetIDs, freshnessOf(game))
	if err != nil {
		return nil, err
	}
	out := make([]cellJSON, 0, len(cells))
	for _, c := range cells {
		q := questions[c.QuestionID]
		out = append(out, cellJSON{
			ID: c.ID.String(), Col: c.ColIndex, Row: c.RowIndex,
			Topic: c.Topic, Points: c.Points, Played: c.PlayedAt != nil,
			Prompt: q.Prompt, Answer: q.AnswerText,
			Spares: spares[cellTopicKey(c)],
		})
	}
	return out, nil
}

// cellTopicKey recovers a cell's topic key from the display spelling stored
// on it. Folding is how the key was made in the first place -- see
// parseTopics -- and is idempotent, so this round-trips.
//
// The cell stores the LABEL because that is what the TV prints, and a
// question carrying two topics cannot be asked which column it is in, so the
// key cannot be read back off the question either.
func cellTopicKey(c BoardCell) string { return FoldKey(c.Topic) }

// handleSwapCell rotates one tile's question out for another in the same
// column.
func (a *App) handleSwapCell(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	cell, ok := a.swappableCell(w, r, game)
	if !ok {
		return
	}
	next, ok := a.nextQuestionFor(w, r, game, cell)
	if !ok {
		return
	}
	if err := SwapCellQuestion(r.Context(), a.pool, tenant.ID, game.ID, cell.ID, next.ID); err != nil {
		if errors.Is(err, ErrNotFound) {
			// The cell was played between the check above and the write: the
			// host's tab has been open a while and the room moved on.
			clientError(w, r, http.StatusConflict, "that cell has already been played")
			return
		}
		serverError(w, "swapping a trivia board question", err)
		return
	}
	cells, err := a.boardCells(r, tenant.ID, game)
	if err != nil {
		serverError(w, "reloading the trivia board", err)
		return
	}
	// The public shape of the board did not move -- same column, same points
	// -- but the state version did, so the surfaces are told rather than left
	// a version behind their own watchdog.
	if snap, err := a.svc.Snapshot(r.Context(), tenant.ID, game.ID); err == nil {
		a.svc.Broker().Publish(game.ID, snap)
	}
	writeJSON(w, map[string]any{"cells": cells})
}

// swappableCell resolves the tile a swap names and refuses the ones that are
// not the host's to change.
//
// Gated to before the game starts, like building the board is: once the room
// is playing, the picker has been drawn and the cells are being chosen off a
// screen, and rewriting what is behind one of them is not an edit anybody
// asked for.
func (a *App) swappableCell(w http.ResponseWriter, r *http.Request, game *Game) (*BoardCell, bool) {
	tenant := auth.TenantFromContext(r.Context())
	if game.Phase != PhaseSetup && game.Phase != PhaseLobby {
		clientError(w, r, http.StatusConflict, "questions can only be swapped before the game starts")
		return nil, false
	}
	cellID, err := uuid.Parse(r.PathValue("cellID"))
	if err != nil {
		clientError(w, r, http.StatusNotFound, "not a cell id")
		return nil, false
	}
	cell, err := GetBoardCell(r.Context(), a.pool, tenant.ID, game.ID, cellID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			clientError(w, r, http.StatusNotFound, "no such cell on this board")
			return nil, false
		}
		serverError(w, "loading a trivia board cell", err)
		return nil, false
	}
	if cell.PlayedAt != nil {
		clientError(w, r, http.StatusConflict, "that cell has already been played")
		return nil, false
	}
	return cell, true
}

// nextQuestionFor picks the replacement, or writes the refusal.
//
// A spent category is the interesting failure and the host is standing in a
// bar at 7pm, so it names the column and which kind of shortfall it is --
// "no other question" and "no other FRESH question" have different fixes,
// exactly as a shortfall on the build does.
func (a *App) nextQuestionFor(w http.ResponseWriter, r *http.Request, game *Game, cell *BoardCell) (*Question, bool) {
	tenant := auth.TenantFromContext(r.Context())
	datasetIDs, err := GameDatasetIDs(r.Context(), a.pool, tenant.ID, game.ID)
	if err != nil {
		serverError(w, "loading game datasets", err)
		return nil, false
	}
	next, err := NextSwapQuestion(r.Context(), a.pool, tenant.ID, game.ID,
		cellTopicKey(*cell), datasetIDs, freshnessOf(game))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			kind := "question"
			if !game.RepeatQuestions {
				kind = "fresh question"
			}
			clientError(w, r, http.StatusUnprocessableEntity,
				"no other "+kind+" in "+cell.Topic+" that this board isn't already using")
			return nil, false
		}
		serverError(w, "finding a trivia swap candidate", err)
		return nil, false
	}
	return next, true
}
