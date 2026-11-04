package cogs

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

type fixture struct {
	t       *testing.T
	ctx     context.Context
	svc     *Service
	admin   *services.Caller
	device  *services.Caller
	vessels map[string]uuid.UUID
	formats map[string]uuid.UUID
	beer    uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_brewing_test_" + uuid.NewString()
	slug := models.SanitizeSlug("brewing-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "brewing-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	f := &fixture{t: t, ctx: ctx, svc: NewService(pool), vessels: map[string]uuid.UUID{},
		formats: map[string]uuid.UUID{},
		admin:   &services.Caller{TenantID: tenant.ID, IsAdmin: true, Label: "Don", Timezone: "America/Denver"},
		device: &services.Caller{Kind: services.CallerDevice, TenantID: tenant.ID, ActorID: uuid.New(),
			Label: "Brewhouse iPad", Timezone: "America/Denver"}}
	for _, v := range []VesselInput{
		{Name: "FV-1", Kind: KindFermenter, SizeBBL: 10}, {Name: "FV-7", Kind: KindFermenter, SizeBBL: 20},
		{Name: "BT-1", Kind: KindBrite, SizeBBL: 10}, {Name: "SV-1", Kind: KindServing, SizeBBL: 10},
		{Name: "SV-2", Kind: KindServing, SizeBBL: 10}, {Name: "B-1", Kind: KindBarrel, SizeBBL: 1.7},
	} {
		id, err := f.svc.SaveVessel(ctx, tenant.ID, nil, v)
		if err != nil {
			t.Fatalf("saving vessel: %v", err)
		}
		f.vessels[v.Name] = id
	}
	for _, fm := range []FormatInput{
		{Name: "½ bbl", Kind: FormatKeg, BBLPerUnit: 0.5}, {Name: "⅙ bbl", Kind: FormatKeg, BBLPerUnit: 1.0 / 6},
		{Name: "16oz can", Kind: FormatCan, BBLPerUnit: 16.0 / 128 / 31, UnitsPerCase: 24},
	} {
		id, err := f.svc.SaveFormat(ctx, tenant.ID, nil, fm)
		if err != nil {
			t.Fatalf("saving format: %v", err)
		}
		f.formats[fm.Name] = id
	}
	f.beer, err = f.svc.SaveBeer(ctx, tenant.ID, nil, NewBeerDefaults("Galactic Orbit"))
	if err != nil {
		t.Fatalf("saving beer: %v", err)
	}
	return f
}

func (f *fixture) id(name string) *uuid.UUID {
	id, ok := f.vessels[name]
	if !ok {
		f.t.Fatalf("no vessel %s", name)
	}
	return &id
}

func (f *fixture) record(c *services.Caller, in ActionInput) *ActionResult {
	f.t.Helper()
	res, err := f.svc.Record(f.ctx, c, in)
	if err != nil {
		f.t.Fatalf("%s: %v", in.Kind, err)
	}
	return res
}

func (f *fixture) vessel(name string) *Vessel {
	f.t.Helper()
	v, err := getVessel(f.ctx, f.svc.pool, f.admin.TenantID, *f.id(name))
	if err != nil {
		f.t.Fatalf("loading %s: %v", name, err)
	}
	return v
}

func (f *fixture) units(format string) float64 {
	f.t.Helper()
	fid := f.formats[format]
	lots, err := listLots(f.ctx, f.svc.pool, f.admin.TenantID, &f.beer, &fid)
	if err != nil {
		f.t.Fatalf("listing lots: %v", err)
	}
	total := 0.0
	for _, l := range lots {
		total += l.Units
	}
	return total
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func (f *fixture) brew(vessel string, vol float64) {
	f.t.Helper()
	f.record(f.device, ActionInput{Kind: EvBrew, VesselID: f.id(vessel), BeerID: &f.beer, VolumeBBL: vol})
}

func TestBrewMovePackageAndUndo(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-1"), ToVesselID: f.id("BT-1"),
		VolumeBBL: 10, VolumeInBBL: 9.6, Empty: true})
	if v := f.vessel("FV-1"); !v.Empty() || !v.Dirty {
		t.Fatalf("FV-1 after the move: %+v, want empty and dirty", v.VesselState)
	}
	if v := f.vessel("BT-1"); !near(v.Volume, 9.6) || v.Stage != StageBrite {
		t.Fatalf("BT-1 = %.2f bbl %s, want 9.6 brite", v.Volume, v.Stage)
	}
	res := f.record(f.device, ActionInput{Kind: EvPackage, VesselID: f.id("BT-1"), Lines: []LineInput{
		{FormatID: f.formats["½ bbl"], Full: 14}, {FormatID: f.formats["⅙ bbl"], Full: 12},
	}})
	if !strings.Contains(res.Message, "Packaged 14 × ½ bbl, 12 × ⅙ bbl") {
		t.Fatalf("message = %q", res.Message)
	}
	if v := f.vessel("BT-1"); !near(v.Volume, 0.6) {
		t.Fatalf("BT-1 after packaging = %.2f, want 0.6 left", v.Volume)
	}
	if got := f.units("½ bbl"); got != 14 {
		t.Fatalf("½ kegs = %v, want 14", got)
	}
	if _, err := f.svc.Undo(f.ctx, f.device, res.ActionID); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if v := f.vessel("BT-1"); !near(v.Volume, 9.6) {
		t.Fatalf("BT-1 after undo = %.2f, want 9.6", v.Volume)
	}
	if got := f.units("½ bbl"); got != 0 {
		t.Fatalf("½ kegs after undo = %v, want 0", got)
	}
	if _, err := f.svc.Undo(f.ctx, f.device, res.ActionID); !errors.Is(err, ErrNotUndoable) {
		t.Fatalf("second undo: err = %v, want ErrNotUndoable", err)
	}
}

