package cogs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is what both the pool and a transaction offer.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const vesselCols = `id, name, kind, area, size_bbl::float8, sort, retired_at,
	batch_id, volume_bbl::float8, stage, stage_at, filled_at, dirty, on_tap`

func scanVessel(row pgx.Row) (*Vessel, error) {
	var v Vessel
	err := row.Scan(&v.ID, &v.Name, &v.Kind, &v.Area, &v.SizeBBL, &v.Sort, &v.RetiredAt,
		&v.BatchID, &v.Volume, &v.Stage, &v.StageAt, &v.FilledAt, &v.Dirty, &v.OnTap)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// listVessels returns the tenant's vessels in display order, retired ones
// only when asked.
func listVessels(ctx context.Context, q querier, tenantID uuid.UUID, withRetired bool) ([]*Vessel, error) {
	rows, err := q.Query(ctx, `SELECT `+vesselCols+` FROM app_cogs_vessels
		WHERE tenant_id = $1 AND ($2 OR retired_at IS NULL)
		ORDER BY CASE area WHEN 'brewhouse' THEN 0 WHEN 'chiller' THEN 1 ELSE 2 END,
		CASE kind WHEN 'fermenter' THEN 0 WHEN 'brite' THEN 1 WHEN 'serving' THEN 2 ELSE 3 END, sort, name`,
		tenantID, withRetired)
	if err != nil {
		return nil, fmt.Errorf("listing vessels: %w", err)
	}
	defer rows.Close()
	out := []*Vessel{}
	for rows.Next() {
		v, err := scanVessel(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning vessel: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func getVessel(ctx context.Context, q querier, tenantID, id uuid.UUID) (*Vessel, error) {
	v, err := scanVessel(q.QueryRow(ctx, `SELECT `+vesselCols+` FROM app_cogs_vessels
		WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("vessel: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("loading vessel: %w", err)
	}
	return v, nil
}

// VesselInput is the editable config of a vessel.
type VesselInput struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Area    string  `json:"area"`
	SizeBBL float64 `json:"size_bbl"`
	Sort    int     `json:"sort"`
	Retired bool    `json:"retired"`
}

func (in *VesselInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return fmt.Errorf("%w: a vessel needs a name", ErrInvalid)
	}
	switch in.Kind {
	case KindFermenter, KindBrite, KindServing, KindBarrel:
	default:
		return fmt.Errorf("%w: unknown vessel kind %q", ErrInvalid, in.Kind)
	}
	if in.Area == "" {
		in.Area = defaultArea(in.Kind)
	}
	switch in.Area {
	case AreaBrewhouse, AreaChiller, AreaBarrels:
	default:
		return fmt.Errorf("%w: unknown area %q", ErrInvalid, in.Area)
	}
	if in.SizeBBL <= 0 {
		return fmt.Errorf("%w: %s needs a size", ErrInvalid, in.Name)
	}
	return nil
}

func defaultArea(kind string) string {
	switch kind {
	case KindServing:
		return AreaChiller
	case KindBarrel:
		return AreaBarrels
	default:
		return AreaBrewhouse
	}
}

func saveVessel(ctx context.Context, q querier, tenantID uuid.UUID, id *uuid.UUID, in VesselInput) (uuid.UUID, error) {
	if err := in.validate(); err != nil {
		return uuid.Nil, err
	}
	var out uuid.UUID
	var err error
	if id == nil {
		err = q.QueryRow(ctx, `INSERT INTO app_cogs_vessels (tenant_id, name, kind, area, size_bbl, sort)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			tenantID, in.Name, in.Kind, in.Area, in.SizeBBL, in.Sort).Scan(&out)
	} else {
		err = q.QueryRow(ctx, `UPDATE app_cogs_vessels SET name = $3, kind = $4, area = $5, size_bbl = $6,
			sort = $7, retired_at = CASE WHEN $8 THEN COALESCE(retired_at, NOW()) ELSE NULL END
			WHERE tenant_id = $1 AND id = $2 RETURNING id`,
			tenantID, *id, in.Name, in.Kind, in.Area, in.SizeBBL, in.Sort, in.Retired).Scan(&out)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("vessel: %w", ErrNotFound)
	}
	if isUniqueViolation(err) {
		return uuid.Nil, fmt.Errorf("%w: there is already a vessel called %s", ErrInvalid, in.Name)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("saving vessel: %w", err)
	}
	return out, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

const beerCols = `id, name, code, aliases, family, style, active, staple, needs_setup,
	ferment_days, dry_hop, dry_hop_days, condition_days, brite_days, uses_brite,
	keg_shelf_weeks::float8, pkg_shelf_weeks::float8`

func scanBeer(row pgx.Row) (*Beer, error) {
	var b Beer
	err := row.Scan(&b.ID, &b.Name, &b.Code, &b.Aliases, &b.Family, &b.Style, &b.Active, &b.Staple,
		&b.NeedsSetup, &b.FermentDays, &b.DryHop, &b.DryHopDays, &b.ConditionDays, &b.BriteDays,
		&b.UsesBrite, &b.KegShelfWeeks, &b.PkgShelfWeeks)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func listBeers(ctx context.Context, q querier, tenantID uuid.UUID) ([]*Beer, error) {
	rows, err := q.Query(ctx, `SELECT `+beerCols+` FROM app_cogs_beers
		WHERE tenant_id = $1 ORDER BY lower(name)`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing beers: %w", err)
	}
	defer rows.Close()
	out := []*Beer{}
	for rows.Next() {
		b, err := scanBeer(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning beer: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func getBeer(ctx context.Context, q querier, tenantID, id uuid.UUID) (*Beer, error) {
	b, err := scanBeer(q.QueryRow(ctx, `SELECT `+beerCols+` FROM app_cogs_beers
		WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("beer: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("loading beer: %w", err)
	}
	return b, nil
}

// saveBeer inserts (id nil) or updates a beer.
func saveBeer(ctx context.Context, q querier, tenantID uuid.UUID, id *uuid.UUID, b Beer) (uuid.UUID, error) {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return uuid.Nil, fmt.Errorf("%w: a beer needs a name", ErrInvalid)
	}
	if strings.TrimSpace(b.Code) == "" {
		b.Code = beerCode(b.Name)
	}
	if b.Aliases == nil {
		b.Aliases = []string{}
	}
	args := []any{tenantID, b.Name, strings.ToUpper(strings.TrimSpace(b.Code)), b.Aliases, b.Family, b.Style,
		b.Active, b.Staple, b.NeedsSetup, b.FermentDays, b.DryHop, b.DryHopDays, b.ConditionDays,
		b.BriteDays, b.UsesBrite, b.KegShelfWeeks, b.PkgShelfWeeks}
	var out uuid.UUID
	var err error
	if id == nil {
		err = q.QueryRow(ctx, `INSERT INTO app_cogs_beers (tenant_id, name, code, aliases, family, style,
			active, staple, needs_setup, ferment_days, dry_hop, dry_hop_days, condition_days, brite_days,
			uses_brite, keg_shelf_weeks, pkg_shelf_weeks)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`, args...).Scan(&out)
	} else {
		args = append(args, *id)
		err = q.QueryRow(ctx, `UPDATE app_cogs_beers SET name=$2, code=$3, aliases=$4, family=$5, style=$6,
			active=$7, staple=$8, needs_setup=$9, ferment_days=$10, dry_hop=$11, dry_hop_days=$12,
			condition_days=$13, brite_days=$14, uses_brite=$15, keg_shelf_weeks=$16, pkg_shelf_weeks=$17,
			updated_at=NOW() WHERE tenant_id=$1 AND id=$18 RETURNING id`, args...).Scan(&out)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("beer: %w", ErrNotFound)
	}
	if isUniqueViolation(err) {
		return uuid.Nil, fmt.Errorf("%w: there is already a beer called %s", ErrInvalid, b.Name)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("saving beer: %w", err)
	}
	return out, nil
}

// NewBeerDefaults is a new beer before anyone has set it up: the
// production durations of a typical ale.
func NewBeerDefaults(name string) Beer {
	return Beer{Name: name, Active: true, FermentDays: 14, DryHopDays: 4, BriteDays: 3, UsesBrite: true,
		KegShelfWeeks: 12, PkgShelfWeeks: 16}
}

// beerCode makes a batch-code prefix from a name: initials of up to three
// words ("Light Lift Lager" is LLL), or the first two letters of one.
func beerCode(name string) string {
	words := strings.Fields(name)
	if len(words) == 1 {
		r := []rune(strings.ToUpper(words[0]))
		if len(r) > 2 {
			r = r[:2]
		}
		return string(r)
	}
	var b strings.Builder
	for i, w := range words {
		if i == 3 {
			break
		}
		b.WriteRune([]rune(strings.ToUpper(w))[0])
	}
	return b.String()
}

// findBeerByName matches a name or alias, ignoring case and spacing.
// ErrNotFound when nothing matches.
func findBeerByName(ctx context.Context, q querier, tenantID uuid.UUID, name string) (*Beer, error) {
	key := strings.Join(strings.Fields(strings.ToLower(name)), " ")
	b, err := scanBeer(q.QueryRow(ctx, `SELECT `+beerCols+` FROM app_cogs_beers
		WHERE tenant_id = $1 AND (lower(name) = $2 OR $2 = ANY (SELECT lower(a) FROM unnest(aliases) a))
		LIMIT 1`, tenantID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("beer %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("finding beer: %w", err)
	}
	return b, nil
}

const formatCols = `id, name, kind, bbl_per_unit::float8, units_per_case, returnable, sort, retired_at`

func listFormats(ctx context.Context, q querier, tenantID uuid.UUID, withRetired bool) ([]*Format, error) {
	rows, err := q.Query(ctx, `SELECT `+formatCols+` FROM app_cogs_package_formats
		WHERE tenant_id = $1 AND ($2 OR retired_at IS NULL)
		ORDER BY CASE kind WHEN 'keg' THEN 0 WHEN 'can' THEN 1 ELSE 2 END, sort, bbl_per_unit DESC`,
		tenantID, withRetired)
	if err != nil {
		return nil, fmt.Errorf("listing formats: %w", err)
	}
	defer rows.Close()
	out := []*Format{}
	for rows.Next() {
		var f Format
		if err := rows.Scan(&f.ID, &f.Name, &f.Kind, &f.BBLPerUnit, &f.UnitsPerCase, &f.Returnable,
			&f.Sort, &f.RetiredAt); err != nil {
			return nil, fmt.Errorf("scanning format: %w", err)
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}

// FormatInput is the editable config of a package format.
type FormatInput struct {
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	BBLPerUnit   float64 `json:"bbl_per_unit"`
	UnitsPerCase int     `json:"units_per_case"`
	Returnable   bool    `json:"returnable"`
	Sort         int     `json:"sort"`
	Retired      bool    `json:"retired"`
}

func saveFormat(ctx context.Context, q querier, tenantID uuid.UUID, id *uuid.UUID, in FormatInput) (uuid.UUID, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.BBLPerUnit <= 0 {
		return uuid.Nil, fmt.Errorf("%w: a format needs a name and a size", ErrInvalid)
	}
	switch in.Kind {
	case FormatKeg:
		in.UnitsPerCase = 0
	case FormatCan, FormatBottle:
		if in.UnitsPerCase <= 0 {
			return uuid.Nil, fmt.Errorf("%w: %s needs units per case", ErrInvalid, in.Name)
		}
	default:
		return uuid.Nil, fmt.Errorf("%w: unknown format kind %q", ErrInvalid, in.Kind)
	}
	var out uuid.UUID
	var err error
	if id == nil {
		err = q.QueryRow(ctx, `INSERT INTO app_cogs_package_formats (tenant_id, name, kind, bbl_per_unit,
			units_per_case, returnable, sort) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			tenantID, in.Name, in.Kind, in.BBLPerUnit, in.UnitsPerCase, in.Returnable, in.Sort).Scan(&out)
	} else {
		err = q.QueryRow(ctx, `UPDATE app_cogs_package_formats SET name=$3, kind=$4, bbl_per_unit=$5,
			units_per_case=$6, returnable=$7, sort=$8,
			retired_at = CASE WHEN $9 THEN COALESCE(retired_at, NOW()) ELSE NULL END
			WHERE tenant_id=$1 AND id=$2 RETURNING id`,
			tenantID, *id, in.Name, in.Kind, in.BBLPerUnit, in.UnitsPerCase, in.Returnable, in.Sort,
			in.Retired).Scan(&out)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("format: %w", ErrNotFound)
	}
	if isUniqueViolation(err) {
		return uuid.Nil, fmt.Errorf("%w: there is already a format called %s", ErrInvalid, in.Name)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("saving format: %w", err)
	}
	return out, nil
}

func loadSettings(ctx context.Context, q querier, tenantID uuid.UUID) (Settings, error) {
	s := DefaultSettings()
	err := q.QueryRow(ctx, `SELECT partial_fullness::float8, barrel_bbl::float8, brewhouse_bbl::float8,
		count_interval_days FROM app_cogs_settings WHERE tenant_id = $1`, tenantID).
		Scan(&s.PartialFullness, &s.BarrelBBL, &s.BrewhouseBBL, &s.CountIntervalDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return s, fmt.Errorf("loading brewing settings: %w", err)
	}
	return s, nil
}

func saveSettings(ctx context.Context, q querier, tenantID uuid.UUID, s Settings) error {
	if s.PartialFullness <= 0 || s.PartialFullness > 1 || s.BarrelBBL <= 0 || s.BrewhouseBBL <= 0 ||
		s.CountIntervalDays < 1 {
		return fmt.Errorf("%w: settings out of range", ErrInvalid)
	}
	_, err := q.Exec(ctx, `INSERT INTO app_cogs_settings (tenant_id, partial_fullness, barrel_bbl,
		brewhouse_bbl, count_interval_days) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (tenant_id) DO UPDATE SET partial_fullness=$2, barrel_bbl=$3, brewhouse_bbl=$4,
		count_interval_days=$5, updated_at=NOW()`,
		tenantID, s.PartialFullness, s.BarrelBBL, s.BrewhouseBBL, s.CountIntervalDays)
	if err != nil {
		return fmt.Errorf("saving brewing settings: %w", err)
	}
	return nil
}
