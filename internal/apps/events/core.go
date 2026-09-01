package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// dispatchCore is the one implementation of every events tool. The agent
// registry and the MCP server are both thin wrappers over this function, which
// is what keeps the two surfaces in parity by construction rather than by two
// hand-maintained switches that drift apart.
func dispatchCore(ctx context.Context, caller *services.Caller, svc *Service, name string, raw json.RawMessage) (string, error) {
	if caller == nil {
		return "", errors.New("events: no caller on context")
	}
	switch name {
	case "create_event":
		return coreCreate(ctx, caller, svc, raw)
	case "update_event":
		return coreUpdate(ctx, caller, svc, raw)
	case "clone_event":
		return coreClone(ctx, caller, svc, raw)
	case "publish_event":
		return corePublish(ctx, caller, svc, raw)
	case "unpublish_event":
		return coreSimpleTransition(ctx, caller, svc, raw, svc.Unpublish, "Moved back to draft")
	case "cancel_event":
		return coreSimpleTransition(ctx, caller, svc, raw, svc.Cancel, "Cancelled")
	case "events_site_status":
		return coreSiteStatus(ctx, caller, svc)
	case "events_publish_site":
		return corePublishSite(ctx, caller, svc)
	case "delete_event":
		return coreDelete(ctx, caller, svc, raw)
	case "reopen_event":
		return coreSimpleTransition(ctx, caller, svc, raw, svc.Reopen, "Reopened as a draft")
	case "list_events":
		return coreList(ctx, caller, svc, raw)
	case "get_event":
		return coreGet(ctx, caller, svc, raw)
	case "event_poster_upload_url":
		return corePosterUploadURL(ctx, caller, raw)
	case "events_status":
		return coreStatus(ctx, caller, svc)
	case "events_sync_now":
		return coreSyncNow(ctx, caller)
	case "events_reconcile":
		return coreReconcile(ctx, caller, raw)
	case "events_staff_map":
		return coreStaffMap(ctx, caller)
	case "events_map_staff":
		return coreMapStaff(ctx, caller, raw)
	case "events_shift_notices":
		return coreShiftNotices(ctx, caller, raw)
	case "events_channels":
		return coreListChannels(ctx, caller, svc)
	case "events_add_channel":
		return coreSaveChannel(ctx, caller, svc, raw, true)
	case "events_update_channel":
		return coreSaveChannel(ctx, caller, svc, raw, false)
	case "events_remove_channel":
		return coreDeleteChannel(ctx, caller, svc, raw)
	case "events_promo_list":
		return corePromoList(ctx, caller, svc)
	default:
		return "", fmt.Errorf("events: unknown tool %q", name)
	}
}

// eventInput is the union of every field the create/update tools accept.
// Pointers throughout so an absent field is distinguishable from a cleared one
// -- that distinction is what makes update a partial patch.
type eventInput struct {
	EventID string `json:"event_id"`
	Slug    string `json:"slug"`

	Title       *string `json:"title"`
	Summary     *string `json:"summary"`
	Description *string `json:"description"`
	PrepNotes   *string `json:"prep_notes"`
	Location    *string `json:"location"`

	StartsAt *string `json:"starts_at"`
	EndsAt   *string `json:"ends_at"`
	AllDay   *bool   `json:"all_day"`
	Timezone *string `json:"timezone"`
	// RepeatRule is the tool-facing name; "rrule" is jargon.
	RepeatRule *string `json:"repeat_rule"`
	// RepeatDates is a pointer so update can tell "not sent" from "cleared" --
	// an empty list is a real instruction, meaning "back to a one-off".
	RepeatDates *[]string `json:"repeat_dates"`

	Visibility  *string `json:"visibility"`
	Venue       *string `json:"venue"`
	SpaceImpact *string `json:"space_impact"`

	PriceCents         *int64 `json:"price_cents"`
	ClearPrice         bool   `json:"clear_price"`
	Currency           *string
	Capacity           *int    `json:"capacity"`
	ClearCapacity      bool    `json:"clear_capacity"`
	ExpectedAttendance *int    `json:"expected_attendance"`
	RegistrationURL    *string `json:"registration_url"`
	NotifyFoodPartner  *bool   `json:"notify_food_partner"`
	// Prominence is featured / normal / background. Featured is its boolean
	// predecessor, still accepted -- an agent working from a saved prompt, or
	// an MCP client written against the old schema, should not start failing.
	// Note both were declared in the tool schema before this and neither was
	// ever read, so "featured: true" from the agent silently did nothing.
	Prominence *string `json:"prominence"`
	Featured   *bool   `json:"featured"`
}

