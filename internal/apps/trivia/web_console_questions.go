package trivia

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"path/filepath"
	"strings"
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
// importTarget names the dataset an import writes into.
type importTarget struct {
	Name       string
	Notes      string
	BuiltinKey string
	// Replace empties the dataset first, so an upload is the dataset's new
	// contents rather than an addition to them.
	Replace bool
}

type importResponse struct {
	DatasetID         string       `json:"dataset_id"`
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
		clientError(w, r, http.StatusBadRequest, "the file is too large or not a valid upload")
		return
	}
	file, header, err := r.FormFile("csv")
	if err != nil {
		clientError(w, r, http.StatusBadRequest, "no csv file in the upload")
		return
	}
	defer func() { _ = file.Close() }()

	// The dataset is named by the host, falling back to the file name — which
	// is usually what they meant anyway ("christmas-2026.csv").
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" && header != nil {
		name = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}
	if name == "" {
		name = "Untitled set"
	}
	if len(name) > 60 {
		clientError(w, r, http.StatusBadRequest, "a dataset name is at most 60 characters")
		return
	}

	plan, err := ParseCSV(file)
	if err != nil {
		// A header the parser cannot read is the host's to fix, and the
		// message names what it found against what it wanted.
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := a.importPlan(r, tenant.ID, plan, importTarget{
		Name:    name,
		Notes:   strings.TrimSpace(r.FormValue("notes")),
		Replace: true,
	})
	if err != nil {
		if errors.Is(err, ErrDatasetInUse) {
			clientError(w, r, http.StatusConflict,
				"questions from this set are on the board of a game still in play — finish or delete that game first")
			return
		}
		serverError(w, "importing trivia questions", err)
		return
	}
	writeJSON(w, resp)
}

// importPlan writes the good rows and reports the bad. A three-hundred-row
// sheet with two typos is not a total failure.
func (a *App) importPlan(r *http.Request, tenantID uuid.UUID, plan ImportPlan, opts importTarget) (importResponse, error) {
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

	datasetID, err := UpsertDataset(r.Context(), tx, tenantID, opts.Name, opts.Notes, opts.BuiltinKey)
	if err != nil {
		return resp, err
	}
	// A re-upload REPLACES the dataset's contents rather than merging into
	// them. Merging would make a removed question impossible to remove: you
	// would fix your sheet, upload it, and the stale row would still be
	// there.
	if opts.Replace {
		if err := ClearDataset(r.Context(), tx, tenantID, datasetID); err != nil {
			return resp, err
		}
	}

	for _, q := range plan.Rows {
		_, inserted, err := UpsertQuestion(r.Context(), tx, tenantID, datasetID, q)
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

	resp.DatasetID = datasetID.String()
	hist, err := TopicHistogram(r.Context(), a.pool, tenantID, []uuid.UUID{datasetID}, Freshness{})
	if err != nil {
		return resp, err
	}
	resp.Topics = hist
	if resp.Topics == nil {
		resp.Topics = []TopicCount{}
	}
	return resp, nil
}

// handleListQuestions serves the workspace's datasets plus the overall
// histogram, so the Trivia page can say what is available before a game
// exists.
func (a *App) handleListQuestions(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	sets, err := ListDatasets(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "listing trivia datasets", err)
		return
	}
	hist, err := TopicHistogram(r.Context(), a.pool, tenant.ID, nil, Freshness{})
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
	writeJSON(w, map[string]any{
		"total": total, "topics": hist, "datasets": sets, "packs": BuiltinPacks,
	})
}

// handleLoadStarter imports the embedded starter pack straight into the
// workspace bank — no download-and-re-upload round trip.
//
// Idempotent, because the import path upserts on prompt_key: loading it twice
// updates 62 rows and adds none. That is what makes it safe to offer as a
// button rather than a one-time setup step.
func (a *App) handleLoadStarter(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	key := r.PathValue("key")
	if key == "" {
		key = "starter"
	}
	body, pack, err := BuiltinPackCSV(key)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "no such pack")
		return
	}
	plan, err := ParseCSV(bytes.NewReader(body))
	if err != nil {
		serverError(w, "parsing a shipped trivia pack", err)
		return
	}
	resp, err := a.importPlan(r, tenant.ID, plan, importTarget{
		Name:       pack.Name,
		Notes:      pack.Notes,
		BuiltinKey: pack.Key,
		Replace:    true,
	})
	if err != nil {
		if errors.Is(err, ErrDatasetInUse) {
			clientError(w, r, http.StatusConflict,
				"that set is on the board of a game still in play — finish or delete that game first")
			return
		}
		serverError(w, "importing a shipped trivia pack", err)
		return
	}
	writeJSON(w, resp)
}

