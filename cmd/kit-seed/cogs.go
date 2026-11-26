package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/cogs"
	"github.com/mrdon/kit/internal/services"
)

// seedCogs sets up a small brewery and a couple of weeks of history,
// written through the same Service the iPad uses so the ledger and the
// vessel cache agree. Dates are relative to today so the board always
// looks current.
func seedCogs(ctx context.Context, pool *pgxpool.Pool, admin *services.Caller) error {
	svc := cogs.NewService(pool)
	setup, err := svc.Setup(ctx, admin.TenantID)
	if err != nil {
		return fmt.Errorf("loading setup: %w", err)
	}
	if len(setup.Vessels) > 0 {
		slog.Info("cogs already has vessels, skipping")
		return nil
	}
	vessels, err := seedCogsVessels(ctx, svc, admin)
	if err != nil {
		return err
	}
	formats, err := seedCogsFormats(ctx, svc, admin)
	if err != nil {
		return err
	}
	beers := map[string]uuid.UUID{}
	for _, name := range []string{"Galactic Orbit", "Moon Dust Lager", "Comet Tail Stout"} {
		id, err := svc.SaveBeer(ctx, admin.TenantID, nil, cogs.NewBeerDefaults(name))
		if err != nil {
			return fmt.Errorf("saving beer %s: %w", name, err)
		}
		beers[name] = id
	}
	if err := seedCogsHistory(ctx, svc, admin, vessels, formats, beers); err != nil {
		return err
	}
	slog.Info("seeded cogs", "vessels", len(vessels), "beers", len(beers))
	return nil
}

func seedCogsVessels(ctx context.Context, svc *cogs.Service, admin *services.Caller) (map[string]uuid.UUID, error) {
	ids := map[string]uuid.UUID{}
	for _, v := range []cogs.VesselInput{
		{Name: "FV-1", Kind: cogs.KindFermenter, SizeBBL: 10},
		{Name: "FV-2", Kind: cogs.KindFermenter, SizeBBL: 10},
		{Name: "FV-3", Kind: cogs.KindFermenter, SizeBBL: 20},
		{Name: "BT-1", Kind: cogs.KindBrite, SizeBBL: 10},
		{Name: "SV-1", Kind: cogs.KindServing, SizeBBL: 10},
		{Name: "B-1", Kind: cogs.KindBarrel, SizeBBL: 1.7},
	} {
		id, err := svc.SaveVessel(ctx, admin.TenantID, nil, v)
		if err != nil {
			return nil, fmt.Errorf("saving vessel %s: %w", v.Name, err)
		}
		ids[v.Name] = id
	}
	return ids, nil
}

func seedCogsFormats(ctx context.Context, svc *cogs.Service, admin *services.Caller) (map[string]uuid.UUID, error) {
	ids := map[string]uuid.UUID{}
	for _, f := range []cogs.FormatInput{
		{Name: "½ bbl", Kind: cogs.FormatKeg, BBLPerUnit: 0.5},
		{Name: "⅙ bbl", Kind: cogs.FormatKeg, BBLPerUnit: 1.0 / 6},
		{Name: "16oz can", Kind: cogs.FormatCan, BBLPerUnit: 16.0 / 128 / 31, UnitsPerCase: 24},
	} {
		id, err := svc.SaveFormat(ctx, admin.TenantID, nil, f)
		if err != nil {
			return nil, fmt.Errorf("saving format %s: %w", f.Name, err)
		}
		ids[f.Name] = id
	}
	return ids, nil
}

// seedCogsHistory leaves FV-1 dry hopped, FV-2 freshly brewed, FV-3 empty
// and dirty after moving to the brite, and BT-1 part packaged.
func seedCogsHistory(ctx context.Context, svc *cogs.Service, admin *services.Caller,
	v, f, b map[string]uuid.UUID,
) error {
	loc, err := time.LoadLocation(admin.Timezone)
	if err != nil {
		return fmt.Errorf("loading time zone: %w", err)
	}
	day := func(ago int) string { return time.Now().In(loc).AddDate(0, 0, -ago).Format("2006-01-02") }
	ref := func(m map[string]uuid.UUID, k string) *uuid.UUID { id := m[k]; return &id }

	steps := []cogs.ActionInput{
		{Kind: cogs.EvBrew, Date: day(20), VesselID: ref(v, "FV-3"), BeerID: ref(b, "Moon Dust Lager"), VolumeBBL: 10},
		{Kind: cogs.EvBrew, Date: day(12), VesselID: ref(v, "FV-1"), BeerID: ref(b, "Galactic Orbit"), VolumeBBL: 10},
		{Kind: cogs.EvDryHop, Date: day(5), VesselID: ref(v, "FV-1")},
		{Kind: cogs.EvTransfer, Date: day(4), VesselID: ref(v, "FV-3"), ToVesselID: ref(v, "BT-1"),
			VolumeBBL: 10, VolumeInBBL: 9.6, Empty: true},
		{Kind: cogs.EvPackage, Date: day(2), VesselID: ref(v, "BT-1"), Lines: []cogs.LineInput{
			{FormatID: f["½ bbl"], Full: 8}, {FormatID: f["⅙ bbl"], Full: 6},
		}},
		{Kind: cogs.EvBrew, Date: day(1), VesselID: ref(v, "FV-2"), BeerID: ref(b, "Comet Tail Stout"), VolumeBBL: 10},
	}
	for _, in := range steps {
		if _, err := svc.Record(ctx, admin, in); err != nil {
			return fmt.Errorf("recording %s on %s: %w", in.Kind, in.Date, err)
		}
	}
	return nil
}
