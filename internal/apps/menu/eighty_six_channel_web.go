package menu

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// The Admin page that says where 86s are posted. The same shape as trivia's
// ratings channel, so the console reuses its picker types.

type channelOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// BotIsMember is shown because Kit cannot post to a channel it is not in.
	BotIsMember bool `json:"bot_is_member"`
	IsPrivate   bool `json:"is_private"`
}

type eightySixChannelPayload struct {
	// ChannelID is where 86s go, "" when they go nowhere.
	ChannelID     string          `json:"channel_id"`
	ChannelName   string          `json:"channel_name"`
	Channels      []channelOption `json:"channels"`
	ChannelsError string          `json:"channels_error,omitempty"`
}

// A workspace setting, so admin only even though 86ing is not.
func registerEightySixChannelRoutes(mux apps.Mux, a *App) {
	route := func(h http.HandlerFunc) http.Handler {
		return console.RequireCapAdmin(a.pool, a.signer, auth.CapMenuEightySix, h)
	}
	mux.Handle("GET /{slug}/api/menu/86/channel", route(a.handleGetEightySixChannel))
	mux.Handle("PUT /{slug}/api/menu/86/channel", route(a.handleSaveEightySixChannel))
}

func (a *App) handleGetEightySixChannel(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	a.writeEightySixChannel(w, r.Context(), tenant.ID)
}

// handleSaveEightySixChannel points 86s at a channel, or nowhere with an
// empty id. A channel Kit is not in is refused now, while an admin is
// looking, rather than failing quietly on the next blown keg.
func (a *App) handleSaveEightySixChannel(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	choice := EightySixChannel{}
	if id := strings.TrimSpace(body.ChannelID); id != "" {
		options, listErr := a.channelOptions(r.Context(), tenant.ID)
		if listErr != "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": listErr})
			return
		}
		var picked *channelOption
		for i := range options {
			if options[i].ID == id {
				picked = &options[i]
			}
		}
		switch {
		case picked == nil:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That channel is not one Kit can see."})
			return
		case !picked.BotIsMember:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Kit is not in #" + picked.Name +
				" yet. Invite it with /invite @Kit and try again."})
			return
		}
		choice = EightySixChannel{ID: picked.ID, Name: picked.Name}
	}
	if err := a.SaveEightySixChannel(r.Context(), tenant.ID, choice); err != nil {
		slog.Error("saving 86 channel", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	a.writeEightySixChannel(w, r.Context(), tenant.ID)
}

func (a *App) writeEightySixChannel(w http.ResponseWriter, ctx context.Context, tenantID uuid.UUID) {
	current, err := a.GetEightySixChannel(ctx, tenantID)
	if err != nil {
		slog.Error("loading 86 channel", "tenant_id", tenantID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	p := eightySixChannelPayload{ChannelID: current.ID, ChannelName: current.Name}
	p.Channels, p.ChannelsError = a.channelOptions(ctx, tenantID)
	writeJSON(w, http.StatusOK, p)
}

// channelOptions lists the channels Kit can see, by name. An error comes back
// as text for the page to show: the setting itself still loads without Slack.
func (a *App) channelOptions(ctx context.Context, tenantID uuid.UUID) ([]channelOption, string) {
	client, err := kitslack.TenantClient(ctx, a.pool, a.enc, tenantID)
	if err != nil {
		return nil, "Could not reach Slack: " + err.Error()
	}
	channels, err := client.ListChannels(ctx)
	if err != nil {
		return nil, "Could not list Slack channels: " + err.Error()
	}
	out := make([]channelOption, 0, len(channels))
	for _, c := range channels {
		out = append(out, channelOption{ID: c.ID, Name: c.Name, BotIsMember: c.IsMember, IsPrivate: c.IsPrivate})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, ""
}