// prominenceFrom folds the tool input's two spellings into one value.
func (in eventInput) prominenceFrom() *Prominence {
	var p *Prominence
	if in.Prominence != nil {
		v := Prominence(strings.ToLower(strings.TrimSpace(*in.Prominence)))
		p = &v
	}
	return ResolveProminence(p, in.Featured)
}

func coreCreate(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	var in eventInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	p := CreateParams{
		Title:              deref(in.Title),
		Summary:            deref(in.Summary),
		Description:        deref(in.Description),
		PrepNotes:          deref(in.PrepNotes),
		Location:           deref(in.Location),
		StartsAt:           deref(in.StartsAt),
		EndsAt:             deref(in.EndsAt),
		AllDay:             in.AllDay != nil && *in.AllDay,
		Timezone:           deref(in.Timezone),
		RRule:              deref(in.RepeatRule),
		RepeatDates:        derefSlice(in.RepeatDates),
		Visibility:         Visibility(strings.ToLower(deref(in.Visibility))),
		Venue:              Venue(strings.ToLower(deref(in.Venue))),
		SpaceImpact:        SpaceImpact(strings.ToLower(deref(in.SpaceImpact))),
		PriceCents:         in.PriceCents,
		Currency:           deref(in.Currency),
		Capacity:           in.Capacity,
		ExpectedAttendance: in.ExpectedAttendance,
		RegistrationURL:    deref(in.RegistrationURL),
		NotifyFoodPartner:  in.NotifyFoodPartner,
		Prominence:         in.prominenceFrom(),
	}
	if caller.UserID != uuid.Nil {
		id := caller.UserID
		p.CreatedBy = &id
	}
	e, err := svc.Create(ctx, caller.TenantID, p)
	if err != nil {
		return userError(err)
	}
	settings, _ := svc.Settings(ctx, caller.TenantID)
	out := "Created as a draft — it is not visible anywhere yet. Publish it when confirmed.\n\n" +
		FormatEvent(e, settings)
	// Offered unprompted, because the moment right after a create is the only
	// moment the caller is definitely still thinking about this event, and a
	// poster added later is a poster never added. Best-effort: an event that
	// was created stays created even if no link could be minted.
	if app := Instance(); app != nil {
		out += app.posterUploadOffer(ctx, caller.TenantID, caller.UserID, e.ID)
	}
	return out, nil
}

// corePosterUploadURL mints a fresh one-time upload link for an existing
// event -- the same link create_event offers, for when that one has expired,
// has been spent, or the poster is simply being replaced.
func corePosterUploadURL(ctx context.Context, caller *services.Caller, raw json.RawMessage) (string, error) {
	var in struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	id, err := uuid.Parse(strings.TrimSpace(in.EventID))
	if err != nil {
		return "", errors.New("event_id must be an event id")
	}
	app := Instance()
	if app == nil {
		return "", errors.New("events app not available")
	}
	// Resolve the event first so a bad id fails as "no such event" rather than
	// handing back a link that dies on redemption -- and so a token is never
	// minted for an event in another tenant.
	if _, err := app.svc.Get(ctx, caller.TenantID, id); err != nil {
		return userError(err)
	}
	link, err := app.PosterUploadLink(ctx, caller.TenantID, caller.UserID, id)
	if err != nil {
		return "", err
	}
	return "One-time upload link, good for 15 minutes and a single POST:\n\n" +
		"  curl -F poster=@poster.jpg '" + link + "'\n\n" +
		"JPEG, PNG, WebP or GIF, up to 8MB. It replaces any poster already on the event.", nil
}