func TestDoubleBrewAddsToTheSameBatch(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-7", 10)
	first := f.vessel("FV-7").BatchID
	f.brew("FV-7", 10)
	v := f.vessel("FV-7")
	if !near(v.Volume, 20) || *v.BatchID != *first {
		t.Fatalf("FV-7 = %.1f bbl batch %v, want 20 in batch %v", v.Volume, v.BatchID, first)
	}
}

func TestRefusesOverdrawAndBlending(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	_, err := f.svc.Record(f.ctx, f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-1"),
		ToVesselID: f.id("BT-1"), VolumeBBL: 12})
	if !errors.Is(err, ErrOverdraw) {
		t.Fatalf("overdraw: err = %v", err)
	}
	other, err := f.svc.SaveBeer(f.ctx, f.admin.TenantID, nil, NewBeerDefaults("Wicked Nebula"))
	if err != nil {
		t.Fatal(err)
	}
	f.record(f.device, ActionInput{Kind: EvBrew, VesselID: f.id("FV-7"), BeerID: &other, VolumeBBL: 10})
	f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-7"), ToVesselID: f.id("BT-1"), VolumeBBL: 5})
	_, err = f.svc.Record(f.ctx, f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-1"),
		ToVesselID: f.id("BT-1"), VolumeBBL: 2})
	if !errors.Is(err, ErrBlend) {
		t.Fatalf("blend: err = %v", err)
	}
}

func TestKickPutsTheSiblingOnTap(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-1"), ToVesselID: f.id("SV-1"), VolumeBBL: 5})
	f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-1"), ToVesselID: f.id("SV-2"),
		VolumeBBL: 5, Empty: true})
	f.record(f.device, ActionInput{Kind: EvOnTap, VesselID: f.id("SV-1")})
	res := f.record(f.device, ActionInput{Kind: EvKick, VesselID: f.id("SV-1")})
	if !f.vessel("SV-2").OnTap || !f.vessel("SV-1").Empty() {
		t.Fatalf("after the kick SV-1 should be empty and SV-2 on tap (%s)", res.Message)
	}
}

func TestRepackIntoAVariant(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	f.record(f.device, ActionInput{Kind: EvPackage, VesselID: f.id("FV-1"), Empty: true,
		Lines: []LineInput{{FormatID: f.formats["½ bbl"], Full: 19}}})
	res := f.record(f.device, ActionInput{Kind: EvRepack, NewBeerName: "Grapefruit Orbit", Added: "grapefruit",
		Take:  &LineInput{BeerID: &f.beer, FormatID: f.formats["½ bbl"], Units: 1},
		Lines: []LineInput{{FormatID: f.formats["⅙ bbl"], Full: 2}}})
	if !strings.Contains(res.Message, "0.17 bbl written off") {
		t.Fatalf("message = %q, want the shortfall written off", res.Message)
	}
	if got := f.units("½ bbl"); got != 18 {
		t.Fatalf("½ kegs = %v, want 18", got)
	}
	variant, err := findBeerByName(f.ctx, f.svc.pool, f.admin.TenantID, "grapefruit orbit")
	if err != nil || !variant.NeedsSetup {
		t.Fatalf("variant beer: %+v %v, want one that needs setup", variant, err)
	}
	lots, err := listLots(f.ctx, f.svc.pool, f.admin.TenantID, &variant.ID, nil)
	if err != nil || len(lots) != 1 || lots[0].BatchID == nil {
		t.Fatalf("variant lots = %+v %v", lots, err)
	}
	b, err := getBatch(f.ctx, f.svc.pool, f.admin.TenantID, *lots[0].BatchID)
	if err != nil || b.ParentBatchID == nil {
		t.Fatalf("variant batch = %+v %v, want a parent", b, err)
	}
	q, err := f.svc.OpenQueue(f.ctx, f.admin.TenantID)
	if err != nil || len(q) != 1 || q[0].Kind != QueueNewBeer {
		t.Fatalf("queue = %+v %v, want one new-beer item", q, err)
	}
}

