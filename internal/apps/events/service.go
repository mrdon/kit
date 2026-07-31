package events

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service holds the business rules. Every surface -- the console API, the
// agent tools, and the MCP tools -- goes through these methods, so behaviour
// cannot drift between them.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds a service over the pool.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// CreateParams is a new event. Zero values take documented defaults rather
// than being rejected, so a caller can create an event from a title and a
// start time alone.
type CreateParams struct {
	Title       string
	Summary     string
	Description string
	PrepNotes   string
	Location    string

	// StartsAt / EndsAt are raw strings so the caller need not resolve the
	// timezone first; they are parsed in the event's own zone.
	StartsAt string
	EndsAt   string
	AllDay   bool
	Timezone string
	RRule    string

	Visibility  Visibility
	Venue       Venue
	SpaceImpact SpaceImpact

	PriceCents         *int64
	Currency           string
	Capacity           *int
	ExpectedAttendance *int
	RegistrationURL    string

	NotifyFoodPartner *bool
	CreatedBy         *uuid.UUID
}

// Create inserts a draft event.
//
// Two defaults matter. Visibility is private unless the caller says otherwise
// -- publishing to the public web is an explicit act, never an accident of an
// omitted field. And notify_food_partner defaults to "public onsite event",
// which is the case where a crowd shows up at the taproom, while staying
// overridable because a private party may well want the truck.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, p CreateParams) (*Event, error) {
	settings, err := getSettings(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}

	e := &Event{
		TenantID:           tenantID,
		Title:              strings.TrimSpace(p.Title),
		Summary:            strings.TrimSpace(p.Summary),
		Description:        p.Description,
		PrepNotes:          p.PrepNotes,
		Location:           strings.TrimSpace(p.Location),
		AllDay:             p.AllDay,
		RRule:              strings.TrimSpace(p.RRule),
		Status:             StatusDraft,
		Visibility:         firstNonEmptyVisibility(p.Visibility, VisibilityPrivate),
		Venue:              firstNonEmptyVenue(p.Venue, VenueOnsite),
		SpaceImpact:        firstNonEmptySpace(p.SpaceImpact, SpaceImpactNone),
		PriceCents:         p.PriceCents,
		Currency:           strings.TrimSpace(p.Currency),
		Capacity:           p.Capacity,
		ExpectedAttendance: p.ExpectedAttendance,
		RegistrationURL:    strings.TrimSpace(p.RegistrationURL),
		CreatedBy:          p.CreatedBy,
	}
	if e.Currency == "" {
		e.Currency = "USD"
	}
	e.Timezone = strings.TrimSpace(p.Timezone)
	if e.Timezone == "" {
		e.Timezone = settings.Timezone
	}
	loc, err := ResolveTimezone(e.Timezone)
	if err != nil {
		return nil, err
	}
	if e.StartsAt, err = ParseTime(p.StartsAt, loc); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.EndsAt) != "" {
		end, err := ParseTime(p.EndsAt, loc)
		if err != nil {
			return nil, err
		}
		e.EndsAt = &end
	}

	if p.NotifyFoodPartner != nil {
		e.NotifyFoodPartner = *p.NotifyFoodPartner
	} else {
		e.NotifyFoodPartner = e.Visibility == VisibilityPublic && e.Venue == VenueOnsite
	}

	if err := validateEvent(e); err != nil {
		return nil, err
	}
	if e.Slug, err = UniqueSlug(ctx, s.pool, tenantID, e.Title, nil); err != nil {
		return nil, err
	}
	return insertEvent(ctx, s.pool, e)
}

// UpdateParams is a partial patch: a nil field is left alone. Pointers rather
// than sentinel values because "" and 0 are meaningful for several fields, and
// because the console form and the chat agent can edit the same event
// concurrently -- patching only what each touched keeps them from clobbering
// each other.
type UpdateParams struct {
	Title       *string
	Summary     *string
	Description *string
	PrepNotes   *string
	Location    *string

	StartsAt *string
	EndsAt   *string
	AllDay   *bool
	Timezone *string
	RRule    *string

	Visibility  *Visibility
	Venue       *Venue
	SpaceImpact *SpaceImpact

	PriceCents         *int64
	ClearPrice         bool
	Currency           *string
	Capacity           *int
	ClearCapacity      bool
	ExpectedAttendance *int
	RegistrationURL    *string

	NotifyFoodPartner *bool
	// HeroAttachmentID sets the poster. ClearHero removes it -- a nil pointer
	// means "leave alone", so removal needs its own flag.
	HeroAttachmentID *uuid.UUID
	ClearHero        bool
	// Slug is only honoured while the event is a draft; see below.
	Slug *string
}

// Update applies a partial patch.
//
// The slug is frozen once an event leaves draft. It is the public URL, which
// by then may already be in an Instagram post or a newsletter, and recycling
// or changing it silently breaks every link already in the wild.
func (s *Service) Update(ctx context.Context, tenantID, id uuid.UUID, p UpdateParams) (*Event, error) {
	e, err := getEvent(ctx, s.pool, tenantID, id)
	if err != nil {
		return nil, err
	}

	applyStrings(e, p)
	if p.AllDay != nil {
		e.AllDay = *p.AllDay
	}
	if p.Visibility != nil {
		e.Visibility = *p.Visibility
	}
	if p.Venue != nil {
		e.Venue = *p.Venue
	}
	if p.SpaceImpact != nil {
		e.SpaceImpact = *p.SpaceImpact
	}
	if p.ClearHero {
		e.HeroAttachmentID = nil
	} else if p.HeroAttachmentID != nil {
		e.HeroAttachmentID = p.HeroAttachmentID
	}
	if p.NotifyFoodPartner != nil {
		e.NotifyFoodPartner = *p.NotifyFoodPartner
	}
	if p.ClearPrice {
		e.PriceCents = nil
	} else if p.PriceCents != nil {
		e.PriceCents = p.PriceCents
	}
	if p.ClearCapacity {
		e.Capacity = nil
	} else if p.Capacity != nil {
		e.Capacity = p.Capacity
	}
	if p.ExpectedAttendance != nil {
		e.ExpectedAttendance = p.ExpectedAttendance
	}

	if err := applyTimes(e, p); err != nil {
		return nil, err
	}
	if err := applySlug(ctx, s.pool, e, p); err != nil {
		return nil, err
	}
	if err := validateEvent(e); err != nil {
		return nil, err
	}
	return updateEvent(ctx, s.pool, e)
}

