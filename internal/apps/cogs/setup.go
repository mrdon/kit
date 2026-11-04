package cogs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Setup is the console's configuration view.
type Setup struct {
	Vessels  []*Vessel `json:"vessels"`
	Beers    []*Beer   `json:"beers"`
	Formats  []*Format `json:"formats"`
	Settings Settings  `json:"settings"`
}

// Setup loads everything the setup screens edit, retired rows included.
func (s *Service) Setup(ctx context.Context, tenantID uuid.UUID) (*Setup, error) {
	if s == nil || s.pool == nil {
		return nil, ErrNotConfigured
	}
	out := &Setup{}
	var err error
	if out.Vessels, err = listVessels(ctx, s.pool, tenantID, true); err != nil {
		return nil, err
	}
	if out.Beers, err = listBeers(ctx, s.pool, tenantID); err != nil {
		return nil, err
	}
	if out.Formats, err = listFormats(ctx, s.pool, tenantID, true); err != nil {
		return nil, err
	}
	if out.Settings, err = loadSettings(ctx, s.pool, tenantID); err != nil {
		return nil, err
	}
	return out, nil
}

// SaveVessel creates (id nil) or edits a vessel's config. Its contents are
// never set here; they come from the ledger. Retiring takes the ledger lock
// so it can't race a fill.
func (s *Service) SaveVessel(ctx context.Context, tenantID uuid.UUID, id *uuid.UUID, in VesselInput) (uuid.UUID, error) {
	var out uuid.UUID
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		if id != nil && in.Retired {
			v, err := getVessel(ctx, tx, tenantID, *id)
			if err != nil {
				return err
			}
			if !v.Empty() {
				return fmt.Errorf("%w: %s still has beer in it", ErrInvalid, v.Name)
			}
		}
		var err error
		out, err = saveVessel(ctx, tx, tenantID, id, in)
		return err
	})
	return out, err
}

// BulkVessels is "FV-1 to FV-6, 10 bbl fermenters" in one go.
type BulkVessels struct {
	Prefix string `json:"prefix"`
	From   int    `json:"from"`
	To     int    `json:"to"`
	VesselInput
}

// AddVessels creates a numbered run of vessels in one transaction.
func (s *Service) AddVessels(ctx context.Context, tenantID uuid.UUID, in BulkVessels) (int, error) {
	if in.From < 0 || in.To < in.From || in.To-in.From > 50 {
		return 0, fmt.Errorf("%w: number from 1 up to at most 50 vessels at once", ErrInvalid)
	}
	prefix := strings.TrimSpace(in.Prefix)
	if prefix == "" {
		return 0, fmt.Errorf("%w: a name prefix like FV-", ErrInvalid)
	}
	n := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		for i := in.From; i <= in.To; i++ {
			v := in.VesselInput
			v.Name = fmt.Sprintf("%s%d", prefix, i)
			v.Sort = i
			if _, err := saveVessel(ctx, tx, tenantID, nil, v); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// SaveBeer creates (id nil) or edits a beer. Saving clears needs_setup:
// someone has now looked at it.
func (s *Service) SaveBeer(ctx context.Context, tenantID uuid.UUID, id *uuid.UUID, b Beer) (uuid.UUID, error) {
	b.NeedsSetup = false
	return saveBeer(ctx, s.pool, tenantID, id, b)
}

// AddBeers creates beers from a pasted list of names, skipping any that
// exist, with the default durations.
func (s *Service) AddBeers(ctx context.Context, tenantID uuid.UUID, namesList []string) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		for _, name := range namesList {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			_, err := findBeerByName(ctx, tx, tenantID, name)
			if err == nil {
				continue
			}
			if !errors.Is(err, ErrNotFound) {
				return err
			}
			if _, err := saveBeer(ctx, tx, tenantID, nil, NewBeerDefaults(name)); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// SaveFormat creates (id nil) or edits a package format. A format with
// stock on the shelf can't be retired (the count would expect a row nobody
// can see) or resized (every lot of it would change volume after the fact).
func (s *Service) SaveFormat(ctx context.Context, tenantID uuid.UUID, id *uuid.UUID, in FormatInput) (uuid.UUID, error) {
	var out uuid.UUID
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		if id != nil {
			if err := checkFormatEdit(ctx, tx, tenantID, *id, in); err != nil {
				return err
			}
		}
		var err error
		out, err = saveFormat(ctx, tx, tenantID, id, in)
		return err
	})
	return out, err
}

func checkFormatEdit(ctx context.Context, q querier, tenantID, id uuid.UUID, in FormatInput) error {
	formats, err := listFormats(ctx, q, tenantID, true)
	if err != nil {
		return err
	}
	var cur *Format
	for _, f := range formats {
		if f.ID == id {
			cur = f
		}
	}
	if cur == nil {
		return fmt.Errorf("format: %w", ErrNotFound)
	}
	resized := math.Abs(cur.BBLPerUnit-in.BBLPerUnit) > 1e-9
	if !in.Retired && !resized {
		return nil
	}
	lots, err := listLots(ctx, q, tenantID, nil, &id)
	if err != nil {
		return err
	}
	if len(lots) == 0 {
		return nil
	}
	if resized {
		return fmt.Errorf("%w: %s has been packaged, so its size is fixed. Add a new format instead", ErrInvalid, cur.Name)
	}
	for _, l := range lots {
		if math.Abs(l.Units) > 0.001 {
			return fmt.Errorf("%w: there's still %s on the shelf. Count it out first", ErrInvalid, cur.Name)
		}
	}
	return nil
}

// SaveSettings stores the tenant's globals.
func (s *Service) SaveSettings(ctx context.Context, tenantID uuid.UUID, in Settings) error {
	return saveSettings(ctx, s.pool, tenantID, in)
}
