package trivia

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/livegame"
)

// The Admin panel that says where end-of-night ratings are posted. The
// payload and the channel list are livegame's, shared with Hoppers; the
// stored choice is trivia's own.

func (a *App) handleGetFeedbackChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	current, err := GetFeedbackChannel(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		serverError(w, "loading trivia feedback channel", err)
		return
	}
	writeJSON(w, livegame.NewChannelPayload(r.Context(), a.pool, a.enc, caller.TenantID, current))
}

// handleSaveFeedbackChannel stores the admin's pick. A channel Kit is not in
// is refused with a sentence saying how to fix it, rather than saved and then
// silently never posted to.
func (a *App) handleSaveFeedbackChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	choice := livegame.FeedbackChannel{}
	if strings.TrimSpace(body.ChannelID) != "" {
		options, listErr := livegame.ChannelOptions(r.Context(), a.pool, a.enc, caller.TenantID)
		if listErr != "" {
			clientError(w, r, http.StatusBadGateway, listErr)
			return
		}
		picked, err := livegame.PickChannel(options, body.ChannelID)
		if err != nil {
			clientError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		choice = picked
	}
	if err := SaveFeedbackChannel(r.Context(), a.pool, caller.TenantID, choice); err != nil {
		serverError(w, "saving trivia feedback channel", err)
		return
	}
	writeJSON(w, livegame.NewChannelPayload(r.Context(), a.pool, a.enc, caller.TenantID, choice))
}