func TestBlindCountResetsLots(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	f.record(f.device, ActionInput{Kind: EvPackage, VesselID: f.id("FV-1"), Empty: true,
		Lines: []LineInput{{FormatID: f.formats["½ bbl"], Full: 18}}})
	_, err := f.svc.Record(f.ctx, f.device, ActionInput{Kind: EvCount})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("count missing a row: err = %v", err)
	}
	f.record(f.device, ActionInput{Kind: EvCount, Lines: []LineInput{
		{BeerID: &f.beer, FormatID: f.formats["½ bbl"], Full: 15, Partial: 1},
	}})
	if got := f.units("½ bbl"); got != 15.5 {
		t.Fatalf("½ kegs after count = %v, want 15.5", got)
	}
	q, err := f.svc.OpenQueue(f.ctx, f.admin.TenantID)
	if err != nil || len(q) != 1 || q[0].Kind != QueueCountVariance {
		t.Fatalf("queue = %+v %v, want the variance", q, err)
	}
}

func TestDeviceUndoesOnlyTheNewest(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	first, err := f.svc.History(f.ctx, f.device, f.id("FV-1"), 5)
	if err != nil || len(first) != 1 || !first[0].CanUndo {
		t.Fatalf("history = %+v %v, want the brew undoable", first, err)
	}
	f.record(f.device, ActionInput{Kind: EvDryHop, VesselID: f.id("FV-1")})
	if _, err := f.svc.Undo(f.ctx, f.device, first[0].ActionID); !errors.Is(err, ErrNotUndoable) {
		t.Fatalf("device undoing an older entry: err = %v", err)
	}
	// An admin can, and the brewing that rested on it unwinds with it.
	if _, err := f.svc.Undo(f.ctx, f.admin, first[0].ActionID); err != nil {
		t.Fatalf("admin reversal: %v", err)
	}
	if !f.vessel("FV-1").Empty() {
		t.Fatalf("FV-1 should be empty with its brew reversed")
	}
}

func TestBarrelPullWritesOffEvaporation(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	f.record(f.device, ActionInput{Kind: "barrel_fill", VesselID: f.id("FV-1"), ToVessels: []uuid.UUID{*f.id("B-1")}})
	if v := f.vessel("B-1"); !near(v.Volume, 1.7) || v.Stage != StageAging {
		t.Fatalf("B-1 = %+v", v.VesselState)
	}
	res := f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("B-1"), ToVesselID: f.id("BT-1"),
		VolumeInBBL: 1.4, Empty: true})
	if !strings.Contains(res.Message, "0.30 bbl written off") || !f.vessel("B-1").Empty() {
		t.Fatalf("pull: %q", res.Message)
	}
}

func TestStateReadsTheTiles(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	st, err := f.svc.State(f.ctx, f.device)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range st.Vessels {
		if v.Name == "FV-1" && (v.StageLabel != "Fermenting" || v.BatchCode != "GO-01") {
			t.Fatalf("FV-1 tile = %s %s", v.StageLabel, v.BatchCode)
		}
		if v.Name == "FV-7" && v.Badge != BadgeEmpty {
			t.Fatalf("FV-7 badge = %q", v.Badge)
		}
	}
	if len(st.Schedule) != 3 {
		t.Fatalf("schedule lanes = %d, want the 2 fermenters and the brite", len(st.Schedule))
	}
}

func TestOccurredAt(t *testing.T) {
	loc, _ := time.LoadLocation("America/Denver")
	now := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)
	if got, _ := occurredAt("", loc, now, true); !got.Equal(now) {
		t.Fatalf("empty date = %v, want now", got)
	}
	got, err := occurredAt("2026-03-08", loc, now, true)
	if err != nil || got.Hour() != 12 || got.Day() != 8 {
		t.Fatalf("backdated = %v %v, want noon on the 8th", got, err)
	}
	if _, err := occurredAt("2026-02-20", loc, now, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too old from the iPad: err = %v", err)
	}
	if _, err := occurredAt("2026-02-20", loc, now, false); err != nil {
		t.Fatalf("console backdate: %v", err)
	}
	if _, err := occurredAt("2026-03-11", loc, now, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("future: err = %v", err)
	}
}

// A brewery with nothing set up is the first screen anyone sees; every list
// must go out as [] (the console reads .length on them), never null.
func TestEmptyBreweryHasNoNullLists(t *testing.T) {
	f := newFixture(t)
	other := *f.admin
	teamID := "T_cogs_empty_" + uuid.NewString()
	tenant, err := models.UpsertTenant(f.ctx, f.svc.pool, teamID, "empty", "x",
		models.SanitizeSlug("cogs-empty-"+uuid.NewString(), teamID), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.svc.pool.Exec(f.ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	other.TenantID = tenant.ID
	st, err := f.svc.State(f.ctx, &other)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := f.svc.Setup(f.ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{"state": st, "setup": setup} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"vessels", "beers", "formats", "this_week", "packaged", "schedule", "plan"} {
			if strings.Contains(string(raw), "\""+key+"\":null") {
				t.Errorf("%s has %s: null", name, key)
			}
		}
	}
}
