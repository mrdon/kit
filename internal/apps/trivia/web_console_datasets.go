package trivia

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
)

// The dataset surface: list, rename, delete, and choose which ones a game
// draws from. Uploading is handled by the import endpoint, which creates or
// replaces a dataset by name.

func (a *App) handleListDatasets(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	sets, err := ListDatasets(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "listing trivia datasets", err)
		return
	}
	writeJSON(w, map[string]any{"datasets": sets})
}

type datasetPatch struct {
	Name  string `json:"name"`
	Notes string `json:"notes"`
}

func (a *App) handleRenameDataset(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		clientError(w, r, http.StatusNotFound, "not a dataset id")
		return
	}
	var req datasetPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		clientError(w, r, http.StatusBadRequest, "a dataset name is 1 to 60 characters")
		return
	}
	switch err := RenameDataset(r.Context(), a.pool, tenant.ID, id, name, strings.TrimSpace(req.Notes)); {
	case errors.Is(err, ErrNotFound):
		clientError(w, r, http.StatusNotFound, "no such dataset")
		return
	case errors.Is(err, ErrBadRequest):
		clientError(w, r, http.StatusConflict, err.Error())
		return
	case err != nil:
		serverError(w, "renaming trivia dataset", err)
		return
	}
	a.handleListDatasets(w, r)
}

// handleDeleteDataset removes a set and its questions.
//
// A set whose questions are on some game's board is refused rather than
// cascaded: board cells reference questions ON DELETE RESTRICT, because
// deleting the question a cell is about would leave a game unable to say what
// it asked.
func (a *App) handleDeleteDataset(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		clientError(w, r, http.StatusNotFound, "not a dataset id")
		return
	}
	switch err := DeleteDataset(r.Context(), a.pool, tenant.ID, id); {
	case errors.Is(err, ErrNotFound):
		clientError(w, r, http.StatusNotFound, "no such dataset")
		return
	case errors.Is(err, ErrDatasetInUse):
		clientError(w, r, http.StatusConflict,
			"questions from this set are on the board of a game still in play — finish or delete that game first")
		return
	case err != nil:
		serverError(w, "deleting trivia dataset", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type gameDatasetsRequest struct {
	// DatasetIDs is the game's selection. An EMPTY list means every dataset,
	// which is what keeps a game playable when its only selected set is later
	// deleted.
	DatasetIDs []string `json:"dataset_ids"`
}

func (a *App) handleSetGameDatasets(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	if game.Phase != PhaseSetup && game.Phase != PhaseLobby {
		clientError(w, r, http.StatusConflict,
			"the question sets can only change before the game starts")
		return
	}
	var req gameDatasetsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	ids := make([]uuid.UUID, 0, len(req.DatasetIDs))
	for _, raw := range req.DatasetIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			clientError(w, r, http.StatusBadRequest, "not a dataset id: "+raw)
			return
		}
		ids = append(ids, id)
	}
	if err := SetGameDatasets(r.Context(), a.pool, tenant.ID, game.ID, ids); err != nil {
		serverError(w, "selecting trivia datasets", err)
		return
	}

	// Hand back the topics this game can now draw from, so the column picker
	// updates in the same round trip rather than needing a reload.
	hist, err := TopicHistogram(r.Context(), a.pool, tenant.ID, ids)
	if err != nil {
		serverError(w, "loading topic histogram", err)
		return
	}
	if hist == nil {
		hist = []TopicCount{}
	}
	writeJSON(w, map[string]any{"selected": req.DatasetIDs, "topics": hist})
}
