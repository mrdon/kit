package menu

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBeerSlugMatchesTheTagsAndKeys(t *testing.T) {
	for in, want := range map[string]string{
		"Wicked Nebula":             "wicked-nebula",
		"Viener Vino (Barrel-Aged)": "viener-vino-barrel-aged",
		"2026 Tsar Bomba":           "2026-tsar-bomba",
		"  Light Lift  Lager ":      "light-lift-lager",
	} {
		if got := beerSlug(in); got != want {
			t.Errorf("beerSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// Any shape of photo comes out as the box's portrait slot, as a JPEG.
func TestCropPortraitFitsTheSlot(t *testing.T) {
	for _, size := range [][2]int{{900, 600}, {400, 1200}, {500, 500}} {
		out, err := cropPortrait(testPNG(t, size[0], size[1]))
		if err != nil {
			t.Fatalf("%v: %v", size, err)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("%v: output is not a JPEG: %v", size, err)
		}
		b := img.Bounds()
		if got := float64(b.Dx()) / float64(b.Dy()); got < featuredPhotoAspect-0.02 || got > featuredPhotoAspect+0.02 {
			t.Errorf("%v: aspect %.2f, want %.2f", size, got, featuredPhotoAspect)
		}
	}
	if _, err := cropPortrait([]byte("not an image")); err == nil {
		t.Error("garbage should fail to crop")
	}
}

// The box needs its beer on the printed list; the row is starred; the label
// and text fall back when left empty.
func TestResolveFeatured(t *testing.T) {
	a := &App{}
	rows := []Beer{
		{Name: "Golden Mosaic", Notes: "Tropical."},
		{Name: "Wicked Nebula", Notes: "Juicy hops."},
	}
	f := a.resolveFeatured(context.Background(), uuid.Nil,
		&FeaturedConfig{Beer: "wicked  nebula", Photo: photoNone}, rows)
	if f == nil {
		t.Fatal("a beer on the list should be featured")
	}
	if !rows[1].Featured || rows[0].Featured {
		t.Error("only the featured beer's row is starred")
	}
	if f.Label != defaultFeaturedLabel || f.blurb() != "Juicy hops." {
		t.Errorf("defaults: label %q, blurb %q", f.Label, f.blurb())
	}
	f = a.resolveFeatured(context.Background(), uuid.Nil,
		&FeaturedConfig{Beer: "Wicked Nebula", Label: "Last keg", Text: "Get it now."}, rows)
	if f.Label != "Last keg" || f.blurb() != "Get it now." {
		t.Errorf("set: label %q, blurb %q", f.Label, f.blurb())
	}
	if a.resolveFeatured(context.Background(), uuid.Nil, &FeaturedConfig{Beer: "Kicked Keg"}, rows) != nil {
		t.Error("a beer that is not pouring must not be featured")
	}
}

func TestPrepareFeaturedValidates(t *testing.T) {
	ctx := context.Background()
	cfg := PrintConfig{Featured: &FeaturedConfig{Beer: "  "}}
	if err := prepareFeatured(ctx, nil, uuid.Nil, &cfg); err != nil || cfg.Featured != nil {
		t.Errorf("an empty beer clears the setting: %v %+v", err, cfg.Featured)
	}
	cfg = PrintConfig{Featured: &FeaturedConfig{Beer: "X", Label: strings.Repeat("a", 31), Photo: photoNone}}
	if err := prepareFeatured(ctx, nil, uuid.Nil, &cfg); !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("long label: %v", err)
	}
	cfg = PrintConfig{Featured: &FeaturedConfig{Beer: "X", Photo: "https://example.com/a.png"}}
	if err := prepareFeatured(ctx, nil, uuid.Nil, &cfg); !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("a URL is not a photo choice: %v", err)
	}
}

// The box is planned first, at the head of the first column, with no heading.
func TestFeaturedBoxLeadsTheMenu(t *testing.T) {
	pdf := newMeasuringPDF(t)
	f := &Featured{Beer: layoutBeer("Wicked Nebula"), Label: "Brewer's pick"}
	sections := append([]PrintSection{{featured: f}}, layoutSections(3, 3)...)
	plan, _ := planLayout(pdf, sections)
	first := plan[0]
	if first.unit.Featured == nil || first.heading || first.page != 0 || first.col != 0 || first.y != bodyTopFirst {
		t.Fatalf("first placement = %+v, want the featured box at the top of page one", first)
	}
	for _, withPhoto := range []bool{false, true} {
		m := PrintMenu{Title: "Beers", Featured: f, Sections: layoutSections(3, 3)}
		if withPhoto {
			photo, err := cropPortrait(testPNG(t, 300, 450))
			if err != nil {
				t.Fatal(err)
			}
			f.Photo, f.PhotoKind = photo, "JPG"
		}
		var buf bytes.Buffer
		if err := RenderPrintPDF(m, &buf); err != nil {
			t.Fatalf("rendering (photo %v): %v", withPhoto, err)
		}
	}
}

// Saving with "auto" finds the menu image named for the beer and stores it;
// printing then boxes the beer with that photo and stars its row.
func TestFeaturedAutoPhotoEndToEnd(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_menu_feat_" + uuid.NewString()
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "Feature Test", "enc",
		models.SanitizeSlug("feat-"+uuid.NewString(), teamID), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	if err := UpsertAsset(ctx, pool, tenant.ID, &Asset{Key: "beer-wicked-nebula", Mime: "image/png", Bytes: testPNG(t, 400, 600)}); err != nil {
		t.Fatal(err)
	}
	rows := []Beer{{Section: "IPAs", Name: "Wicked Nebula", Style: "IPA - Hazy", ABV: "6.5%", Notes: "Juicy.",
		Pours: []Pour{{Size: "16oz", Label: "16oz Draft", Price: "8"}}}}
	if err := SavePrintRows(ctx, pool, tenant.ID, rows, ""); err != nil {
		t.Fatal(err)
	}
	cfg := PrintConfig{Featured: &FeaturedConfig{Beer: "Wicked Nebula", Photo: photoAuto}}
	if err := prepareFeatured(ctx, pool, tenant.ID, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Featured.Photo != "asset:beer-wicked-nebula" {
		t.Fatalf("auto picked %q, want the beer's menu image", cfg.Featured.Photo)
	}
	if err := SavePrintConfig(ctx, pool, tenant.ID, cfg); err != nil {
		t.Fatal(err)
	}

	m, err := (&App{pool: pool}).buildPrintMenu(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if m.Featured == nil || len(m.Featured.Photo) == 0 {
		t.Fatalf("printed menu has no featured box or no photo: %+v", m.Featured)
	}
	if !m.Sections[0].Rows[0].Featured {
		t.Error("the featured beer's own row should carry its star")
	}
}
