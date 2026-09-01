package events

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The promotion surfaces: the work list that hangs off the Events page, and
// the admin page where channels are defined.
//
// The split in gating mirrors the rest of the app. Working through the list is
// everyday work anyone who can publish an event can do, so it uses
// console.JSON. Defining a destination -- its mode, its submit URL, its
// campaign -- is configuration, so it is admin-only.

func registerPromoRoutes(mux apps.Mux, a *App) {
	jsonRoute := func(h http.HandlerFunc) http.Handler {
		return console.JSON(a.pool, a.signer, h)
	}
	adminRoute := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}

	mux.Handle("GET /{slug}/api/events/promo", jsonRoute(a.handlePromoList))
	mux.Handle("POST /{slug}/api/events/promo/mark", jsonRoute(a.handlePromoMark))

	// Channels sit on their own prefix rather than under /api/events/, because
	// "channels/{id}" and the existing "{id}/poster" are genuinely ambiguous to
	// net/http's mux -- both match /api/events/channels/poster and neither is
	// more specific. That is a startup panic, not a routing subtlety, so the
	// prefix stays distinct.
	mux.Handle("GET /{slug}/api/event-channels", adminRoute(a.handleListChannels))
	mux.Handle("POST /{slug}/api/event-channels", adminRoute(a.handleSaveChannel))
	mux.Handle("PUT /{slug}/api/event-channels/{id}", adminRoute(a.handleSaveChannel))
	mux.Handle("DELETE /{slug}/api/event-channels/{id}", adminRoute(a.handleDeleteChannel))
}

// promoPayload is the Events page's work list.
//
// Split into actionable and done rather than handed over as one array with a
// status field, because the page renders them differently: the actionable
// items are the list you work, and the completed ones collapse into a "done
// automatically" group. Doing the split here keeps the client from
// re-deriving the actionable rule.
type promoPayload struct {
	Items   []PromoItem  `json:"items"`
	Done    []PromoItem  `json:"done"`
	Summary PromoSummary `json:"summary"`
	// Channels is the name/mode list, so the page can explain an empty state
	// ("no channels configured yet") rather than looking broken.
	Channels []Channel `json:"channels"`
}

func (p promoPayload) MarshalJSON() ([]byte, error) {
	type alias promoPayload // shed the method, or this recurses
	if p.Items == nil {
		p.Items = []PromoItem{}
	}
	if p.Done == nil {
		p.Done = []PromoItem{}
	}
	if p.Channels == nil {
		p.Channels = []Channel{}
	}
	return json.Marshal(alias(p))
}

func (a *App) handlePromoList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())

	all, err := a.svc.PromoList(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	channels, err := a.svc.ListChannels(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}

	payload := promoPayload{Summary: summarisePromo(all), Channels: channels}
	for _, it := range all {
		if it.State.actionable() {
			payload.Items = append(payload.Items, it)
			continue
		}
		payload.Done = append(payload.Done, it)
	}
	eventsJSON(w, http.StatusOK, payload)
}

type promoMarkRequest struct {
	EventID   string     `json:"event_id"`
	ChannelID string     `json:"channel_id"`
	StepKey   string     `json:"step_key"`
	Status    PromoState `json:"status"`
	URL       string     `json:"url"`
	Note      string     `json:"note"`
}

func (a *App) handlePromoMark(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())

	var req promoMarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		eventsJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return
	}
	eventID, err := uuid.Parse(req.EventID)
	if err != nil {
		eventsJSON(w, http.StatusBadRequest, map[string]string{"error": "bad event id"})
		return
	}
	channelID, err := uuid.Parse(req.ChannelID)
	if err != nil {
		eventsJSON(w, http.StatusBadRequest, map[string]string{"error": "bad channel id"})
		return
	}

	userID := caller.UserID
	if err := a.svc.MarkPromo(r.Context(), caller.TenantID, eventID, channelID,
		req.StepKey, req.Status, req.URL, req.Note, &userID); err != nil {
		a.serviceErr(w, err)
		return
	}
	a.handlePromoList(w, r)
}

// channelsPayload carries the feed URLs alongside the channels, because the
// admin page is where you copy the address to send a chamber -- the same page
// where you then mark them subscribed.
type channelsPayload struct {
	Channels []Channel `json:"channels"`
	FeedURLs feedURLs  `json:"feed_urls"`
}

type feedURLs struct {
	All        string `json:"all"`
	Highlights string `json:"highlights"`
	Featured   string `json:"featured"`
}

func (p channelsPayload) MarshalJSON() ([]byte, error) {
	type alias channelsPayload
	if p.Channels == nil {
		p.Channels = []Channel{}
	}
	return json.Marshal(alias(p))
}

func (a *App) handleListChannels(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	channels, err := a.svc.ListChannels(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}

	// The public addresses, not Kit's. What a chamber subscribes to is the
	// copy the site republishes token-free; handing over the Kit URL would
	// give them one that 401s.
	settings, err := getSettings(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, channelsPayload{
		Channels: channels,
		FeedURLs: publicFeedURLs(settings),
	})
}

// publicFeedURLs derives the three published calendar addresses from the
// site's own URL template. Empty when no template is configured -- the admin
// page then says so rather than showing three broken links.
func publicFeedURLs(s Settings) feedURLs {
	base := s.SiteBaseURL()
	if base == "" {
		return feedURLs{}
	}
	return feedURLs{
		All:        base + "/events.ics",
		Highlights: base + "/events-highlights.ics",
		Featured:   base + "/events-featured.ics",
	}
}

func (a *App) handleSaveChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())

	var c Channel
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		eventsJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return
	}
	// The path id wins over the body's, so a PUT cannot be talked into
	// updating a different row than the one it addresses.
	if raw := r.PathValue("id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			eventsJSON(w, http.StatusBadRequest, map[string]string{"error": "bad channel id"})
			return
		}
		c.ID = id
	}

	if _, err := a.svc.SaveChannel(r.Context(), caller.TenantID, c); err != nil {
		a.serviceErr(w, err)
		return
	}
	a.handleListChannels(w, r)
}

func (a *App) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		eventsJSON(w, http.StatusBadRequest, map[string]string{"error": "bad channel id"})
		return
	}
	if err := a.svc.DeleteChannel(r.Context(), caller.TenantID, id); err != nil {
		a.serviceErr(w, err)
		return
	}
	a.handleListChannels(w, r)
}
