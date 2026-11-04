package cogs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// Service is the COGS app's write path and read model. The iPad, the
// console and the agent tools all come through here.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds the service over a pool.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// maxBackdateDays is how far back the brewer can log from the iPad. The
// console can go further: corrections are its job.
const maxBackdateDays = 7

// ActionInput is one press of Save, from any form. Kind picks the action;
// each action reads the fields it needs and ignores the rest.
type ActionInput struct {
	Kind string `json:"kind"`
	// Date is YYYY-MM-DD in the brewery's time zone; empty is now.
	Date string `json:"date"`

	VesselID   *uuid.UUID  `json:"vessel_id"`
	ToVesselID *uuid.UUID  `json:"to_vessel_id"`
	ToVessels  []uuid.UUID `json:"to_vessel_ids"`

	BeerID      *uuid.UUID `json:"beer_id"`
	NewBeerName string     `json:"new_beer_name"`
	BatchID     *uuid.UUID `json:"batch_id"`

	VolumeBBL   float64 `json:"volume_bbl"`
	VolumeInBBL float64 `json:"volume_in_bbl"`
	Gallons     float64 `json:"gallons"`
	Reading     float64 `json:"reading_bbl"`
	All         bool    `json:"all"`
	Empty       bool    `json:"empty"`

	Lines         []LineInput `json:"lines"`
	LineLossUnits float64     `json:"line_loss_units"`
	Take          *LineInput  `json:"take"`
	// KeepRest, on a repack, puts what wasn't used back on the shelf as a
	// partial unit of what was taken (a ½ with a sixtel pulled out of it).
	KeepRest bool `json:"keep_rest"`

	Reason      string     `json:"reason"`
	Note        string     `json:"note"`
	Added       string     `json:"added"`
	Stage       string     `json:"stage"`
	OnTap       bool       `json:"on_tap"`
	BrewedOn    string     `json:"brewed_on"`
	Source      string     `json:"source"`
	PhotoID     *uuid.UUID `json:"photo_id"`
	PlanEntryID *uuid.UUID `json:"plan_entry_id"`
}

// LineInput is a packaged quantity: kegs as full and partial, cans and
// bottles as cases plus loose units, or plain units.
type LineInput struct {
	BeerID   *uuid.UUID `json:"beer_id"`
	FormatID uuid.UUID  `json:"format_id"`
	BatchID  *uuid.UUID `json:"batch_id"`
	Units    float64    `json:"units"`
	Full     float64    `json:"full"`
	Partial  float64    `json:"partial"`
	Cases    float64    `json:"cases"`
	Loose    float64    `json:"loose"`
	None     bool       `json:"none"`
}

// ActionResult is what Save shows: the line that now heads the history,
// and whether it can be undone from where the brewer is standing.
type ActionResult struct {
	ActionID uuid.UUID `json:"action_id"`
	Message  string    `json:"message"`
}

// actx is the state one action builds up inside its transaction.
type actx struct {
	ctx      context.Context
	tx       pgx.Tx
	tenantID uuid.UUID
	caller   *services.Caller
	rec      recorder
	actionID uuid.UUID
	at       time.Time
	loc      *time.Location
	settings Settings
	events   []Event
	touched  map[uuid.UUID]*Vessel
	formats  map[uuid.UUID]*Format
}

// emit appends an event to this action.
func (a *actx) emit(ev Event) (*Event, error) {
	ev.ActionID = a.actionID
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = a.at
	}
	if err := insertEvent(a.ctx, a.tx, a.tenantID, a.rec, &ev); err != nil {
		return nil, err
	}
	a.events = append(a.events, ev)
	return &a.events[len(a.events)-1], nil
}

// vessel loads a vessel once per action and remembers it for the replay.
func (a *actx) vessel(id *uuid.UUID, role string) (*Vessel, error) {
	if id == nil {
		return nil, fmt.Errorf("%w: pick the %s", ErrInvalid, role)
	}
	if v, ok := a.touched[*id]; ok {
		return v, nil
	}
	v, err := getVessel(a.ctx, a.tx, a.tenantID, *id)
	if err != nil {
		return nil, err
	}
	// The action works on the vessel as it was when the entry is dated;
	// the cache is today's. They differ only for a backdated entry.
	if v.VesselState, err = vesselStateAt(a.ctx, a.tx, a.tenantID, v, a.at); err != nil {
		return nil, err
	}
	a.touched[*id] = v
	return v, nil
}

func (a *actx) format(id uuid.UUID) (*Format, error) {
	if f, ok := a.formats[id]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("format: %w", ErrNotFound)
}

// units turns a line into a number of units of its format.
func (a *actx) units(l LineInput) (float64, *Format, error) {
	f, err := a.format(l.FormatID)
	if err != nil {
		return 0, nil, err
	}
	if l.None {
		return 0, f, nil
	}
	u := l.Units
	if f.Kind == FormatKeg {
		u += l.Full + l.Partial*a.settings.PartialFullness
	} else {
		u += l.Cases*float64(f.UnitsPerCase) + l.Loose
	}
	if u < 0 {
		return 0, nil, fmt.Errorf("%w: counts can't be negative", ErrInvalid)
	}
	return u, f, nil
}

