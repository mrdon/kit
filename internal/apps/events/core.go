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
	case "publish_event":
		return corePublish(ctx, caller, svc, raw)
	case "unpublish_event":
		return coreSimpleTransition(ctx, caller, svc, raw, svc.Unpublish, "Moved back to draft")
	case "cancel_event":
		return coreSimpleTransition(ctx, caller, svc, raw, svc.Cancel, "Cancelled")
	case "reopen_event":
		return coreSimpleTransition(ctx, caller, svc, raw, svc.Reopen, "Reopened as a draft")
	case "list_events":
		return coreList(ctx, caller, svc, raw)
	case "get_event":
		return coreGet(ctx, caller, svc, raw)
	case "events_status":
		return coreStatus(ctx, caller, svc)
	case "events_sync_now":
		return coreSyncNow(ctx, caller)
	case "events_reconcile":
		return coreReconcile(ctx, caller, raw)
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
		Visibility:         Visibility(strings.ToLower(deref(in.Visibility))),
		Venue:              Venue(strings.ToLower(deref(in.Venue))),
		SpaceImpact:        SpaceImpact(strings.ToLower(deref(in.SpaceImpact))),
		PriceCents:         in.PriceCents,
		Currency:           deref(in.Currency),
		Capacity:           in.Capacity,
		ExpectedAttendance: in.ExpectedAttendance,
		RegistrationURL:    deref(in.RegistrationURL),
		NotifyFoodPartner:  in.NotifyFoodPartner,
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
	return "Created as a draft — it is not visible anywhere yet. Publish it when confirmed.\n\n" +
		FormatEvent(e, settings), nil
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
		PriceCents: in.PriceCents, ClearPrice: in.ClearPrice,
		Currency: in.Currency, Capacity: in.Capacity, ClearCapacity: in.ClearCapacity,
		ExpectedAttendance: in.ExpectedAttendance,
		RegistrationURL:    in.RegistrationURL,
		NotifyFoodPartner:  in.NotifyFoodPartner,
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
