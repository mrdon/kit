package menu

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// A description typed on the device lands in the written layer under the
// board's exact name, replaces any older spelling of the same beer, and is
// removed by an empty save without touching the other beers.
func TestSetPrintNoteWrittenLayer(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_menu_note_" + uuid.NewString()
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "Note Test", "enc", models.SanitizeSlug("note-"+uuid.NewString(), teamID), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	// An older hand-typed entry under a looser spelling, and a stored one.
	if err := SavePrintConfig(ctx, pool, tenant.ID, PrintConfig{Notes: map[string]string{"Mr Radar Nitro": "Old words.", "Mars Water": "A lager."}}); err != nil {
		t.Fatal(err)
	}
	if err := MergePrintNotes(ctx, pool, tenant.ID, map[string]string{"mr radar nitro": "Scraped."}); err != nil {
		t.Fatal(err)
	}

	if err := SetPrintNote(ctx, pool, tenant.ID, "Mr. Radar (Nitro)", "  New words.  "); err != nil {
		t.Fatal(err)
	}
	state, err := LoadPrintState(ctx, pool, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Config.Notes["Mr. Radar (Nitro)"] != "New words." || state.Config.Notes["Mr Radar Nitro"] != "" || state.Config.Notes["Mars Water"] != "A lager." {
		t.Fatalf("config notes = %v", state.Config.Notes)
	}
	if state.Notes["mr radar nitro"] != "Scraped." {
		t.Fatal("the stored layer was touched")
	}
	state.Rows = []Beer{{Name: "Mr. Radar (Nitro)", Section: "Dark"}}
	beers := printBeers(state)
	if len(beers) != 1 || beers[0].Note != "New words." || !beers[0].Written {
		t.Fatalf("printBeers = %+v", beers)
	}

	if err := SetPrintNote(ctx, pool, tenant.ID, "Mr. Radar (Nitro)", ""); err != nil {
		t.Fatal(err)
	}
	state, _ = LoadPrintState(ctx, pool, tenant.ID)
	if _, ok := state.Config.Notes["Mr. Radar (Nitro)"]; ok || state.Config.Notes["Mars Water"] != "A lager." {
		t.Fatalf("after clearing, config notes = %v", state.Config.Notes)
	}
	state.Rows = []Beer{{Name: "Mr. Radar (Nitro)", Section: "Dark"}}
	if beers := printBeers(state); beers[0].Note != "Scraped." || beers[0].Written {
		t.Fatalf("stored description did not show through: %+v", beers)
	}
}