func coreUpdate(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	var in eventInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	id, err := uuid.Parse(strings.TrimSpace(in.EventID))
	if err != nil {
		return "That does not look like an event id.", nil
	}
	p := UpdateParams{
		Title: in.Title, Summary: in.Summary, Description: in.Description,
		PrepNotes: in.PrepNotes, Location: in.Location,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, AllDay: in.AllDay,
		Timezone: in.Timezone, RRule: in.RepeatRule,
		RepeatDates: in.RepeatDates,
		PriceCents:  in.PriceCents, ClearPrice: in.ClearPrice,
		Currency: in.Currency, Capacity: in.Capacity, ClearCapacity: in.ClearCapacity,
		ExpectedAttendance: in.ExpectedAttendance,
		RegistrationURL:    in.RegistrationURL,
		NotifyFoodPartner:  in.NotifyFoodPartner,
		Prominence:         in.prominenceFrom(),
	}
	if in.Visibility != nil {
		v := Visibility(strings.ToLower(*in.Visibility))
		p.Visibility = &v
	}
	if in.Venue != nil {
		v := Venue(strings.ToLower(*in.Venue))
		p.Venue = &v
	}
	if in.SpaceImpact != nil {
		v := SpaceImpact(strings.ToLower(*in.SpaceImpact))
		p.SpaceImpact = &v
	}
	if in.Slug != "" {
		s := in.Slug
		p.Slug = &s
	}
	e, err := svc.Update(ctx, caller.TenantID, id, p)
	if err != nil {
		return userError(err)
	}
	settings, _ := svc.Settings(ctx, caller.TenantID)
	return "Updated.\n\n" + FormatEvent(e, settings), nil
}

func corePublish(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	id, msg, ok := parseEventID(raw)
	if !ok {
		return msg, nil
	}
	res, err := svc.Publish(ctx, caller.TenantID, id)
	if err != nil {
		return userError(err)
	}
	settings, _ := svc.Settings(ctx, caller.TenantID)
	return "Published — " + describeExposure(res.Event) + ".\n\n" +
		FormatEvent(res.Event, settings) + FormatWarnings(res.Warnings), nil
}

type transitionFunc func(context.Context, uuid.UUID, uuid.UUID) (*Event, error)

func coreSimpleTransition(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage, fn transitionFunc, verb string) (string, error) {
	id, msg, ok := parseEventID(raw)
	if !ok {
		return msg, nil
	}
	e, err := fn(ctx, caller.TenantID, id)
	if err != nil {
		return userError(err)
	}
	settings, _ := svc.Settings(ctx, caller.TenantID)
	return verb + ".\n\n" + FormatEvent(e, settings), nil
}

