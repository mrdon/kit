package cogs

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Regression tests for the ways a late entry, a reversal or an odd input
// used to create or lose beer without anyone being told.

func (f *fixture) daysAgo(n int) string {
	loc, _ := time.LoadLocation("America/Denver")
	return time.Now().In(loc).AddDate(0, 0, -n).Format("2006-01-02")
}

func TestBackdatedPackageCantDoubleCountAMovedTank(t *testing.T) {
	f := newFixture(t)
	f.record(f.admin, ActionInput{Kind: EvBrew, VesselID: f.id("FV-1"), BeerID: &f.beer, VolumeBBL: 10, Date: f.daysAgo(3)})
	f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-1"), ToVesselID: f.id("BT-1"), VolumeBBL: 10})
	_, err := f.svc.Record(f.ctx, f.device, ActionInput{Kind: EvPackage, VesselID: f.id("BT-1"), Date: f.daysAgo(1),
		Empty: true, Lines: []LineInput{{FormatID: f.formats["½ bbl"], Full: 20}}})
	if err == nil {
		t.Fatal("packaging BT-1 the day before it was filled was accepted")
	}
	if v := f.vessel("BT-1"); !near(v.Volume, 10) || f.units("½ bbl") != 0 {
		t.Fatalf("BT-1 = %.2f with %v kegs, want 10 and none", v.Volume, f.units("½ bbl"))
	}
}

func TestBackdatedLevelReadsAgainstThatDay(t *testing.T) {
	f := newFixture(t)
	f.record(f.admin, ActionInput{Kind: EvBrew, VesselID: f.id("FV-1"), BeerID: &f.beer, VolumeBBL: 10, Date: f.daysAgo(3)})
	f.record(f.device, ActionInput{Kind: EvPackage, VesselID: f.id("FV-1"),
		Lines: []LineInput{{FormatID: f.formats["½ bbl"], Full: 10}}})
	f.record(f.device, ActionInput{Kind: EvLevel, VesselID: f.id("FV-1"), Reading: 9.5, Date: f.daysAgo(1)})
	if v := f.vessel("FV-1"); !near(v.Volume, 4.5) {
		t.Fatalf("FV-1 = %.2f, want 4.5", v.Volume)
	}
}

func TestUndoRefusedOnceKegsWereRepacked(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	pkg := f.record(f.device, ActionInput{Kind: EvPackage, VesselID: f.id("FV-1"),
		Lines: []LineInput{{FormatID: f.formats["½ bbl"], Full: 2}}})
	f.record(f.device, ActionInput{Kind: EvRepack, Take: &LineInput{BeerID: &f.beer, FormatID: f.formats["½ bbl"], Units: 2},
		Lines: []LineInput{{FormatID: f.formats["⅙ bbl"], Full: 6}}})
	if _, err := f.svc.Undo(f.ctx, f.device, pkg.ActionID); !errors.Is(err, ErrNotUndoable) {
		t.Fatalf("device undo: err = %v", err)
	}
	if _, err := f.svc.Undo(f.ctx, f.admin, pkg.ActionID); !errors.Is(err, ErrNotUndoable) {
		t.Fatalf("admin undo: err = %v", err)
	}
}

