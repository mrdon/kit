package trivia

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
)

// importMaxBytes caps an upload. A question sheet is text; two megabytes is
// tens of thousands of rows and anything larger is a mistake, not a bank.
const importMaxBytes = 2 << 20

// importResponse is what the host sees after an upload.
//
// The TOPIC HISTOGRAM comes back from this same call, deliberately. It is
// what the setup page needs to offer column choices, and a host who uploads a
// sheet and is told only "38 imported" has no idea what they got or whether
// any column can fill a board.
type importResponse struct {
	Imported          int          `json:"imported"`
	Updated           int          `json:"updated"`
	SkippedDuplicates int          `json:"skipped_duplicates"`
	Errors            []RowError   `json:"errors"`
	Truncated         bool         `json:"truncated"`
	Topics            []TopicCount `json:"topics"`
}

func (a *App) handleImport(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, importMaxBytes)
	if err := r.ParseMultipartForm(importMaxBytes); err != nil {
		http.Error(w, "the file is too large or not a valid upload", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("csv")
	if err != nil {
		http.Error(w, "no csv file in the upload", http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	plan, err := ParseCSV(file)
	if err != nil {
		// A header the parser cannot read is the host's to fix, and the
		// message names what it found against what it wanted.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := a.importPlan(r, tenant.ID, plan)
	if err != nil {
		serverError(w, "importing trivia questions", err)
		return
	}
	writeJSON(w, resp)
}

// importPlan writes the good rows and reports the bad. A three-hundred-row
// sheet with two typos is not a total failure.
func (a *App) importPlan(r *http.Request, tenantID uuid.UUID, plan ImportPlan) (importResponse, error) {
	resp := importResponse{
		Errors:            plan.Errors,
		Truncated:         plan.Truncated,
		SkippedDuplicates: plan.Skipped,
	}
	if resp.Errors == nil {
		resp.Errors = []RowError{}
	}

	tx, err := a.pool.Begin(r.Context())
	if err != nil {
		return resp, fmt.Errorf("beginning import: %w", err)
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	for _, q := range plan.Rows {
		_, inserted, err := UpsertQuestion(r.Context(), tx, tenantID, q)
		if err != nil {
			return resp, err
		}
		// Re-uploading a corrected sheet updates in place rather than
		// duplicating, and the host is told which happened.
		if inserted {
			resp.Imported++
		} else {
			resp.Updated++
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return resp, fmt.Errorf("committing import: %w", err)
	}

	hist, err := TopicHistogram(r.Context(), a.pool, tenantID)
	if err != nil {
		return resp, err
	}
	resp.Topics = hist
	if resp.Topics == nil {
		resp.Topics = []TopicCount{}
	}
	return resp, nil
}

// handleListQuestions serves the bank plus its histogram, so the setup page
// can show what a column would be drawn from.
func (a *App) handleListQuestions(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	hist, err := TopicHistogram(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "loading topic histogram", err)
		return
	}
	if hist == nil {
		hist = []TopicCount{}
	}
	total, err := CountQuestions(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "counting trivia questions", err)
		return
	}
	writeJSON(w, map[string]any{"total": total, "topics": hist})
}

func (a *App) handleDeleteQuestion(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := DeleteQuestion(r.Context(), a.pool, tenant.ID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		// Board cells reference questions ON DELETE RESTRICT, so a question
		// that is on some game's board cannot be pulled out from under it.
		http.Error(w, "that question is on a game's board and cannot be deleted", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// buildBoardRequest picks the columns. Topics may be empty, which is the Auto
// button: the server picks among the viable ones.
type buildBoardRequest struct {
	Topics []string `json:"topics"`
	Auto   bool     `json:"auto"`
}

// handleBuildBoard materialises a game's grid.
//
// The column set is a HOST DECISION, defaulted rather than imposed, because
// "Sports" and "Sportsball" arriving from a CSV as two topics is a real thing
// and the host has to see and fix it. Auto rerolls among the viable ones.
func (a *App) handleBuildBoard(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	if game.Phase != PhaseSetup && game.Phase != PhaseLobby {
		http.Error(w, "the board can only be built before the game starts", http.StatusConflict)
		return
	}
	var req buildBoardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	topics, err := a.resolveTopics(r, tenant.ID, game, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cells, err := a.assignBoard(r, tenant.ID, game, topics)
	if err != nil {
		var se *ShortfallError
		if errors.As(err, &se) {
			// The host is in a bar at 7pm. Name the column and the gap.
			http.Error(w, se.Error(), http.StatusUnprocessableEntity)
			return
		}
		serverError(w, "building trivia board", err)
		return
	}
	if err := ReplaceBoard(r.Context(), a.pool, tenant.ID, game.ID, cells); err != nil {
		serverError(w, "writing trivia board", err)
		return
	}
	snap, err := a.svc.Snapshot(r.Context(), tenant.ID, game.ID)
	if err != nil {
		serverError(w, "loading trivia game", err)
		return
	}
	a.svc.Broker().Publish(game.ID, snap)
	writeJSON(w, ProjectHost(snap))
}

// resolveTopics turns the request into a column list, defaulting to the
// topics with the most unused questions.
func (a *App) resolveTopics(r *http.Request, tenantID uuid.UUID, game *Game, req buildBoardRequest) ([]string, error) {
	if len(req.Topics) > 0 && !req.Auto {
		out := make([]string, 0, len(req.Topics))
		for _, t := range req.Topics {
			if k := FoldKey(t); k != "" {
				out = append(out, k)
			}
		}
		if len(out) != game.BoardColumns {
			return nil, fmt.Errorf("this board has %d columns but %d topics were chosen",
				game.BoardColumns, len(out))
		}
		return out, nil
	}
	hist, err := TopicHistogram(r.Context(), a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	seed := int64(0)
	if req.Auto {
		// Auto rerolls; the plain default is stable so reopening the page
		// does not silently change the host's columns underneath them.
		seed = time.Now().UnixNano()
	}
	topics := PickTopics(hist, game.BoardColumns, game.BoardRows, seed)
	if len(topics) < game.BoardColumns {
		return nil, fmt.Errorf("only %d topics have at least %d questions — this board needs %d columns",
			len(topics), game.BoardRows, game.BoardColumns)
	}
	return topics, nil
}

// assignBoard runs the matching over the bank, least-recently-used first.
func (a *App) assignBoard(r *http.Request, tenantID uuid.UUID, game *Game, topics []string) ([]BoardCell, error) {
	bank, err := QuestionsForTopics(r.Context(), a.pool, tenantID, topics)
	if err != nil {
		return nil, err
	}
	cands := make([]BoardCandidate, 0, len(bank))
	for _, q := range bank {
		keys := make([]string, 0, len(q.Topics))
		for _, t := range q.Topics {
			keys = append(keys, t.Key)
		}
		cands = append(cands, BoardCandidate{QuestionID: q.ID.String(), TopicKeys: keys})
	}
	labels := topicLabels(bank)

	seed := rand.Int63() //nolint:gosec // board variety, not secrecy
	placed, err := BuildBoard(topics, game.BoardRows, game.CellValues, cands, seed)
	if err != nil {
		return nil, err
	}
	out := make([]BoardCell, 0, len(placed))
	for _, c := range placed {
		qid, err := uuid.Parse(c.QuestionID)
		if err != nil {
			return nil, fmt.Errorf("parsing question id: %w", err)
		}
		label := labels[c.Topic]
		if label == "" {
			label = c.Topic
		}
		out = append(out, BoardCell{
			ColIndex: c.ColIndex, RowIndex: c.RowIndex,
			Topic: label, Points: c.Points, QuestionID: qid,
		})
	}
	return out, nil
}

// topicLabels maps a folded key back to a display spelling, so the board
// header reads "Sports" rather than "sports".
func topicLabels(bank []Question) map[string]string {
	out := map[string]string{}
	for _, q := range bank {
		for _, t := range q.Topics {
			if _, seen := out[t.Key]; !seen {
				out[t.Key] = t.Label
			}
		}
	}
	return out
}
