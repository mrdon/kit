package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// The staff-mapping admin page.
//
// Both sides of the pairing are opaque ids -- Square's TM… and Slack's U… --
// so the page never asks anyone to type one. It serves two name-labelled
// lists and stores the ids behind them.

// registerStaffRoutes wires the mapping page. All admin-only.
func registerStaffRoutes(mux apps.Mux, a *App) {
	adminRoute := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/events/staff", adminRoute(a.handleGetStaff))
	mux.Handle("PUT /{slug}/api/events/staff", adminRoute(a.handleSaveStaffMapping))
	mux.Handle("PUT /{slug}/api/events/staff/channel", adminRoute(a.handleSaveNoticeChannel))
	mux.Handle("POST /{slug}/api/events/staff/preview", adminRoute(a.handlePreviewNotices))
	mux.Handle("POST /{slug}/api/events/staff/send", adminRoute(a.handleSendNotices))
}

// slackOption is one entry in the Slack side of the picker.
type slackOption struct {
	SlackUserID string `json:"slack_user_id"`
	Name        string `json:"name"`
}

// channelOption is one entry in the "where do notices go" picker.
type channelOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// BotIsMember is why a picked channel might still not work: posting to a
	// public channel the bot has not been invited to fails, and a picker that
	// hides this hands you a channel you can select and then never hear from.
	BotIsMember bool `json:"bot_is_member"`
	IsPrivate   bool `json:"is_private"`
}

type staffPayload struct {
	SquareConnected bool            `json:"square_connected"`
	NoticeChannelID string          `json:"notice_channel_id"`
	Channels        []channelOption `json:"channels"`
	ChannelsError   string          `json:"channels_error,omitempty"`
	Staff           []StaffMember   `json:"staff"`
	SlackUsers      []slackOption   `json:"slack_users"`
	Mappings        []StaffMapping  `json:"mappings"`

	// StaffError and SlackError explain an empty dropdown rather than leaving
	// an admin staring at one with no reason given.
	StaffError string `json:"staff_error,omitempty"`
	SlackError string `json:"slack_error,omitempty"`

	Recent []noticeRunPayload `json:"recent"`
}

// MarshalJSON guarantees the array fields serialise as `[]` rather than
// `null`, so the React page cannot die on `.length` of null in the first-run
// state where nothing is configured yet. Same reasoning as settingsPayload.
func (p staffPayload) MarshalJSON() ([]byte, error) {
	type alias staffPayload // shed the method, or this recurses
	if p.Staff == nil {
		p.Staff = []StaffMember{}
	}
	if p.SlackUsers == nil {
		p.SlackUsers = []slackOption{}
	}
	if p.Mappings == nil {
		p.Mappings = []StaffMapping{}
	}
	if p.Channels == nil {
		p.Channels = []channelOption{}
	}
	if p.Recent == nil {
		p.Recent = []noticeRunPayload{}
	}
	return json.Marshal(alias(p))
}

type noticeRunPayload struct {
	At          string `json:"at"`
	OK          bool   `json:"ok"`
	TriggeredBy string `json:"triggered_by"`
	Posted      bool   `json:"posted"`
	Skipped     bool   `json:"skipped"`
	Mentions    int    `json:"mentions"`
	Unmapped    int    `json:"unmapped"`
	Error       string `json:"error,omitempty"`
}

func (a *App) handleGetStaff(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	payload, err := a.buildStaffPayload(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, payload)
}

// buildStaffPayload assembles both dropdowns plus the current pairings in one
// round trip, following the calendar picker: the client cannot render a
// half-loaded page, and either list failing degrades to an explanation rather
// than taking the page down with it.
func (a *App) buildStaffPayload(ctx context.Context, tenantID uuid.UUID) (staffPayload, error) {
	payload := staffPayload{}

	roster, err := staffRoster(ctx, tenantID)
	switch {
	case errors.Is(err, square.ErrNotConfigured):
		payload.StaffError = "Square is not connected yet. Connect it on the Integrations page to see who is on the schedule."
	case errors.Is(err, square.ErrNotReady):
		payload.StaffError = "Square is connected but the app credentials are not configured on this deployment."
	case err != nil:
		slog.Warn("events: listing square roster", "tenant_id", tenantID, "error", err)
		payload.StaffError = "Could not reach Square: " + err.Error()
	default:
		payload.SquareConnected = true
		payload.Staff = roster
		if len(roster) == 0 {
			payload.StaffError = "Nobody is on the published Square schedule for the next two months. Publish a schedule in Square and they will appear here."
		}
	}

	payload.SlackUsers, payload.SlackError = a.listSlackOptions(ctx, tenantID)

	payload.Mappings, err = listStaffMappings(ctx, a, tenantID)
	if err != nil {
		return staffPayload{}, err
	}

	settings, err := a.svc.Settings(ctx, tenantID)
	if err != nil {
		return staffPayload{}, err
	}
	payload.NoticeChannelID = settings.NoticeChannelID
	payload.Channels, payload.ChannelsError = a.listChannelOptions(ctx, tenantID)

	runs, err := a.ListRecentNoticeRuns(ctx, tenantID, 8)
	if err != nil {
		return staffPayload{}, err
	}
	for _, run := range runs {
		payload.Recent = append(payload.Recent, noticeRunPayload{
			At:          run.CreatedAt.Format("2006-01-02 15:04"),
			OK:          run.Succeeded(),
			TriggeredBy: run.Meta.TriggeredBy,
			Posted:      run.Meta.Posted,
			Skipped:     run.Meta.Skipped,
			Mentions:    run.Meta.Mentions,
			Unmapped:    run.Meta.Unmapped,
			Error:       run.Meta.Error,
		})
	}
	return payload, nil
}

