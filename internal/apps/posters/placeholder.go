package posters

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"

	"github.com/mrdon/kit/internal/posterrender"
)

// When no photo fits an event, a batch still comes out: a flat grey stands
// where the photo goes, so there is a poster to iterate on. Chat swaps it
// for a library photo, a stock photo or an upload like any other photo.

// PlaceholderID is the photo id the stand-in travels under.
const PlaceholderID = "placeholder"

var (
	placeholderOnce sync.Once
	placeholderPNG  string
)

// placeholderRef is the stand-in as an inline image reference.
func placeholderRef() posterrender.ImageRef {
	placeholderOnce.Do(func() {
		img := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
		grey := color.RGBA{0x8c, 0x8c, 0x8c, 0xff}
		for y := range 1200 {
			for x := range 1600 {
				img.Set(x, y, grey)
			}
		}
		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		placeholderPNG = base64Encode(buf.Bytes())
	})
	return posterrender.ImageRef{ID: PlaceholderID, Source: posterrender.SourceInline, Data: placeholderPNG, Modified: "1", Folder: PlaceholderID}
}

// placeholderUse is the stand-in as a hero: centred, no zoom.
func placeholderUse() posterrender.PhotoUse {
	return posterrender.PhotoUse{ID: PlaceholderID, FocusX: 0.5, FocusY: 0.5, Zoom: 1}
}

// usesPlaceholder says whether a source still carries the stand-in.
func usesPlaceholder(source string) bool {
	return strings.Contains(source, `"`+PlaceholderID+`"`)
}