// handleSampleCSV serves the starter sheet as a download, for a host who
// wants it as a template to build their own from.
func (a *App) handleSampleCSV(w http.ResponseWriter, _ *http.Request) {
	body, err := SampleCSV()
	if err != nil {
		serverError(w, "reading the trivia sample sheet", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="trivia-sample-questions.csv"`)
	if _, err := w.Write(body); err != nil {
		slog.Warn("writing trivia sample sheet", "error", err)
	}
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
		clientError(w, r, http.StatusConflict, "that question is on a game's board and cannot be deleted")
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
		clientError(w, r, http.StatusConflict, "the board can only be built before the game starts")
		return
	}
	var req buildBoardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}

	datasetIDs, err := GameDatasetIDs(r.Context(), a.pool, tenant.ID, game.ID)
	if err != nil {
		serverError(w, "loading game datasets", err)
		return
	}

	topics, err := a.resolveTopics(r, tenant.ID, game, req, datasetIDs)
	if err != nil {
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	cells, err := a.assignBoard(r, tenant.ID, game, topics, datasetIDs)
	if err != nil {
		var se *ShortfallError
		if errors.As(err, &se) {
			// The host is in a bar at 7pm. Name the column and the gap.
			clientError(w, r, http.StatusUnprocessableEntity, se.Error())
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
func (a *App) resolveTopics(r *http.Request, tenantID uuid.UUID, game *Game, req buildBoardRequest, datasetIDs []uuid.UUID) ([]string, error) {
	// EVERY ROUND GETS ITS OWN CATEGORIES, so a two-round night wants twice
	// the columns. They arrive as one flat list and are sliced per round in
	// assignBoard, in the order the host ticked them -- the first five are
	// round one, the next five are what the room comes back to.
	want := game.BoardColumns * boardRoundsOf(game)
	if len(req.Topics) > 0 && !req.Auto {
		out := make([]string, 0, len(req.Topics))
		for _, t := range req.Topics {
			if k := FoldKey(t); k != "" {
				out = append(out, k)
			}
		}
		if len(out) != want {
			return nil, fmt.Errorf("this night needs %d categories but %d were chosen",
				want, len(out))
		}
		return out, nil
	}
	hist, err := TopicHistogram(r.Context(), a.pool, tenantID, datasetIDs, freshnessOf(game))
	if err != nil {
		return nil, err
	}
	seed := int64(0)
	if req.Auto {
		// Auto rerolls; the plain default is stable so reopening the page
		// does not silently change the host's columns underneath them.
		seed = time.Now().UnixNano()
	}
	topics := PickTopics(hist, want, game.BoardRows, seed)
	if len(topics) < want {
		kind := "fresh question"
		if game.RepeatQuestions {
			kind = "question"
		}
		if game.BoardRows != 1 {
			kind += "s"
		}
		return nil, fmt.Errorf("only %d categories have at least %d %s — this night needs %d",
			len(topics), game.BoardRows, kind, want)
	}
	return topics, nil
}

// boardRoundsOf reads the round count off a game, defaulting a zero to one so
// a row written before migration 104 -- or a test that built a Game by hand
// -- still describes a playable night.
func boardRoundsOf(g *Game) int {
	if g.BoardRounds < 1 {
		return 1
	}
	return g.BoardRounds
}

// assignBoard runs the matching over the bank this game may draw on. The seed
// is fresh per call, so pressing Auto twice gives two different boards from
// the same questions.
func (a *App) assignBoard(r *http.Request, tenantID uuid.UUID, game *Game, topics []string, datasetIDs []uuid.UUID) ([]BoardCell, error) {
	rounds := boardRoundsOf(game)
	cols := game.BoardColumns
	// taken carries across rounds because the bank query cannot: it reads the
	// database, and none of this is written until every round has been drawn.
	taken := map[uuid.UUID]bool{}
	out := make([]BoardCell, 0, cols*game.BoardRows*rounds)
	for round := range rounds {
		seed := rand.Int63() //nolint:gosec // board variety, not secrecy
		slice := topics[round*cols : (round+1)*cols]
		cells, err := drawRound(r.Context(), a.pool, tenantID, game, slice, datasetIDs, seed, round, taken)
		if err != nil {
			return nil, err
		}
		for _, c := range cells {
			taken[c.QuestionID] = true
		}
		out = append(out, cells...)
	}
	return out, nil
}