// listSlackOptions returns the workspace members to choose from.
//
// It reads Slack directly rather than Kit's users table on purpose. A user row
// appears only once someone has interacted with Kit, and taproom staff have no
// reason ever to have DM'd the bot -- so the table would offer a list that
// excludes almost everyone the admin needs to pick. The row is created on save
// instead, by EnsureUserBySlackID.
func (a *App) listSlackOptions(ctx context.Context, tenantID uuid.UUID) ([]slackOption, string) {
	if a.enc == nil {
		return nil, "Slack is not configured on this deployment."
	}
	tenant, err := models.GetTenantByID(ctx, a.pool, tenantID)
	if err != nil || tenant == nil {
		return nil, "Could not load this workspace's Slack connection."
	}
	botToken, err := a.enc.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, "Could not decrypt this workspace's Slack token."
	}
	users, err := kitslack.NewClient(botToken).ListAllUsers(ctx)
	if err != nil {
		slog.Warn("events: listing slack users", "tenant_id", tenantID, "error", err)
		return nil, "Could not list Slack members: " + err.Error()
	}
	out := make([]slackOption, 0, len(users))
	for _, u := range users {
		name := strings.TrimSpace(u.DisplayName)
		if name == "" {
			name = u.SlackUserID
		}
		out = append(out, slackOption{SlackUserID: u.SlackUserID, Name: name})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, ""
}

// handleSaveStaffMapping sets or clears one pairing. An empty slack_user_id
// clears, which is how you stop someone's notices without touching Square or
// Slack.
func (a *App) handleSaveStaffMapping(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		SquareTeamMemberID string `json:"square_team_member_id"`
		SlackUserID        string `json:"slack_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.SquareTeamMemberID) == "" {
		eventsErr(w, http.StatusBadRequest, "a Square team member is required")
		return
	}

	if strings.TrimSpace(body.SlackUserID) == "" {
		if err := clearStaffMapping(r.Context(), a, caller.TenantID, body.SquareTeamMemberID); err != nil {
			a.serviceErr(w, err)
			return
		}
	} else if _, err := setStaffMapping(r.Context(), a, caller.TenantID, body.SquareTeamMemberID, body.SlackUserID); err != nil {
		eventsErr(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := a.buildStaffPayload(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, payload)
}

// listChannelOptions returns the channels the notice could be posted to.
func (a *App) listChannelOptions(ctx context.Context, tenantID uuid.UUID) ([]channelOption, string) {
	client, err := a.slackClient(ctx, tenantID)
	if err != nil {
		return nil, "Could not reach Slack: " + err.Error()
	}
	channels, err := client.ListChannels(ctx)
	if err != nil {
		slog.Warn("events: listing slack channels", "tenant_id", tenantID, "error", err)
		return nil, "Could not list Slack channels: " + err.Error()
	}
	out := make([]channelOption, 0, len(channels))
	for _, c := range channels {
		out = append(out, channelOption{
			ID: c.ID, Name: c.Name, BotIsMember: c.IsMember, IsPrivate: c.IsPrivate,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, ""
}

// handleSaveNoticeChannel points notices at a channel, or turns them off with
// an empty id.
//
// A channel the bot is not in is refused rather than saved. Slack rejects the
// post at 8am with not_in_channel, by which point nobody is watching -- the
// only moment this is cheap to catch is while an admin is standing here.
func (a *App) handleSaveNoticeChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	settings, err := a.svc.Settings(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	settings.TenantID = caller.TenantID
	settings.NoticeChannelID = strings.TrimSpace(body.ChannelID)
	settings.NoticeChannelName = ""

	if settings.NoticeChannelID != "" {
		options, listErr := a.listChannelOptions(r.Context(), caller.TenantID)
		if listErr != "" {
			eventsErr(w, http.StatusBadGateway, listErr)
			return
		}
		var picked *channelOption
		for i := range options {
			if options[i].ID == settings.NoticeChannelID {
				picked = &options[i]
				break
			}
		}
		if picked == nil {
			eventsErr(w, http.StatusBadRequest, "That channel is not one Kit can see.")
			return
		}
		if !picked.BotIsMember {
			eventsErr(w, http.StatusBadRequest,
				"Kit is not in #"+picked.Name+" yet, so it could not post there. Invite it with /invite @Kit and try again.")
			return
		}
		settings.NoticeChannelName = picked.Name
	}

	if _, err := a.svc.SaveSettings(r.Context(), settings); err != nil {
		a.serviceErr(w, err)
		return
	}
	payload, err := a.buildStaffPayload(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, payload)
}

// handlePreviewNotices shows exactly what would go out right now, to whom.
//
// These messages go to real people about private bookings, so seeing the text
// before anyone receives it is the default way to check the mapping is right.
func (a *App) handlePreviewNotices(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	notice, err := a.PreviewShiftNotices(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{
		"dry_run": true,
		"notice":  notice,
		"message": FormatNoticePreview(notice),
	})
}

// handleSendNotices runs the notice pass now. Already-delivered notices are
// suppressed by the notice log, so pressing this twice does not double-DM.
func (a *App) handleSendNotices(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	sum, err := a.RunShiftNotices(r.Context(), caller.TenantID, "manual")
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	payload, err := a.buildStaffPayload(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{
		"message": "Shift notices: " + sum.String() + ".",
		"staff":   payload,
	})
}