func TestCountCantBeBackdated(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Record(f.ctx, f.device, ActionInput{Kind: EvCount, Date: f.daysAgo(1)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("backdated count: err = %v", err)
	}
}

func TestUndoneBrewPutsAMissedPlanBack(t *testing.T) {
	f := newFixture(t)
	plan, err := f.svc.SavePlan(f.ctx, f.admin.TenantID, nil, PlanInput{BeerID: f.beer, VesselID: *f.id("FV-1"),
		BrewOn: f.daysAgo(2), VolumeBBL: 10})
	if err != nil {
		t.Fatal(err)
	}
	res := f.record(f.device, ActionInput{Kind: EvBrew, VesselID: f.id("FV-1"), BeerID: &f.beer, VolumeBBL: 10,
		PlanEntryID: &plan})
	if _, err := f.svc.Undo(f.ctx, f.device, res.ActionID); err != nil {
		t.Fatal(err)
	}
	entries, err := f.svc.Plan(f.ctx, f.admin.TenantID, f.daysAgo(0))
	if err != nil || len(entries) != 1 || entries[0].BatchID != nil {
		t.Fatalf("plan = %+v %v, want the entry back and open", entries, err)
	}
}

func TestBarrelFillDropsDuplicatesAndUsesTheBarrelSize(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	b1 := *f.id("B-1")
	f.record(f.device, ActionInput{Kind: "barrel_fill", VesselID: f.id("FV-1"), ToVessels: []uuid.UUID{b1, b1}})
	if v := f.vessel("B-1"); !near(v.Volume, 1.7) {
		t.Fatalf("B-1 = %.2f, want 1.7", v.Volume)
	}
	if v := f.vessel("FV-1"); !near(v.Volume, 8.3) {
		t.Fatalf("FV-1 = %.2f, want 8.3", v.Volume)
	}
}

func TestPackagingTheWholeTankEmptiesIt(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	res := f.record(f.device, ActionInput{Kind: EvPackage, VesselID: f.id("FV-1"),
		Lines: []LineInput{{FormatID: f.formats["½ bbl"], Full: 20}, {FormatID: f.formats["⅙ bbl"], Full: 0.2}}})
	if v := f.vessel("FV-1"); !v.Empty() {
		t.Fatalf("FV-1 = %+v after packaging all of it (%s)", v.VesselState, res.Message)
	}
	if !strings.Contains(res.Message, "more than recorded") {
		t.Fatalf("message = %q, want the gain noted", res.Message)
	}
}

func TestKickIsNotALoss(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 10)
	f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-1"), ToVesselID: f.id("SV-1"), VolumeBBL: 10})
	f.record(f.device, ActionInput{Kind: EvOnTap, VesselID: f.id("SV-1")})
	res := f.record(f.device, ActionInput{Kind: EvKick, VesselID: f.id("SV-1")})
	var loss float64
	if err := f.svc.pool.QueryRow(f.ctx, `SELECT loss_bbl::float8 FROM app_cogs_events WHERE tenant_id = $1
		AND action_id = $2 AND kind = 'kick'`, f.admin.TenantID, res.ActionID).Scan(&loss); err != nil || loss != 0 {
		t.Fatalf("kick loss = %v %v, want 0", loss, err)
	}
}

func TestBlendKeepsBothBatches(t *testing.T) {
	f := newFixture(t)
	f.brew("FV-1", 5)
	f.record(f.admin, ActionInput{Kind: EvBrew, VesselID: f.id("FV-7"), BeerID: &f.beer, VolumeBBL: 5, Date: f.daysAgo(1)})
	f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-1"), ToVesselID: f.id("BT-1"), VolumeBBL: 5})
	f.record(f.device, ActionInput{Kind: EvTransfer, VesselID: f.id("FV-7"), ToVesselID: f.id("BT-1"), VolumeBBL: 5})
	bt := f.vessel("BT-1")
	var parents int
	if err := f.svc.pool.QueryRow(f.ctx, `SELECT COUNT(*) FROM app_cogs_batch_parents WHERE tenant_id = $1
		AND batch_id = $2`, f.admin.TenantID, *bt.BatchID).Scan(&parents); err != nil || parents != 2 || !near(bt.Volume, 10) {
		t.Fatalf("BT-1 = %.1f bbl, %d parents (%v), want 10 bbl of a blend of 2", bt.Volume, parents, err)
	}
}

// The pumpkin sixtel: a sixtel pulled from a ½ becomes a variant, and the
// ½ goes back on the shelf as a partial with its original date.
func TestRepackKeepsTheRestAndTheDate(t *testing.T) {
	f := newFixture(t)
	f.record(f.admin, ActionInput{Kind: EvBrew, VesselID: f.id("FV-1"), BeerID: &f.beer, VolumeBBL: 10, Date: f.daysAgo(6)})
	f.record(f.admin, ActionInput{Kind: EvPackage, VesselID: f.id("FV-1"), Date: f.daysAgo(5),
		Lines: []LineInput{{FormatID: f.formats["½ bbl"], Full: 20}}})
	f.record(f.device, ActionInput{Kind: EvRepack, NewBeerName: "Pumpkin Orbit", KeepRest: true,
		Take:  &LineInput{BeerID: &f.beer, FormatID: f.formats["½ bbl"], Units: 1},
		Lines: []LineInput{{FormatID: f.formats["⅙ bbl"], Full: 1}}})
	if got := f.units("½ bbl"); !near(got, 19.67) {
		t.Fatalf("½ kegs = %v, want 19 and two thirds", got)
	}
	pumpkin, err := findBeerByName(f.ctx, f.svc.pool, f.admin.TenantID, "Pumpkin Orbit")
	if err != nil {
		t.Fatal(err)
	}
	lots, err := listLots(f.ctx, f.svc.pool, f.admin.TenantID, &pumpkin.ID, nil)
	if err != nil || len(lots) != 1 || dateKey(lots[0].PackagedOn) != f.daysAgo(5) {
		t.Fatalf("pumpkin lots = %+v %v, want one packaged %s", lots, err, f.daysAgo(5))
	}
}
