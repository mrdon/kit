package menu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // decode PNG assets
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	xdraw "golang.org/x/image/draw"

	"github.com/mrdon/kit/internal/apps/posters"
)

// Choosing the featured beer and its photo.
//
// The setting names a beer, a reason ("Brewer's pick") and optionally its own
// text. The photo is the part with judgement in it, and none of that judgement
// happens at print time: printing reads only what is stored, so the photo is
// found and kept when the setting is saved, the way set_menu_asset keeps any
// image. "Found" is a lookup by name through what Kit already knows, in order:
//
//  1. the wall's spotlight, which pairs beer names with images already;
//  2. a stored menu image named for the beer, "beer-wicked-nebula";
//  3. a library photo tagged with the beer, "wicked-nebula" -- the tag the
//     photo indexer wrote when the photo came in.
//
// Nothing found is a box with no photo, which is a complete design rather
// than a gap.

// FeaturedConfig is the stored setting.
type FeaturedConfig struct {
	Beer  string `json:"beer"`
	Label string `json:"label,omitempty"` // "Brewer's pick" when empty
	Text  string `json:"text,omitempty"`  // the beer's description when empty
	// Photo is "asset:<key>" once saved, or "none". On the way in it may also
	// be "auto" (or empty) to look one up, or "photo:<id>" for a library
	// photo, which is fetched and kept under featuredAssetKey.
	Photo string `json:"photo,omitempty"`
}

const (
	defaultFeaturedLabel = "Brewer's pick"
	featuredAssetKey     = "print-featured"
	photoNone            = "none"
	photoAuto            = "auto"
	featuredLabelMax     = 30
	featuredTextMax      = 240
)

// prepareFeatured validates the featured setting and settles its photo into a
// stored image, so the print path has nothing left to fetch. Both the console
// and set_menu_print call it, so the two surfaces store the same thing.
func prepareFeatured(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, cfg *PrintConfig) error {
	f := cfg.Featured
	if f == nil {
		return nil
	}
	f.Beer = strings.TrimSpace(f.Beer)
	f.Label = strings.TrimSpace(f.Label)
	f.Text = strings.TrimSpace(f.Text)
	switch {
	case f.Beer == "":
		cfg.Featured = nil // "no featured beer"
		return nil
	case len([]rune(f.Label)) > featuredLabelMax:
		return fmt.Errorf("%w: featured label is over %d characters", ErrPayloadInvalid, featuredLabelMax)
	case len([]rune(f.Text)) > featuredTextMax:
		return fmt.Errorf("%w: featured text is over %d characters", ErrPayloadInvalid, featuredTextMax)
	}

	ref := strings.TrimSpace(f.Photo)
	if ref == "" || ref == photoAuto {
		ref = photoNone
		if cands, err := featuredCandidates(ctx, pool, tenantID, f.Beer); err == nil && len(cands) > 0 {
			ref = cands[0].Ref
		}
	}
	stored, err := keepFeaturedPhoto(ctx, pool, tenantID, ref)
	if err != nil {
		return err
	}
	f.Photo = stored
	return nil
}

// keepFeaturedPhoto turns a photo choice into what is stored: a library photo
// is fetched once and kept as a menu image; a menu image is checked to exist.
func keepFeaturedPhoto(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, ref string) (string, error) {
	switch {
	case ref == photoNone:
		return photoNone, nil
	case strings.HasPrefix(ref, AssetRef):
		if _, err := loadPrintAsset(ctx, pool, tenantID, strings.TrimPrefix(ref, AssetRef)); err != nil {
			return "", fmt.Errorf("%w: no stored image %q", ErrPayloadInvalid, ref)
		}
		return ref, nil
	case strings.HasPrefix(ref, photoRef):
		raw, err := libraryPhoto(ctx, tenantID, ref)
		if err != nil {
			return "", fmt.Errorf("fetching %s for the featured beer: %w", ref, err)
		}
		a := &Asset{Key: featuredAssetKey, Mime: "image/jpeg", Bytes: raw, SourceURL: ref}
		if err := UpsertAsset(ctx, pool, tenantID, a); err != nil {
			return "", err
		}
		return AssetRef + featuredAssetKey, nil
	}
	return "", fmt.Errorf("%w: featured photo must be auto, none, asset:<key> or photo:<id>", ErrPayloadInvalid)
}

// photoCandidate is one photo the featured box could use.
type photoCandidate struct {
	Ref   string `json:"ref"`
	Label string `json:"label"`
}

// featuredCandidates lists the photos of a beer Kit can find by name, best
// first: the spotlight's, then a menu image named for it, then library photos
// tagged with it.
func featuredCandidates(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, beer string) ([]photoCandidate, error) {
	var out []photoCandidate
	seen := map[string]bool{}
	add := func(ref, label string) {
		if ref != "" && !seen[ref] {
			seen[ref] = true
			out = append(out, photoCandidate{Ref: ref, Label: label})
		}
	}
	if img := spotlightImage(ctx, pool, tenantID, beer); img != "" {
		add(img, "From the wall's spotlight")
	}
	key := "beer-" + beerSlug(beer)
	if _, err := loadPrintAsset(ctx, pool, tenantID, key); err == nil {
		add(AssetRef+key, "Menu image "+key)
	}
	if app := posters.Instance(); app != nil {
		photos, err := app.BeerPhotos(ctx, tenantID, beerSlug(beer), 12)
		if err != nil {
			return out, err
		}
		for _, p := range photos {
			add(photoRef+p.ID.String(), p.Description)
		}
	}
	return out, nil
}

// spotlightImage is the image the wall's spotlight shows for a beer, if it
// has a stored one.
func spotlightImage(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, beer string) string {
	row, err := GetBoard(ctx, pool, tenantID)
	if err != nil || row == nil {
		return ""
	}
	board, err := ParseBoard(row.Payload)
	if err != nil {
		return ""
	}
	want := nameKey(beer)
	for _, p := range board.Panels {
		for _, b := range p.Beers {
			if nameKey(b.Name) == want && strings.HasPrefix(b.Image, AssetRef) {
				return b.Image
			}
		}
	}
	return ""
}

// beerSlug is a beer name as menu image keys and photo tags spell it:
// lowercase letters and digits, words joined by single hyphens.
func beerSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	return b.String()
}

// featuredPhotoAspect is the box's photo slot, width over height.
const featuredPhotoAspect = featPhotoW / featPhotoH

// cropPortrait cuts an image to the box's portrait slot, centred a little
// above the middle where a glass usually is, and scales it to a size that
// prints sharp without carrying a camera original into the PDF.
func cropPortrait(raw []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decoding featured photo: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("featured photo is empty")
	}
	cw, ch := w, int(float64(w)/featuredPhotoAspect)
	if ch > h {
		ch, cw = h, int(float64(h)*featuredPhotoAspect)
	}
	x0 := b.Min.X + (w-cw)/2
	y0 := b.Min.Y + clampInt(int(float64(h)*0.45)-ch/2, 0, h-ch)
	const outH = 420
	outW := int(float64(outH) * featuredPhotoAspect)
	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, image.Rect(x0, y0, x0+cw, y0+ch), xdraw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("encoding featured photo: %w", err)
	}
	return out.Bytes(), nil
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}