func coreList(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	var in struct {
		Status     string `json:"status"`
		Visibility string `json:"visibility"`
		From       string `json:"from"`
		To         string `json:"to"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	settings, err := svc.Settings(ctx, caller.TenantID)
	if err != nil {
		return "", err
	}
	f := ListFilter{
		Status:     Status(strings.ToLower(in.Status)),
		Visibility: Visibility(strings.ToLower(in.Visibility)),
		Limit:      in.Limit,
	}
	loc := settings.Loc()
	if in.From != "" {
		t, err := ParseTime(in.From, loc)
		if err != nil {
			return userError(err)
		}
		f.From = &t
	}
	if in.To != "" {
		t, err := ParseTime(in.To, loc)
		if err != nil {
			return userError(err)
		}
		f.To = &t
	}
	list, err := svc.List(ctx, caller.TenantID, f)
	if err != nil {
		return "", err
	}
	return FormatEventList(list, settings), nil
}

func coreGet(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	var in struct {
		EventID string `json:"event_id"`
		Slug    string `json:"slug"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	var (
		e   *Event
		err error
	)
	switch {
	case strings.TrimSpace(in.EventID) != "":
		id, parseErr := uuid.Parse(strings.TrimSpace(in.EventID))
		if parseErr != nil {
			return "That does not look like an event id.", nil
		}
		e, err = svc.Get(ctx, caller.TenantID, id)
	case strings.TrimSpace(in.Slug) != "":
		e, err = svc.GetBySlug(ctx, caller.TenantID, strings.TrimSpace(in.Slug))
	default:
		return "Give either an event id or a slug.", nil
	}
	if err != nil {
		return userError(err)
	}
	settings, _ := svc.Settings(ctx, caller.TenantID)
	return FormatEvent(e, settings) + FormatOccurrences(e, 6), nil
}

func coreStatus(ctx context.Context, caller *services.Caller, svc *Service) (string, error) {
	settings, err := svc.Settings(ctx, caller.TenantID)
	if err != nil {
		return "", err
	}
	out := FormatSettings(settings)
	if app := Instance(); app != nil && app.pool != nil {
		runs, err := app.ListRecentRuns(ctx, caller.TenantID, 5)
		if err != nil {
			return "", err
		}
		out += FormatRuns(runs)
	}
	return out, nil
}

func coreSyncNow(ctx context.Context, caller *services.Caller) (string, error) {
	app := Instance()
	if app == nil {
		return "", errors.New("events app not available")
	}
	sum, err := app.SyncNow(ctx, caller.TenantID)
	if errors.Is(err, ErrNoCalendar) {
		return "No calendar is selected yet, so there is nothing to sync. Pick one on the Events settings page.", nil
	}
	if err != nil {
		return "", err
	}
	return "Calendar sync finished: " + sum.String() + ".", nil
}

func coreReconcile(ctx context.Context, caller *services.Caller, raw json.RawMessage) (string, error) {
	var in struct {
		DryRun bool `json:"dry_run"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	app := Instance()
	if app == nil {
		return "", errors.New("events app not available")
	}
	if in.DryRun {
		plan, err := app.PreviewReconcile(ctx, caller.TenantID)
		if errors.Is(err, ErrNoCalendar) {
			return "No calendar is selected yet, so there is nothing to reconcile.", nil
		}
		if err != nil {
			return "", err
		}
		return FormatReconcilePlan(plan), nil
	}
	sum, err := app.RunReconcile(ctx, caller.TenantID)
	if errors.Is(err, ErrNoCalendar) {
		return "No calendar is selected yet, so there is nothing to reconcile.", nil
	}
	if err != nil {
		return "", err
	}
	return "Reconcile finished: " + sum.String() + ".", nil
}

func parseEventID(raw json.RawMessage) (uuid.UUID, string, bool) {
	var in struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return uuid.Nil, "Could not read the input.", false
	}
	id, err := uuid.Parse(strings.TrimSpace(in.EventID))
	if err != nil {
		return uuid.Nil, "That does not look like an event id.", false
	}
	return id, "", true
}

// userError converts the expected failures into plain sentences the agent can
// relay, and lets anything unexpected propagate as a real error.
func userError(err error) (string, error) {
	switch {
	case errors.Is(err, ErrNotFound):
		return "No such event.", nil
	case errors.Is(err, ErrInvalid):
		return strings.TrimPrefix(err.Error(), "invalid event: "), nil
	default:
		return "", err
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefSlice(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}

// coreClone copies an event. The rules -- draft status, a fresh slug, zeroed
// calendar state -- live in Service.Clone so the console cannot drift from the
// agent and MCP surfaces on any of them.
func coreClone(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	var in struct {
		EventID  string `json:"event_id"`
		StartsAt string `json:"starts_at"`
		Title    string `json:"title"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	id, err := uuid.Parse(strings.TrimSpace(in.EventID))
	if err != nil {
		return "That does not look like an event id.", nil
	}
	p := CloneParams{Title: in.Title, StartsAt: in.StartsAt}
	if caller.UserID != uuid.Nil {
		uid := caller.UserID
		p.CreatedBy = &uid
	}
	e, err := svc.Clone(ctx, caller.TenantID, id, p)
	if err != nil {
		return userError(err)
	}
	settings, _ := svc.Settings(ctx, caller.TenantID)
	return "Copied as a new draft — it is not visible anywhere yet, and it is independent of the original. " +
		"Publish it when confirmed.\n\n" + FormatEvent(e, settings), nil
}

// coreDelete erases a row for good. The safety rule lives in Service.Delete so
// the console, the agent and MCP cannot drift apart on it.
func coreDelete(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	id, msg, ok := parseEventID(raw)
	if !ok {
		return msg, nil
	}
	// Read the title before destroying the row, so the confirmation names what
	// went rather than echoing back a uuid.
	e, err := svc.Get(ctx, caller.TenantID, id)
	if err != nil {
		return userError(err)
	}
	title := e.Title
	if err := svc.Delete(ctx, caller.TenantID, id); err != nil {
		return userError(err)
	}
	return fmt.Sprintf("Deleted %q permanently.", title), nil
}

// coreSiteStatus and corePublishSite back the agent and MCP surfaces. Both
// render through FormatSiteStatus so every surface says the same thing.
func coreSiteStatus(ctx context.Context, caller *services.Caller, svc *Service) (string, error) {
	st, err := svc.SiteStatus(ctx, caller.TenantID)
	if err != nil {
		return userError(err)
	}
	return FormatSiteStatus(st), nil
}

func corePublishSite(ctx context.Context, caller *services.Caller, svc *Service) (string, error) {
	st, err := svc.PublishSite(ctx, caller.TenantID, "agent")
	if err != nil {
		return userError(err)
	}
	return "The website is rebuilding; it usually takes a minute or two.\n\n" + FormatSiteStatus(st), nil
}