func applyStrings(e *Event, p UpdateParams) {
	if p.Title != nil {
		e.Title = strings.TrimSpace(*p.Title)
	}
	if p.Summary != nil {
		e.Summary = strings.TrimSpace(*p.Summary)
	}
	if p.Description != nil {
		e.Description = *p.Description
	}
	if p.PrepNotes != nil {
		e.PrepNotes = *p.PrepNotes
	}
	if p.Location != nil {
		e.Location = strings.TrimSpace(*p.Location)
	}
	if p.RegistrationURL != nil {
		e.RegistrationURL = strings.TrimSpace(*p.RegistrationURL)
	}
	if p.Currency != nil {
		e.Currency = strings.TrimSpace(*p.Currency)
	}
	if p.RRule != nil {
		e.RRule = strings.TrimSpace(*p.RRule)
	}
}

// applyTimes re-parses start/end when either the times or the zone change.
//
// Changing the timezone preserves the WALL CLOCK, not the instant: a 7pm event
// moved from Denver to Chicago is still at 7pm. Preserving the instant instead
// would silently shift a recurring event's whole series by an hour, which is
// never what someone correcting a timezone meant.
func applyTimes(e *Event, p UpdateParams) error {
	if p.Timezone != nil {
		newTZ := strings.TrimSpace(*p.Timezone)
		newLoc, err := ResolveTimezone(newTZ)
		if err != nil {
			return err
		}
		if newTZ != e.Timezone {
			e.StartsAt = rezone(e.StartsAt, e.Loc(), newLoc)
			if e.EndsAt != nil {
				moved := rezone(*e.EndsAt, e.Loc(), newLoc)
				e.EndsAt = &moved
			}
			e.Timezone = newTZ
		}
	}
	loc := e.Loc()
	if p.StartsAt != nil {
		t, err := ParseTime(*p.StartsAt, loc)
		if err != nil {
			return err
		}
		e.StartsAt = t
	}
	if p.EndsAt != nil {
		if strings.TrimSpace(*p.EndsAt) == "" {
			e.EndsAt = nil
		} else {
			t, err := ParseTime(*p.EndsAt, loc)
			if err != nil {
				return err
			}
			e.EndsAt = &t
		}
	}
	return nil
}

// rezone rebuilds an instant so its wall-clock reading in `to` matches what it
// read in `from`.
func rezone(t time.Time, from, to *time.Location) time.Time {
	l := t.In(from)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, to)
}

func applySlug(ctx context.Context, pool *pgxpool.Pool, e *Event, p UpdateParams) error {
	if p.Slug == nil {
		return nil
	}
	if e.Status != StatusDraft {
		return invalid("the web address is fixed once an event is published, because links to it may already be shared")
	}
	slug, err := UniqueSlug(ctx, pool, e.TenantID, *p.Slug, &e.ID)
	if err != nil {
		return err
	}
	e.Slug = slug
	return nil
}

// Get loads one event by id.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (*Event, error) {
	return getEvent(ctx, s.pool, tenantID, id)
}

// GetBySlug loads one event by its public slug.
func (s *Service) GetBySlug(ctx context.Context, tenantID uuid.UUID, slug string) (*Event, error) {
	return getEventBySlug(ctx, s.pool, tenantID, slug)
}

// List returns events matching the filter.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, f ListFilter) ([]Event, error) {
	return listEvents(ctx, s.pool, tenantID, f)
}

// Settings returns the tenant's configuration, defaulted when unset.
func (s *Service) Settings(ctx context.Context, tenantID uuid.UUID) (Settings, error) {
	return getSettings(ctx, s.pool, tenantID)
}

// SaveSettings persists configuration, minting a feed token on first save so
// the website always has one to authenticate with.
func (s *Service) SaveSettings(ctx context.Context, in Settings) (Settings, error) {
	if _, err := ResolveTimezone(firstNonEmpty(in.Timezone, DefaultTimezone)); err != nil {
		return Settings{}, err
	}
	if in.FeedToken == "" {
		token, err := NewFeedToken()
		if err != nil {
			return Settings{}, err
		}
		in.FeedToken = token
	}
	return upsertSettings(ctx, s.pool, in)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func firstNonEmptyVisibility(a, fallback Visibility) Visibility {
	if a == "" {
		return fallback
	}
	return a
}

func firstNonEmptyVenue(a, fallback Venue) Venue {
	if a == "" {
		return fallback
	}
	return a
}

func firstNonEmptySpace(a, fallback SpaceImpact) SpaceImpact {
	if a == "" {
		return fallback
	}
	return a
}

// timeNow is the clock. A variable so tests can pin it if they ever need to;
// production never reassigns it.
var timeNow = time.Now
