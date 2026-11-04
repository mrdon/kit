package cogs

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Vessel kinds and areas. Config, not constants of any one brewery: these
// are the shapes a vessel can take, and the tenant's rows say which it has.
const (
	KindFermenter = "fermenter"
	KindBrite     = "brite"
	KindServing   = "serving"
	KindBarrel    = "barrel"

	AreaBrewhouse = "brewhouse"
	AreaChiller   = "chiller"
	AreaBarrels   = "barrels"
)

// Package format kinds.
const (
	FormatKeg    = "keg"
	FormatCan    = "can"
	FormatBottle = "bottle"
)

// Event kinds: the ledger's vocabulary. See the spec's "Event kinds" table.
const (
	EvBrew         = "brew"
	EvDryHop       = "dry_hop"
	EvTransfer     = "transfer"
	EvPackage      = "package"
	EvEmpty        = "empty"
	EvLoss         = "loss"
	EvDump         = "dump"
	EvLevel        = "level"
	EvKick         = "kick"
	EvOnTap        = "on_tap"
	EvClean        = "clean"
	EvVariant      = "variant"
	EvBlend        = "blend"
	EvRepack       = "repack"
	EvOutNotSold   = "out_not_sold"
	EvCount        = "count"
	EvLoadVessel   = "load_vessel"
	EvLoadPackaged = "load_packaged"
	EvReversal     = "reversal"
)

// Stage codes held in the vessel cache. What the brewer reads ("Waiting to
// dry hop · day 9") is worked out from these plus the beer's durations.
const (
	StageFermenting   = "fermenting"
	StageDryHopped    = "dry_hopped"
	StageConditioning = "conditioning"
	StageBrite        = "brite"
	StageWaiting      = "waiting"
	StagePouring      = "pouring"
	StageAging        = "aging"
)

// eps is the volume below which a vessel counts as empty: three decimal
// places of a barrel is about half a cup.
const eps = 0.005

var (
	ErrNotFound      = errors.New("not found")
	ErrInvalid       = errors.New("invalid")
	ErrOverdraw      = errors.New("more than the vessel holds")
	ErrBlend         = errors.New("different beer in the destination")
	ErrNotUndoable   = errors.New("not undoable")
	ErrConflict      = errors.New("the entries don't add up")
	ErrNotConfigured = errors.New("cogs app is not configured")
)

