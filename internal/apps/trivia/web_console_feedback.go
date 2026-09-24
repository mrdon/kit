package trivia

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
)

// The Admin panel that says where end-of-night ratings are posted.

// feedbackChannelOption is one entry in the picker.
type feedbackChannelOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// BotIsMember is shown because a channel Kit is not in cannot be posted
	// to, and a picker that hides that hands an admin a choice that silently
	// never works.
	BotIsMember bool `json:"bot_is_member"`
	IsPrivate   bool `json:"is_private"`
}

type feedbackChannelPayload struct {
	// ChannelID is the channel ratings go to, "" when they go nowhere.
	ChannelID     string                  `json:"channel_id"`
	ChannelName   string                  `json:"channel_name"`
	Channels      []feedbackChannelOption `json:"channels"`
	ChannelsError string                  `json:"channels_error,omitempty"`
}

func (a *App) handleGetFeedbackChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	payload, err := a.feedbackChannelPayload(r.Context(), caller.TenantID)
	if err != nil {
		serverError(w, "loading trivia feedback channel", err)
		return
	}
	writeJSON(w, payload)
}

// handleSaveFeedbackChannel points ratings at a channel, or nowhere with an
// empty id. A channel Kit is not in is refused now, while an admin is looking,
// rather than failing quietly at the end of the next quiz.
func (a *App) handleSaveFeedbackChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	choice := FeedbackChannel{}
	if id := strings.TrimSpace(body.ChannelID); id != "" {
		options, listErr := a.feedbackChannelOptions(r.Context(), caller.TenantID)
		if listErr != "" {
			clientError(w, r, http.StatusBadGateway, listErr)
			return
		}
		var picked *feedbackChannelOption
		for i := range options {
			if options[i].ID == id {
				picked = &options[i]
			}
		}
		if picked == nil {
			clientError(w, r, http.StatusBadRequest, "That channel is not one Kit can see.")
			return
		}
		if !picked.BotIsMember {
			clientError(w, r, http.StatusBadRequest,
				"Kit is not in #"+picked.Name+" yet. Invite it with /invite @Kit and try again.")
			return
		}
		choice = FeedbackChannel{ID: picked.ID, Name: picked.Name}
	}
	if err := SaveFeedbackChannel(r.Context(), a.pool, caller.TenantID, choice); err != nil {
		serverError(w, "saving trivia feedback channel", err)
		return
	}
	payload, err := a.feedbackChannelPayload(r.Context(), caller.TenantID)
	if err != nil {
		serverError(w, "loading trivia feedback channel", err)
		return
	}
	writeJSON(w, payload)
}

// feedbackChannelPayload is the setting plus the channels to pick from.
func (a *App) feedbackChannelPayload(ctx context.Context, tenantID uuid.UUID) (feedbackChannelPayload, error) {
	current, err := GetFeedbackChannel(ctx, a.pool, tenantID)
	if err != nil {
		return feedbackChannelPayload{}, err
	}
	p := feedbackChannelPayload{ChannelID: current.ID, ChannelName: current.Name}
	p.Channels, p.ChannelsError = a.feedbackChannelOptions(ctx, tenantID)
	return p, nil
}

// feedbackChannelOptions lists the channels Kit can see, by name. An error
// comes back as text for the page to show, not as a failed request: the
// setting itself still loads without Slack.
func (a *App) feedbackChannelOptions(ctx context.Context, tenantID uuid.UUID) ([]feedbackChannelOption, string) {
	client, err := tenantSlackClient(ctx, a.pool, a.enc, tenantID)
	if err != nil {
		return nil, "Could not reach Slack: " + err.Error()
	}
	channels, err := client.ListChannels(ctx)
	if err != nil {
		return nil, "Could not list Slack channels: " + err.Error()
	}
	out := make([]feedbackChannelOption, 0, len(channels))
	for _, c := range channels {
		out = append(out, feedbackChannelOption{ID: c.ID, Name: c.Name, BotIsMember: c.IsMember, IsPrivate: c.IsPrivate})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, ""
}