// Record performs one action and returns its history line.
func (s *Service) Record(ctx context.Context, caller *services.Caller, in ActionInput) (*ActionResult, error) {
	if s == nil || s.pool == nil {
		return nil, ErrNotConfigured
	}
	run, ok := actionRunners[in.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: unknown action %q", ErrInvalid, in.Kind)
	}
	loc := s.location(ctx, caller)
	at, err := occurredAt(in.Date, loc, time.Now(), !caller.IsAdmin)
	if err != nil {
		return nil, err
	}
	var result *ActionResult
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := s.newActx(ctx, tx, caller, at, loc)
		if err != nil {
			return err
		}
		if err := run(a, in); err != nil {
			return err
		}
		if len(a.events) == 0 {
			return fmt.Errorf("%w: nothing to record", ErrInvalid)
		}
		for _, v := range a.touched {
			if err := refreshVessel(ctx, tx, a.tenantID, v); err != nil {
				return err
			}
		}
		msg, err := describeAction(ctx, tx, a.tenantID, a.events)
		if err != nil {
			return err
		}
		result = &ActionResult{ActionID: a.actionID, Message: msg}
		return nil
	})
	return result, err
}

func (s *Service) newActx(ctx context.Context, tx pgx.Tx, caller *services.Caller, at time.Time,
	loc *time.Location,
) (*actx, error) {
	if err := lockTenant(ctx, tx, caller.TenantID); err != nil {
		return nil, err
	}
	settings, err := loadSettings(ctx, tx, caller.TenantID)
	if err != nil {
		return nil, err
	}
	formats, err := listFormats(ctx, tx, caller.TenantID, true)
	if err != nil {
		return nil, err
	}
	fm := make(map[uuid.UUID]*Format, len(formats))
	for _, f := range formats {
		fm[f.ID] = f
	}
	rec, err := s.recorderFor(ctx, caller)
	if err != nil {
		return nil, err
	}
	return &actx{ctx: ctx, tx: tx, tenantID: caller.TenantID, caller: caller, rec: rec,
		actionID: uuid.New(), at: at, loc: loc, settings: settings,
		touched: map[uuid.UUID]*Vessel{}, formats: fm}, nil
}

func (s *Service) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning brewing transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing brewing transaction: %w", err)
	}
	return nil
}

// recorderFor names who did it: a device by its label, a person by name.
func (s *Service) recorderFor(ctx context.Context, c *services.Caller) (recorder, error) {
	switch {
	case c.Kind == services.CallerDevice:
		id := c.ActorID
		return recorder{Kind: string(services.CallerDevice), ID: &id, Label: c.Label}, nil
	case c.UserID != uuid.Nil:
		id := c.UserID
		label := c.Label
		if u, err := models.GetUserByID(ctx, s.pool, c.TenantID, c.UserID); err == nil && u != nil &&
			u.DisplayName != nil {
			label = *u.DisplayName
		}
		kind := string(c.Kind)
		if kind == "" {
			kind = string(services.CallerUser)
		}
		return recorder{Kind: kind, ID: &id, Label: label}, nil
	default:
		return recorder{Kind: string(c.Kind), Label: c.Label}, nil
	}
}

// occurredAt turns a picked day into a timestamp. Today is now; any other
// day is stamped at noon local time, so it can't slide across midnight in
// some other zone (COG had that bug).
func occurredAt(date string, loc *time.Location, now time.Time, limited bool) (time.Time, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return now, nil
	}
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: date %q is not YYYY-MM-DD", ErrInvalid, date)
	}
	today := dayOf(now, loc)
	switch {
	case d.Equal(today):
		return now, nil
	case d.After(today):
		return time.Time{}, fmt.Errorf("%w: that date hasn't happened yet", ErrInvalid)
	case limited && today.Sub(d) > maxBackdateDays*24*time.Hour+time.Hour:
		return time.Time{}, fmt.Errorf("%w: entries older than %d days are fixed from the console", ErrInvalid,
			maxBackdateDays)
	}
	// Built on the calendar day, not midnight plus 12h: on a DST change
	// that sum lands at 11am or 1pm.
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc), nil
}

// day is the action's date in the brewery's zone, as YYYY-MM-DD.
func (a *actx) day() string { return a.at.In(a.loc).Format("2006-01-02") }

// dateKey formats a DATE column. pgx reads one as midnight UTC, so it is
// formatted in UTC, never converted.
func dateKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// dayOf is local midnight of t.
func dayOf(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

type actionRunner func(a *actx, in ActionInput) error

var actionRunners map[string]actionRunner

func init() {
	actionRunners = map[string]actionRunner{
		EvBrew:         runBrew,
		EvDryHop:       runSimple(EvDryHop),
		EvClean:        runSimple(EvClean),
		EvOnTap:        runSimple(EvOnTap),
		EvTransfer:     runTransfer,
		"barrel_fill":  runBarrelFill,
		EvPackage:      runPackage,
		EvLoss:         runLoss,
		EvLevel:        runLevel,
		EvKick:         runKick,
		EvVariant:      runVariant,
		EvRepack:       runRepack,
		EvOutNotSold:   runOutNotSold,
		EvCount:        runCount,
		EvLoadVessel:   runLoadVessel,
		EvLoadPackaged: runLoadPackaged,
	}
}

// location is the brewery's time zone: a day on the brewer's screens is a
// day in the brewhouse, whoever is looking. The workspace's zone, falling
// back to the caller's when it has none.
func (s *Service) location(ctx context.Context, caller *services.Caller) *time.Location {
	var tz string
	// tenants is the one table keyed by its own id.
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(timezone, '') FROM tenants WHERE id = $1`,
		caller.TenantID).Scan(&tz); err == nil && tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return caller.Location()
}