// Vessel is a tank or barrel: its config and its cached state.
type Vessel struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Area      string     `json:"area"`
	SizeBBL   float64    `json:"size_bbl"`
	Sort      int        `json:"sort"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
	VesselState
}

// VesselState is what the replay produces and the cache holds.
type VesselState struct {
	BatchID  *uuid.UUID `json:"batch_id"`
	Volume   float64    `json:"volume_bbl"`
	Stage    string     `json:"stage"`
	StageAt  *time.Time `json:"stage_at"`
	FilledAt *time.Time `json:"filled_at"`
	Dirty    bool       `json:"dirty"`
	OnTap    bool       `json:"on_tap"`
}

// Empty reports whether nothing is in the vessel.
func (s VesselState) Empty() bool { return s.BatchID == nil && s.Volume < eps }

// Beer is a catalog entry with its production settings.
type Beer struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Code          string    `json:"code"`
	Aliases       []string  `json:"aliases"`
	Family        string    `json:"family"`
	Style         string    `json:"style"`
	Active        bool      `json:"active"`
	Staple        bool      `json:"staple"`
	NeedsSetup    bool      `json:"needs_setup"`
	FermentDays   int       `json:"ferment_days"`
	DryHop        bool      `json:"dry_hop"`
	DryHopDays    int       `json:"dry_hop_days"`
	ConditionDays int       `json:"condition_days"`
	BriteDays     int       `json:"brite_days"`
	UsesBrite     bool      `json:"uses_brite"`
	KegShelfWeeks float64   `json:"keg_shelf_weeks"`
	PkgShelfWeeks float64   `json:"pkg_shelf_weeks"`
}

// Format is a package format: a keg size, a can, a bottle.
type Format struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	BBLPerUnit   float64    `json:"bbl_per_unit"`
	UnitsPerCase int        `json:"units_per_case"`
	Returnable   bool       `json:"returnable"`
	Sort         int        `json:"sort"`
	RetiredAt    *time.Time `json:"retired_at,omitempty"`
}

// Settings are the tenant's globals.
type Settings struct {
	PartialFullness   float64 `json:"partial_fullness"`
	BarrelBBL         float64 `json:"barrel_bbl"`
	BrewhouseBBL      float64 `json:"brewhouse_bbl"`
	CountIntervalDays int     `json:"count_interval_days"`
}

// DefaultSettings is what a tenant has before anyone saves.
func DefaultSettings() Settings {
	return Settings{PartialFullness: 0.5, BarrelBBL: 1.7, BrewhouseBBL: 10, CountIntervalDays: 7}
}

// Batch is one brew (or a double brew into one vessel), or a derived batch
// for a variant.
type Batch struct {
	ID            uuid.UUID  `json:"id"`
	BeerID        uuid.UUID  `json:"beer_id"`
	Number        int        `json:"number"`
	BrewedOn      time.Time  `json:"brewed_on"`
	Source        string     `json:"source"`
	ParentBatchID *uuid.UUID `json:"parent_batch_id"`
	Note          string     `json:"note"`
}

// Event is one ledger row.
type Event struct {
	ID               uuid.UUID
	Seq              int64
	ActionID         uuid.UUID
	Kind             string
	OccurredAt       time.Time
	BatchID          *uuid.UUID
	VesselID         *uuid.UUID
	ToVesselID       *uuid.UUID
	VolumeBBL        float64
	VolumeInBBL      float64
	LossBBL          float64
	Details          EventDetails
	RecordedByKind   string
	RecordedByID     *uuid.UUID
	RecordedLabel    string
	ReversesActionID *uuid.UUID
	CreatedAt        time.Time
}

// EventDetails is the free-form part of an event. Everything the history
// line or a later report needs that isn't a column goes here.
type EventDetails struct {
	Stage         string       `json:"stage,omitempty"`
	OnTap         bool         `json:"on_tap,omitempty"`
	Note          string       `json:"note,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	Lines         []DetailLine `json:"lines,omitempty"`
	Took          *DetailLine  `json:"took,omitempty"`
	LineLossUnits float64      `json:"line_loss_units,omitempty"`
	Gallons       float64      `json:"gallons,omitempty"`
	Reading       *float64     `json:"reading,omitempty"`
	PhotoID       string       `json:"photo_id,omitempty"`
	PlanEntryID   string       `json:"plan_entry_id,omitempty"`
	Added         string       `json:"added,omitempty"`
	// Left is what the vessel held when it was emptied (or kicked, or
	// dumped). The replay checks it still is.
	Left *float64 `json:"left,omitempty"`
	// Unaccounted is what a kicked serving tank still had on the books.
	// It is not a loss: until sales deplete the tank, nobody knows how much
	// was poured and how much was lost.
	Unaccounted float64 `json:"unaccounted,omitempty"`
	// Parents is a blend's or a multi-batch repack's source batches.
	Parents []BatchShare `json:"parents,omitempty"`
}

// BatchShare is one source batch of a blend and how much of it went in.
type BatchShare struct {
	BatchID uuid.UUID `json:"batch_id"`
	BBL     float64   `json:"bbl"`
}

// DetailLine is a packaged quantity as recorded: beer, format and units,
// with the names kept so a history line still reads if a format is renamed.
type DetailLine struct {
	BeerID     uuid.UUID `json:"beer_id"`
	BeerName   string    `json:"beer_name"`
	FormatID   uuid.UUID `json:"format_id"`
	FormatName string    `json:"format_name"`
	Units      float64   `json:"units"`
	BBL        float64   `json:"bbl"`
}
